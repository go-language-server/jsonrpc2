// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// batchPeer drives one side of an ndjson connection by hand so a test can write a
// raw batch frame and read the raw response frame, bypassing the high-level Conn
// on the test side while exercising the real Conn dispatch on the server side.
type batchPeer struct {
	conn net.Conn
	in   *bufio.Reader
}

func newBatchServer(t *testing.T, handler Handler) (*batchPeer, Conn) {
	t.Helper()
	ca, cb := net.Pipe()
	server := NewConn(NewNDJSONStream(cb))
	server.Go(t.Context(), handler)
	return &batchPeer{conn: ca, in: bufio.NewReader(ca)}, server
}

func (p *batchPeer) writeFrame(t *testing.T, frame string) {
	t.Helper()
	if _, err := io.WriteString(p.conn, frame+"\n"); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// readFrame reads one ndjson frame, or returns ok=false if none arrives before
// the deadline (used to assert that an all-notification batch yields nothing).
func (p *batchPeer) readFrame(t *testing.T, timeout time.Duration) (string, bool) {
	t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := p.in.ReadString('\n')
		ch <- res{line, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil && r.line == "" {
			t.Fatalf("read frame: %v", r.err)
		}
		return strings.TrimRight(r.line, "\n"), true
	case <-time.After(timeout):
		return "", false
	}
}

func batchHandler(ctx context.Context, reply Replier, req Request) error {
	switch req.Method() {
	case "sum":
		return reply(ctx, raw(string(orNull(req.Params()))), nil)
	case "note":
		return reply(ctx, nil, nil)
	default:
		return MethodNotFoundHandler(ctx, reply, req)
	}
}

func TestBatchMixedCallsAndNotifications(t *testing.T) {
	t.Parallel()
	peer, server := newBatchServer(t, batchHandler)
	defer func() {
		_ = server.Close()
		<-server.Done()
	}()

	// Two calls (ids 1 and 3) and one notification (no id). Only the calls produce
	// response members; the notification produces none.
	frame := `[` +
		`{"jsonrpc":"2.0","method":"sum","params":10,"id":1},` +
		`{"jsonrpc":"2.0","method":"note","params":"x"},` +
		`{"jsonrpc":"2.0","method":"sum","params":30,"id":3}` +
		`]`
	peer.writeFrame(t, frame)

	resp, ok := peer.readFrame(t, 2*time.Second)
	if !ok {
		t.Fatal("expected a batch response frame")
	}

	// The response is a JSON array; assert it is a batch of exactly the two call
	// responses, with the notification absent.
	got := decodeBatchResponses(t, resp)
	want := map[int64]string{1: "10", 3: "30"}
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Fatalf("batch response mismatch (-want +got):\n%s", diff)
	}
}

func TestBatchAllNotificationsNoResponse(t *testing.T) {
	t.Parallel()
	peer, server := newBatchServer(t, batchHandler)
	defer func() {
		_ = server.Close()
		<-server.Done()
	}()

	frame := `[` +
		`{"jsonrpc":"2.0","method":"note","params":1},` +
		`{"jsonrpc":"2.0","method":"note","params":2}` +
		`]`
	peer.writeFrame(t, frame)

	if _, ok := peer.readFrame(t, 300*time.Millisecond); ok {
		t.Fatal("an all-notification batch must produce no response")
	}
}

func TestBatchMalformedElement(t *testing.T) {
	t.Parallel()
	peer, server := newBatchServer(t, batchHandler)
	defer func() {
		_ = server.Close()
		<-server.Done()
	}()

	// One valid call and one malformed member (missing method). The malformed
	// member is answered with a null-id error response; the valid call is served.
	frame := `[` +
		`{"jsonrpc":"2.0","method":"sum","params":5,"id":1},` +
		`{"jsonrpc":"2.0","id":2}` +
		`]`
	peer.writeFrame(t, frame)

	resp, ok := peer.readFrame(t, 2*time.Second)
	if !ok {
		t.Fatal("expected a batch response frame")
	}

	msgs := splitArray(t, resp)
	if len(msgs) != 2 {
		t.Fatalf("want 2 response members, got %d: %s", len(msgs), resp)
	}
	var sawValid, sawError bool
	for _, m := range msgs {
		dm, derr := DecodeMessage([]byte(m))
		if derr != nil {
			t.Fatalf("decode member %q: %v", m, derr)
		}
		r := dm.(*Response)
		if r.Err() != nil {
			var we *Error
			if !asError(r.Err(), &we) || we.Code != InvalidRequest {
				t.Fatalf("malformed member: got %v want InvalidRequest", r.Err())
			}
			if r.ID().IsValid() {
				t.Fatalf("malformed member must carry a null id, got %v", r.ID())
			}
			sawError = true
		} else {
			if n, _ := r.ID().Number(); n != 1 {
				t.Fatalf("valid member id: got %v want 1", r.ID())
			}
			sawValid = true
		}
	}
	if !sawValid || !sawError {
		t.Fatalf("want one valid and one error member; valid=%v error=%v", sawValid, sawError)
	}
}

func TestBatchEmptyArray(t *testing.T) {
	t.Parallel()
	peer, server := newBatchServer(t, batchHandler)
	defer func() {
		_ = server.Close()
		<-server.Done()
	}()

	peer.writeFrame(t, `[]`)
	resp, ok := peer.readFrame(t, 2*time.Second)
	if !ok {
		t.Fatal("an empty batch must produce a single error response")
	}
	// Per the specification, an empty batch is answered with a single,
	// unbracketed Response object, not a one-element array.
	if strings.HasPrefix(strings.TrimSpace(resp), "[") {
		t.Fatalf("empty batch response must be a single object, got array: %s", resp)
	}
	dm, err := DecodeMessage([]byte(resp))
	if err != nil {
		t.Fatalf("decode empty-batch response %q: %v", resp, err)
	}
	r := dm.(*Response)
	var we *Error
	if !asError(r.Err(), &we) || we.Code != InvalidRequest {
		t.Fatalf("empty batch: got %v want InvalidRequest", r.Err())
	}
	if r.ID().IsValid() {
		t.Fatalf("empty batch response must carry a null id, got %v", r.ID())
	}
}

// decodeBatchResponses decodes a batch response array into a map of numeric id
// to raw result, ignoring any error members.
func decodeBatchResponses(t *testing.T, arr string) map[int64]string {
	t.Helper()
	out := make(map[int64]string)
	for _, m := range splitArray(t, arr) {
		dm, err := DecodeMessage([]byte(m))
		if err != nil {
			t.Fatalf("decode response member %q: %v", m, err)
		}
		r := dm.(*Response)
		id, _ := r.ID().Number()
		out[id] = string(r.Result())
	}
	return out
}

// splitArray splits a top-level JSON array into its element spans using the
// package scanner, so the test does not re-implement JSON parsing.
func splitArray(t *testing.T, arr string) []string {
	t.Helper()
	data := []byte(arr)
	i := skipSpace(data, 0)
	if i >= len(data) || data[i] != '[' {
		t.Fatalf("not a JSON array: %q", arr)
	}
	spans, ok := scanArrayElements(data, i)
	if !ok {
		t.Fatalf("malformed JSON array: %q", arr)
	}
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, string(s))
	}
	return out
}

// asError is a small wrapper over errors.As for *Error targets.
func asError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
