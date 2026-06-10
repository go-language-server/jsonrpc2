// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// rwc adapts an io.Reader and io.Writer into an io.ReadWriteCloser with a no-op
// Close, for driving a [Stream] from in-memory buffers.
type rwc struct {
	io.Reader
	io.Writer
}

func (rwc) Close() error { return nil }

// chunkReader hands out the underlying data in fixed-size chunks so that a
// single logical read is satisfied by several short reads, exercising the
// framer's partial-read and split-header handling.
type chunkReader struct {
	data []byte
	n    int // max bytes returned per Read
	pos  int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	end := min(c.pos+c.n, len(c.data))
	if end-c.pos > len(p) {
		end = c.pos + len(p)
	}
	n := copy(p, c.data[c.pos:end])
	c.pos += n
	return n, nil
}

// countingConn records the number of Write calls so a test can assert that a
// frame is emitted with a single contiguous write.
type countingConn struct {
	buf    bytes.Buffer
	writes atomic.Int64
}

func (c *countingConn) Read(p []byte) (int, error) { return c.buf.Read(p) }

func (c *countingConn) Write(p []byte) (int, error) { c.writes.Add(1); return c.buf.Write(p) }

func (c *countingConn) Close() error { return nil }

// streamPair connects two streams produced by framer over an in-memory pipe.
type streamPair struct {
	client Stream
	server Stream
}

func newStreamPair(framer Framer) (streamPair, func()) {
	c, s := net.Pipe()
	pair := streamPair{
		client: framer(c),
		server: framer(s),
	}
	cleanup := func() {
		_ = c.Close()
		_ = s.Close()
	}
	return pair, cleanup
}

// frameByteCount feeds buf through a Stream by chunking it n bytes at a time and
// returns the decoded message.
func frameByteCount(t *testing.T, framer Framer, buf []byte, chunk int) (Message, int64, error) {
	t.Helper()
	cr := &chunkReader{data: buf, n: chunk}
	s := framer(rwc{Reader: cr, Writer: io.Discard})
	return s.Read(t.Context())
}

// allFramers enumerates the two production framers under their descriptive
// constructors for shared round-trip coverage.
func allFramers() map[string]Framer {
	return map[string]Framer{
		"header": NewHeaderStream,
		"ndjson": NewNDJSONStream,
	}
}

// messageKey is a comparable projection of a Message used by go-cmp to compare a
// decoded message against the one that was written, without reaching into
// unexported fields.
type messageKey struct {
	kind   string
	method string
	id     string
	params string
	result string
	errMsg string
}

func toKey(t *testing.T, msg Message) messageKey {
	t.Helper()
	switch m := msg.(type) {
	case *Call:
		return messageKey{kind: "call", method: m.Method(), id: idToString(m.ID()), params: string(m.Params())}
	case *Notification:
		return messageKey{kind: "notification", method: m.Method(), params: string(m.Params())}
	case *Response:
		k := messageKey{kind: "response", id: idToString(m.ID()), result: string(m.Result())}
		if m.Err() != nil {
			k.errMsg = m.Err().Error()
		}
		return k
	default:
		t.Fatalf("unexpected message type %T", msg)
		return messageKey{}
	}
}

func idToString(id ID) string { return string(id.appendID(nil)) }

func TestStreamRoundTrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		msg Message
	}{
		"call with params": {
			msg: NewCall(NewNumberID(7), "textDocument/hover", RawMessage(`{"line":1,"character":2}`)),
		},
		"call with string id": {
			msg: NewCall(NewStringID("abc"), "initialize", RawMessage(`{}`)),
		},
		"call without params": {
			msg: NewCall(NewNumberID(1), "shutdown", nil),
		},
		"call with large params": {
			// A frame larger than bufio's default 4096-byte buffer exercises the
			// ndjson readLong accumulation path and the header rbuf growth path.
			msg: NewCall(NewNumberID(42), "big", RawMessage(`{"blob":"`+strings.Repeat("a", 8192)+`"}`)),
		},
		"notification": {
			msg: NewNotification("textDocument/didOpen", RawMessage(`{"uri":"file:///x"}`)),
		},
		"notification without params": {
			msg: NewNotification("exit", nil),
		},
		"response with result": {
			msg: NewResponse(NewNumberID(7), RawMessage(`{"contents":"x"}`), nil),
		},
		"response with error": {
			msg: NewResponse(NewNumberID(9), nil, NewError(InvalidParams, "bad params")),
		},
	}

	for framerName, framer := range allFramers() {
		for name, tt := range tests {
			t.Run(framerName+"/"+name, func(t *testing.T) {
				t.Parallel()
				pair, cleanup := newStreamPair(framer)
				defer cleanup()

				ctx := t.Context()
				writeErr := make(chan error, 1)
				go func() {
					_, err := pair.client.Write(ctx, tt.msg)
					writeErr <- err
				}()

				got, _, err := pair.server.Read(ctx)
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				if err := <-writeErr; err != nil {
					t.Fatalf("Write: %v", err)
				}

				if diff := gocmp.Diff(toKey(t, tt.msg), toKey(t, got), gocmp.AllowUnexported(messageKey{})); diff != "" {
					t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestHeaderStreamPartialReads(t *testing.T) {
	t.Parallel()

	body := []byte(`{"jsonrpc":"2.0","method":"ping","id":3}`)
	var frame bytes.Buffer
	fmt.Fprintf(&frame, "Content-Length: %d\r\n\r\n", len(body))
	frame.Write(body)

	// Each chunk size from 1 (byte-at-a-time, every split possible) up through
	// the whole frame in one read exercises split headers and short bodies.
	for chunk := 1; chunk <= frame.Len(); chunk++ {
		got, _, err := frameByteCount(t, NewHeaderStream, frame.Bytes(), chunk)
		if err != nil {
			t.Fatalf("chunk=%d: Read: %v", chunk, err)
		}
		call, ok := got.(*Call)
		if !ok {
			t.Fatalf("chunk=%d: got %T, want *Call", chunk, got)
		}
		if call.Method() != "ping" {
			t.Errorf("chunk=%d: method = %q, want ping", chunk, call.Method())
		}
	}
}

func TestNDJSONStreamPartialReads(t *testing.T) {
	t.Parallel()

	frame := append([]byte(`{"jsonrpc":"2.0","method":"ping","id":3}`), '\n')
	for chunk := 1; chunk <= len(frame); chunk++ {
		got, _, err := frameByteCount(t, NewNDJSONStream, frame, chunk)
		if err != nil {
			t.Fatalf("chunk=%d: Read: %v", chunk, err)
		}
		call, ok := got.(*Call)
		if !ok {
			t.Fatalf("chunk=%d: got %T, want *Call", chunk, got)
		}
		if call.Method() != "ping" {
			t.Errorf("chunk=%d: method = %q, want ping", chunk, call.Method())
		}
	}
}

func TestHeaderStreamContentLengthErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		frame string
	}{
		"missing content-length": {
			frame: "Content-Type: application/json\r\n\r\n{}",
		},
		"zero content-length": {
			frame: "Content-Length: 0\r\n\r\n",
		},
		"negative content-length": {
			frame: "Content-Length: -5\r\n\r\n{}",
		},
		"non-numeric content-length": {
			frame: "Content-Length: abc\r\n\r\n{}",
		},
		"field without colon": {
			frame: "Content-Length 5\r\n\r\n{}",
		},
		"overflowing content-length": {
			// 20 digits: would wrap a signed int without the overflow guard,
			// landing on a small positive length and desyncing the frame.
			frame: "Content-Length: 18446744073709551623\r\n\r\n{}",
		},
		"oversized content-length": {
			// ~10 GiB: numerically valid but above maxContentLength, so it must
			// be rejected before any body allocation rather than triggering an
			// out-of-memory make.
			frame: "Content-Length: 9999999999\r\n\r\n{}",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := NewHeaderStream(rwc{Reader: bytes.NewReader([]byte(tt.frame)), Writer: io.Discard})
			_, _, err := s.Read(t.Context())
			if !errors.Is(err, ErrInvalidHeader) {
				t.Errorf("Read err = %v, want ErrInvalidHeader", err)
			}
		})
	}
}

func TestHeaderStreamUnknownHeadersIgnored(t *testing.T) {
	t.Parallel()

	body := `{"jsonrpc":"2.0","method":"ping","id":1}`
	frame := "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\n" +
		"X-Custom-Header: whatever\r\n" +
		fmt.Sprintf("Content-Length: %d\r\n", len(body)) +
		"\r\n" + body

	s := NewHeaderStream(rwc{Reader: bytes.NewReader([]byte(frame)), Writer: io.Discard})
	got, _, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	call, ok := got.(*Call)
	if !ok {
		t.Fatalf("got %T, want *Call", got)
	}
	if call.Method() != "ping" {
		t.Errorf("method = %q, want ping", call.Method())
	}
}

func TestNDJSONStreamMultipleMessages(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	buf.WriteString(`{"jsonrpc":"2.0","method":"a","id":1}` + "\n")
	buf.WriteString(`{"jsonrpc":"2.0","method":"b"}` + "\n")
	buf.WriteString(`{"jsonrpc":"2.0","id":1,"result":42}` + "\n")

	s := NewNDJSONStream(rwc{Reader: bytes.NewReader(buf.Bytes()), Writer: io.Discard})
	ctx := t.Context()

	wantKinds := []string{"call", "notification", "response"}
	for i, want := range wantKinds {
		msg, _, err := s.Read(ctx)
		if err != nil {
			t.Fatalf("message %d: Read: %v", i, err)
		}
		if got := toKey(t, msg).kind; got != want {
			t.Errorf("message %d: kind = %q, want %q", i, got, want)
		}
	}

	// A fourth read at the clean boundary surfaces io.EOF.
	if _, _, err := s.Read(ctx); !errors.Is(err, io.EOF) {
		t.Errorf("trailing Read err = %v, want io.EOF", err)
	}
}

func TestEncodedPayloadHasNoLiteralNewline(t *testing.T) {
	t.Parallel()

	// A method name containing a raw newline is escaped by the wire encoder
	// (\n -> \\n), so the ndjson framer's single trailing newline is the only
	// 0x0A on the wire. This guards the ndjson frame-boundary invariant.
	msg := NewCall(NewNumberID(1), "line\nbreak", RawMessage(`{"k":"v"}`))

	var cc countingConn
	s := NewNDJSONStream(&cc)
	if _, err := s.Write(t.Context(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	wire := cc.buf.Bytes()
	if n := bytes.Count(wire, []byte{'\n'}); n != 1 {
		t.Fatalf("wire contains %d newlines, want exactly 1 (the delimiter):\n%q", n, wire)
	}
	if wire[len(wire)-1] != '\n' {
		t.Fatalf("frame does not end with a newline:\n%q", wire)
	}

	// The frame must still decode to the original message, newline intact.
	got, derr := DecodeMessage(wire[:len(wire)-1])
	if derr != nil {
		t.Fatalf("DecodeMessage: %v", derr)
	}
	call, ok := got.(*Call)
	if !ok {
		t.Fatalf("got %T, want *Call", got)
	}
	if call.Method() != "line\nbreak" {
		t.Errorf("method = %q, want %q", call.Method(), "line\nbreak")
	}
}

func TestNDJSONRawParamNewlineBreaksFraming(t *testing.T) {
	t.Parallel()

	// The ndjson frame-boundary invariant (NewNDJSONStream) holds only for
	// newline-free payloads. The wire encoder escapes the strings it controls,
	// but caller-supplied params are copied verbatim, so a RawMessage carrying a
	// literal 0x0A emits a second newline and desyncs the wire. This locks the
	// documented caller contract on the raw-params path that
	// TestEncodedPayloadHasNoLiteralNewline (a newline in the escaped method
	// name) does not exercise.
	msg := NewCall(NewNumberID(1), "m", RawMessage("{\n\"a\":1}"))

	var cc countingConn
	s := NewNDJSONStream(&cc)
	if _, err := s.Write(t.Context(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	wire := cc.buf.Bytes()
	if n := bytes.Count(wire, []byte{'\n'}); n != 2 {
		t.Fatalf("wire contains %d newlines, want 2 (raw param newline + delimiter); the verbatim raw-params contract is no longer violable:\n%q", n, wire)
	}

	// A reader splitting on '\n' sees a truncated first frame: the embedded
	// newline is read as a premature frame boundary, so the leading fragment no
	// longer decodes as a complete message.
	first, _, _ := bytes.Cut(wire, []byte{'\n'})
	if _, derr := DecodeMessage(first); derr == nil {
		t.Fatalf("first ndjson fragment decoded cleanly; expected the embedded newline to split the frame:\n%q", first)
	}
}

func TestStreamWriteSingleWrite(t *testing.T) {
	t.Parallel()

	tests := map[string]Framer{
		"header": NewHeaderStream,
		"ndjson": NewNDJSONStream,
	}
	for name, framer := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var cc countingConn
			s := framer(&cc)
			msg := NewCall(NewNumberID(1), "method", RawMessage(`{"a":1}`))
			if _, err := s.Write(t.Context(), msg); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if n := cc.writes.Load(); n != 1 {
				t.Errorf("conn.Write called %d times, want 1 (single-write framing)", n)
			}
		})
	}
}

func TestStreamReadEOF(t *testing.T) {
	t.Parallel()

	tests := map[string]Framer{
		"header": NewHeaderStream,
		"ndjson": NewNDJSONStream,
	}
	for name, framer := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := framer(rwc{Reader: bytes.NewReader(nil), Writer: io.Discard})
			_, _, err := s.Read(t.Context())
			if !errors.Is(err, io.EOF) {
				t.Errorf("Read err = %v, want io.EOF", err)
			}
		})
	}
}

func TestStreamReadUnexpectedEOF(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		framer Framer
		data   string
	}{
		"header truncated body": {
			framer: NewHeaderStream,
			data:   "Content-Length: 40\r\n\r\n{\"jsonrpc\":\"2.0\"", // body shorter than declared
		},
		"header truncated header": {
			framer: NewHeaderStream,
			data:   "Content-Length: 40\r\n", // no terminating blank line
		},
		"ndjson truncated": {
			framer: NewNDJSONStream,
			data:   `{"jsonrpc":"2.0","method":"x"}`, // no trailing newline
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := tt.framer(rwc{Reader: bytes.NewReader([]byte(tt.data)), Writer: io.Discard})
			_, _, err := s.Read(t.Context())
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("Read err = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	}
}

func TestStreamContextCanceled(t *testing.T) {
	t.Parallel()

	tests := map[string]Framer{
		"header": NewHeaderStream,
		"ndjson": NewNDJSONStream,
	}
	for name, framer := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			s := framer(rwc{Reader: bytes.NewReader(nil), Writer: io.Discard})
			if _, _, err := s.Read(ctx); !errors.Is(err, context.Canceled) {
				t.Errorf("Read err = %v, want context.Canceled", err)
			}
			if _, err := s.Write(ctx, NewNotification("x", nil)); !errors.Is(err, context.Canceled) {
				t.Errorf("Write err = %v, want context.Canceled", err)
			}
		})
	}
}

func TestFrameWriteCallHonorsContextCanceledWhileQueued(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*countingConn) (frameWriter, func()){
		"header": func(cc *countingConn) (frameWriter, func()) {
			s := NewHeaderStream(cc).(*headerStream)
			s.writeMu.Lock()
			return s, s.writeMu.Unlock
		},
		"ndjson": func(cc *countingConn) (frameWriter, func()) {
			s := NewNDJSONStream(cc).(*ndjsonStream)
			s.writeMu.Lock()
			return s, s.writeMu.Unlock
		},
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var cc countingConn
			fw, unlock := setup(&cc)
			ctx, cancel := context.WithCancel(t.Context())
			errc := make(chan error, 1)
			go func() {
				_, err := fw.writeCall(ctx, NewNumberID(1), "queued", nil)
				errc <- err
			}()

			// Give the writer goroutine time to pass its first context check and
			// block on the stream write mutex. The post-lock context check is the
			// behavior under test: a cancellation while queued for the write lock
			// must not emit a request after the lock becomes available.
			time.Sleep(10 * time.Millisecond)
			cancel()
			unlock()

			select {
			case err := <-errc:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("writeCall err = %v, want context.Canceled", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for writeCall")
			}
			if writes := cc.writes.Load(); writes != 0 {
				t.Fatalf("conn.Write calls = %d, want 0 for canceled queued write", writes)
			}
		})
	}
}

func TestStreamConcurrentWrites(t *testing.T) {
	t.Parallel()

	tests := map[string]Framer{
		"header": NewHeaderStream,
		"ndjson": NewNDJSONStream,
	}
	for name, framer := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, srv := net.Pipe()
			defer c.Close()
			defer srv.Close()

			client := framer(c)
			server := framer(srv)
			ctx := t.Context()

			const writers = 8
			const perWriter = 16
			total := writers * perWriter

			// Drain the server so writers never block on a full pipe; collect the
			// decoded ids to confirm every framed message arrives intact.
			ids := make(chan int64, total)
			readErr := make(chan error, 1)
			go func() {
				for range total {
					msg, _, err := server.Read(ctx)
					if err != nil {
						readErr <- err
						return
					}
					call, ok := msg.(*Call)
					if !ok {
						readErr <- fmt.Errorf("got %T, want *Call", msg)
						return
					}
					n, _ := call.ID().Number()
					ids <- n
				}
				readErr <- nil
			}()

			var wg sync.WaitGroup
			for w := range writers {
				wg.Go(func() {
					for i := range perWriter {
						id := int64(w*perWriter + i)
						msg := NewCall(NewNumberID(id), "concurrent", RawMessage(`{"x":1}`))
						if _, err := client.Write(ctx, msg); err != nil {
							t.Errorf("Write: %v", err)
							return
						}
					}
				})
			}
			wg.Wait()

			if err := <-readErr; err != nil {
				t.Fatalf("server read: %v", err)
			}
			seen := make(map[int64]bool, total)
			for range total {
				seen[<-ids] = true
			}
			for want := range int64(total) {
				if !seen[want] {
					t.Errorf("missing message id %d (frame interleaving corrupted output)", want)
				}
			}
		})
	}
}

// bufConn is a single-goroutine, in-memory ReadWriteCloser: writes append to a
// buffer that reads then drain. It lets a benchmark encode then decode a frame
// without the cross-goroutine handoff that net.Pipe requires, which would
// otherwise deadlock a same-goroutine round trip.
type bufConn struct {
	buf bytes.Buffer
}

func (c *bufConn) Read(p []byte) (int, error) { return c.buf.Read(p) }

func (c *bufConn) Write(p []byte) (int, error) { return c.buf.Write(p) }

func (c *bufConn) Close() error { return nil }

func benchmarkFramerRoundTrip(b *testing.B, framer Framer) {
	b.Helper()
	conn := &bufConn{}
	s := framer(conn)
	ctx := context.Background()
	msg := NewCall(NewNumberID(1), "textDocument/hover", RawMessage(`{"line":10,"character":4}`))

	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Write(ctx, msg); err != nil {
			b.Fatalf("Write: %v", err)
		}
		if _, _, err := s.Read(ctx); err != nil {
			b.Fatalf("Read: %v", err)
		}
	}
}

func BenchmarkHeaderStreamRoundTrip(b *testing.B) {
	benchmarkFramerRoundTrip(b, NewHeaderStream)
}

func BenchmarkNDJSONStreamRoundTrip(b *testing.B) {
	benchmarkFramerRoundTrip(b, NewNDJSONStream)
}
