// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

// Package benchmark drives three JSON-RPC 2.0 implementations through an
// identical workload so their round-trip, notify, batch, encode, and decode
// costs can be compared apples-to-apples.
//
// The three implementations under test are:
//
//   - jsonrpc2:   go.lsp.dev/jsonrpc2 (this repository).
//   - jrpc2:      github.com/creachadair/jrpc2.
//   - mcp:        the vendored mcpref package (the Go MCP SDK's jsonrpc2), a
//     gopls-derived implementation kept under mcpref/.
//
// Per the Phase 6 §6 integrity protocol the harness exposes two transport
// families:
//
//   - "native": the fastest in-memory transport each library natively offers.
//     jsonrpc2 and mcp run over an in-memory net.Pipe with newline-delimited JSON
//     framing; jrpc2 runs over channel.Direct (server.NewLocal), which passes
//     message buffers in memory with no framing or encoding and is therefore
//     strictly faster than a piped, framed transport. This deliberately does
//     not advantage jsonrpc2.
//
//   - "common": all three libraries run over the SAME net.Pipe pair with
//     newline-delimited JSON framing (one goroutine per endpoint). jsonrpc2 uses
//     NewNDJSONStream, jrpc2 uses channel.RawJSON, and mcp uses the ndjson
//     Reader/Writer implemented in this file. No library gets an in-memory
//     channel here; if anything the shared net.Pipe path biases slightly
//     against jsonrpc2, which is the intent.
//
// Every adapter answers a single no-op "void" method (matching jrpc2's
// BenchmarkRoundTrip) and each adapter's constructor runs a sanity round-trip
// to verify the handler is actually reached and the correct response decoded,
// so a rigged instant no-op cannot silently falsify timings.
package benchmark

import (
	"bufio"
	"context"
	stdjson "encoding/json"
	"fmt"
	"io"
	"net"

	jrpc2 "github.com/creachadair/jrpc2"
	jchannel "github.com/creachadair/jrpc2/channel"
	jhandler "github.com/creachadair/jrpc2/handler"
	jserver "github.com/creachadair/jrpc2/server"
	"go.lsp.dev/jsonrpc2"
	mcp "go.lsp.dev/jsonrpc2/internal/benchmark/mcpref"
)

// voidMethod is the no-op method every adapter answers. It returns a nil
// (JSON null) result with no error, matching the jrpc2 reference benchmark.
const voidMethod = "void"

// rpcClient is the common surface the benchmarks drive. Each library's adapter
// implements it so a single benchmark body can exercise all three.
type rpcClient interface {
	// Call invokes voidMethod (or another method) with the given pre-encoded
	// params and decodes the response result into a freshly allocated
	// json.RawMessage, returning the raw result bytes. params may be nil.
	Call(ctx context.Context, method string, params []byte) ([]byte, error)

	// Notify sends a notification with the given pre-encoded params.
	Notify(ctx context.Context, method string, params []byte) error

	// Batch sends n calls to voidMethod as a single logical batch and waits for
	// all responses. Implementations that lack a public batch-send API write the
	// batch frame directly through the transport; see the mcp and jsonrpc2 adapters.
	Batch(ctx context.Context, n int) error

	// Close tears down the connection and its goroutines.
	Close() error
}

// framedStream is the raw framed surface used by the jsonrpc2 adapter's batch path.
// It matches the built-in framers as well as the benchmark-local direct transport.
type framedStream interface {
	jsonrpc2.Stream
	ReadFrame(context.Context) ([]byte, int64, error)
	WriteFrame(context.Context, []byte) (int64, error)
}

// ---------------------------------------------------------------------------
// jsonrpc2 adapter
// ---------------------------------------------------------------------------

// oursAdapter drives go.lsp.dev/jsonrpc2 over a configurable framed transport.
//
// The adapter reuses a second framed transport for Batch so the benchmark can
// measure the batch path without paying connection setup on every iteration.
// The batch side always speaks raw frames (WriteFrame/ReadFrame) so the same
// implementation works for NDJSON, header, and the benchmark-local direct
// transport.
type oursAdapter struct {
	client      jsonrpc2.Conn
	server      jsonrpc2.Conn
	batchServer jsonrpc2.Conn
	batchClient framedStream
}

// newOursAdapter builds a jsonrpc2 client/server pair over a fresh net.Pipe with
// NDJSON framing and verifies a void round-trip. It also wires the dedicated
// batch transport used by Batch.
func newOursAdapter(ctx context.Context) (*oursAdapter, error) {
	return newOursAdapterWithPair(ctx, func() (jsonrpc2.Stream, jsonrpc2.Stream) {
		ca, cb := net.Pipe()
		return jsonrpc2.NewNDJSONStream(ca), jsonrpc2.NewNDJSONStream(cb)
	})
}

// newOursHeaderAdapter builds a jsonrpc2 adapter that uses the LSP header framing
// on both the main and batch transports.
func newOursHeaderAdapter(ctx context.Context) (*oursAdapter, error) {
	return newOursAdapterWithPair(ctx, func() (jsonrpc2.Stream, jsonrpc2.Stream) {
		ca, cb := net.Pipe()
		return jsonrpc2.NewHeaderStream(ca), jsonrpc2.NewHeaderStream(cb)
	})
}

// newOursDirectAdapter builds a jsonrpc2 adapter over the benchmark-local direct
// transport. The transport bypasses net.Pipe while still exercising the same
// jsonrpc2 Conn and batch code paths.
func newOursDirectAdapter(ctx context.Context) (*oursAdapter, error) {
	return newOursAdapterWithPair(ctx, newDirectStreamPair)
}

func newOursAdapterWithPair(ctx context.Context, pair func() (jsonrpc2.Stream, jsonrpc2.Stream)) (*oursAdapter, error) {
	ca, cb := pair()
	client := jsonrpc2.NewConn(ca)
	server := jsonrpc2.NewConn(cb)

	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	server.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		return reply(ctx, nil, nil)
	})

	bca, bcb := pair()
	batchServer := jsonrpc2.NewConn(bcb)
	batchServer.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		return reply(ctx, nil, nil)
	})

	batchClient, ok := bca.(framedStream)
	if !ok {
		_ = client.Close()
		_ = server.Close()
		_ = batchServer.Close()
		return nil, fmt.Errorf("batch transport %T does not support framed batch writes", bca)
	}

	a := &oursAdapter{
		client:      client,
		server:      server,
		batchServer: batchServer,
		batchClient: batchClient,
	}
	if err := sanityCheck(ctx, a); err != nil {
		_ = a.Close()
		return nil, fmt.Errorf("jsonrpc2 adapter: %w", err)
	}
	return a, nil
}

func (a *oursAdapter) Call(ctx context.Context, method string, params []byte) ([]byte, error) {
	var result jsonrpc2.RawMessage
	if _, err := a.client.Call(ctx, method, rawParams(params), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *oursAdapter) Notify(ctx context.Context, method string, params []byte) error {
	return a.client.Notify(ctx, method, rawParams(params))
}

// Batch sends n void calls as a single JSON-RPC batch array directly through
// the dedicated batch transport and reads the single response-array frame.
func (a *oursAdapter) Batch(ctx context.Context, n int) error {
	frame := make([]byte, 0, 48*n+2)
	frame = append(frame, '[')
	for i := range n {
		if i > 0 {
			frame = append(frame, ',')
		}
		frame = append(frame, `{"jsonrpc":"2.0","method":"void","id":`...)
		frame = appendInt(frame, int64(i+1))
		frame = append(frame, '}')
	}
	frame = append(frame, ']')

	if _, err := a.batchClient.WriteFrame(ctx, frame); err != nil {
		return err
	}

	// Read the single response-array frame (one NDJSON line) and verify it holds
	// n response objects.
	line, _, err := a.batchClient.ReadFrame(ctx)
	if err != nil {
		return err
	}
	if got := countTopLevelObjects(line); got != n {
		return fmt.Errorf("jsonrpc2 batch: got %d responses, want %d", got, n)
	}
	return nil
}

func (a *oursAdapter) Close() error {
	errc := a.client.Close()
	<-a.client.Done()
	errs := a.server.Close()
	<-a.server.Done()
	_ = a.batchClient.Close()
	_ = a.batchServer.Close()
	<-a.batchServer.Done()
	if errc != nil {
		return errc
	}
	return errs
}

// ---------------------------------------------------------------------------
// jrpc2 adapter
// ---------------------------------------------------------------------------

// jrpc2NativeAdapter drives github.com/creachadair/jrpc2 over its native
// in-memory transport (channel.Direct, via server.NewLocal). This is jrpc2's
// fastest transport: it passes message buffers in memory with no framing or
// encoding, so it is strictly faster than the piped, framed transports used by
// the other native adapters. The asymmetry is intentional and recorded.
type jrpc2NativeAdapter struct {
	loc jserver.Local
}

func newJRPC2NativeAdapter(ctx context.Context) (*jrpc2NativeAdapter, error) {
	loc := jserver.NewLocal(voidService(), &jserver.LocalOptions{
		Server: &jrpc2.ServerOptions{DisableBuiltin: true, Concurrency: 1},
	})
	a := &jrpc2NativeAdapter{loc: loc}
	if err := sanityCheck(ctx, a); err != nil {
		_ = a.Close()
		return nil, fmt.Errorf("jrpc2 native adapter: %w", err)
	}
	return a, nil
}

func (a *jrpc2NativeAdapter) Call(ctx context.Context, method string, params []byte) ([]byte, error) {
	rsp, err := a.loc.Client.Call(ctx, method, jrpc2Params(params))
	if err != nil {
		return nil, err
	}
	return []byte(rsp.ResultString()), nil
}

func (a *jrpc2NativeAdapter) Notify(ctx context.Context, method string, params []byte) error {
	return a.loc.Client.Notify(ctx, method, jrpc2Params(params))
}

func (a *jrpc2NativeAdapter) Batch(ctx context.Context, n int) error {
	specs := make([]jrpc2.Spec, n)
	for i := range specs {
		specs[i].Method = voidMethod
	}
	rsps, err := a.loc.Client.Batch(ctx, specs)
	if err != nil {
		return err
	}
	if len(rsps) != n {
		return fmt.Errorf("jrpc2 batch: got %d responses, want %d", len(rsps), n)
	}
	return nil
}

func (a *jrpc2NativeAdapter) Close() error { return a.loc.Close() }

// jrpc2CommonAdapter drives jrpc2 over the SAME net.Pipe transport family used
// by the jsonrpc2 and mcp common adapters: a net.Pipe pair with channel.RawJSON
// (newline/JSON-syntax framing). This is the apples-to-apples transport.
type jrpc2CommonAdapter struct {
	client *jrpc2.Client
	server *jrpc2.Server
	conn   io.Closer
}

func newJRPC2CommonAdapter(ctx context.Context) (*jrpc2CommonAdapter, error) {
	ca, cb := net.Pipe()
	clientCh := jchannel.RawJSON(ca, ca)
	serverCh := jchannel.RawJSON(cb, cb)

	server := jrpc2.NewServer(voidService(), &jrpc2.ServerOptions{
		DisableBuiltin: true, Concurrency: 1,
	}).Start(serverCh)
	client := jrpc2.NewClient(clientCh, nil)

	a := &jrpc2CommonAdapter{client: client, server: server, conn: closerPair{ca, cb}}
	if err := sanityCheck(ctx, a); err != nil {
		_ = a.Close()
		return nil, fmt.Errorf("jrpc2 common adapter: %w", err)
	}
	return a, nil
}

func (a *jrpc2CommonAdapter) Call(ctx context.Context, method string, params []byte) ([]byte, error) {
	rsp, err := a.client.Call(ctx, method, jrpc2Params(params))
	if err != nil {
		return nil, err
	}
	return []byte(rsp.ResultString()), nil
}

func (a *jrpc2CommonAdapter) Notify(ctx context.Context, method string, params []byte) error {
	return a.client.Notify(ctx, method, jrpc2Params(params))
}

func (a *jrpc2CommonAdapter) Batch(ctx context.Context, n int) error {
	specs := make([]jrpc2.Spec, n)
	for i := range specs {
		specs[i].Method = voidMethod
	}
	rsps, err := a.client.Batch(ctx, specs)
	if err != nil {
		return err
	}
	if len(rsps) != n {
		return fmt.Errorf("jrpc2 batch: got %d responses, want %d", len(rsps), n)
	}
	return nil
}

func (a *jrpc2CommonAdapter) Close() error {
	a.client.Close()
	err := a.server.Wait()
	_ = a.conn.Close()
	return err
}

// voidService is the jrpc2 handler map answering voidMethod with a nil result.
func voidService() jhandler.Map {
	return jhandler.Map{
		voidMethod: func(context.Context, *jrpc2.Request) (any, error) {
			return nil, nil
		},
	}
}

// ---------------------------------------------------------------------------
// mcp adapter
// ---------------------------------------------------------------------------

// mcpAdapter drives the vendored mcpref package over a net.Pipe with the ndjson
// Reader/Writer implemented below (mcp has no in-memory channel transport, so
// net.Pipe is its fastest in-memory option and serves both native and common).
type mcpAdapter struct {
	client *mcp.Connection
	server *mcp.Connection
	conn   io.Closer
}

func newMCPAdapter(ctx context.Context) (*mcpAdapter, error) {
	ca, cb := net.Pipe()

	server := mcp.NewConnection(ctx, mcp.ConnectionConfig{
		Reader: newMCPReader(ca),
		Writer: newMCPWriter(ca),
		Closer: ca,
		Bind: func(*mcp.Connection) mcp.Handler {
			return mcp.HandlerFunc(func(ctx context.Context, req *mcp.Request) (any, error) {
				if req.IsCall() {
					// A non-nil, JSON-marshalable result is required for a call.
					return struct{}{}, nil
				}
				return nil, nil
			})
		},
	})

	client := mcp.NewConnection(ctx, mcp.ConnectionConfig{
		Reader: newMCPReader(cb),
		Writer: newMCPWriter(cb),
		Closer: cb,
		Bind: func(*mcp.Connection) mcp.Handler {
			return mcp.HandlerFunc(func(context.Context, *mcp.Request) (any, error) {
				return nil, mcp.ErrNotHandled
			})
		},
	})

	a := &mcpAdapter{client: client, server: server, conn: closerPair{ca, cb}}
	if err := sanityCheck(ctx, a); err != nil {
		_ = a.Close()
		return nil, fmt.Errorf("mcp adapter: %w", err)
	}
	return a, nil
}

func (a *mcpAdapter) Call(ctx context.Context, method string, params []byte) ([]byte, error) {
	var result stdjson.RawMessage
	ac := a.client.Call(ctx, method, rawParams(params))
	if err := ac.Await(ctx, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *mcpAdapter) Notify(ctx context.Context, method string, params []byte) error {
	return a.client.Notify(ctx, method, rawParams(params))
}

// Batch sends n void calls. mcp exposes no public batch-send, so the calls are
// issued concurrently and awaited; the underlying transport still carries them
// as independent frames. This mirrors how an mcp caller would issue a burst.
func (a *mcpAdapter) Batch(ctx context.Context, n int) error {
	acs := make([]*mcp.AsyncCall, n)
	for i := range acs {
		acs[i] = a.client.Call(ctx, voidMethod, nil)
	}
	for _, ac := range acs {
		if err := ac.Await(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func (a *mcpAdapter) Close() error {
	errc := a.client.Close()
	errs := a.server.Close()
	_ = a.conn.Close()
	if errc != nil {
		return errc
	}
	return errs
}

// mcpReader adapts an io.Reader into an mcp.Reader using newline-delimited JSON
// framing decoded via mcp.DecodeMessage.
type mcpReader struct {
	in *bufio.Reader
}

func newMCPReader(r io.Reader) *mcpReader { return &mcpReader{in: bufio.NewReader(r)} }

func (r *mcpReader) Read(ctx context.Context) (mcp.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	line, err := r.in.ReadBytes('\n')
	if err != nil {
		if err == io.EOF && len(line) == 0 {
			return nil, io.EOF
		}
		if err == io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return mcp.DecodeMessage(line[:len(line)-1])
}

// mcpWriter adapts an io.Writer into an mcp.Writer using newline-delimited JSON
// framing encoded via mcp.EncodeMessage. Writes are not internally locked; the
// mcp.Connection serializes its own writes, and net.Pipe writes are atomic per
// call for the small frames used here.
type mcpWriter struct {
	out io.Writer
}

func newMCPWriter(w io.Writer) *mcpWriter { return &mcpWriter{out: w} }

func (w *mcpWriter) Write(ctx context.Context, msg mcp.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := mcp.EncodeMessage(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.out.Write(data)
	return err
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// sanityCheck performs one void round-trip with nil params and verifies the
// call returns without error, proving the adapter actually reaches its handler
// and decodes the response rather than short-circuiting.
func sanityCheck(ctx context.Context, c rpcClient) error {
	if _, err := c.Call(ctx, voidMethod, nil); err != nil {
		return fmt.Errorf("sanity void call: %w", err)
	}
	if err := c.Notify(ctx, voidMethod, nil); err != nil {
		return fmt.Errorf("sanity void notify: %w", err)
	}
	return nil
}

// rawParams wraps pre-encoded params for jsonrpc2/mcp APIs. A nil slice means "no
// params"; both libraries treat a nil/raw value as already-encoded JSON.
func rawParams(params []byte) any {
	if params == nil {
		return nil
	}
	return jsonrpc2.RawMessage(params)
}

// jrpc2Params wraps pre-encoded params for jrpc2. jrpc2 marshals params with
// encoding/json, which passes an encoding/json.RawMessage through verbatim; nil
// means no params.
func jrpc2Params(params []byte) any {
	if params == nil {
		return nil
	}
	return stdjson.RawMessage(params)
}

// closerPair closes both ends of a net.Pipe.
type closerPair struct {
	a, b io.Closer
}

func (p closerPair) Close() error {
	_ = p.a.Close()
	return p.b.Close()
}

// appendInt appends the base-10 representation of v (v >= 0) to dst.
func appendInt(dst []byte, v int64) []byte {
	if v == 0 {
		return append(dst, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for v > 0 {
		i--
		tmp[i] = byte('0' + v%10)
		v /= 10
	}
	return append(dst, tmp[i:]...)
}

// countTopLevelObjects counts the number of top-level '{' ... '}' objects in a
// JSON array frame, used to verify a response-array length when ParseRequests
// cannot classify a response array.
func countTopLevelObjects(frame []byte) int {
	depth := 0
	inStr := false
	esc := false
	count := 0
	for _, c := range frame {
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				count++
			}
			depth++
		case '}':
			depth--
		}
	}
	return count
}
