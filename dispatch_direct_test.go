// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// newDirectPair builds a connected client/server pair over net.Pipe with
// NDJSON framing, the server running h in direct-return mode.
func newDirectPair(t *testing.T, h Handler) (client, server Conn) {
	t.Helper()
	ctx := t.Context()
	ca, cb := net.Pipe()
	client = NewConn(NewNDJSONStream(ca))
	server = NewConn(NewNDJSONStream(cb))
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, h)
	t.Cleanup(func() {
		_ = client.Close()
		<-client.Done()
		_ = server.Close()
		<-server.Done()
	})
	return client, server
}

// TestDirectRoundTrip exercises the direct-return dispatch surface end to
// end: calls with and without params, the error return becoming a wire error,
// and notifications reaching the handler.
func TestDirectRoundTrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		method     string
		params     any
		wantResult string
		wantErr    bool
	}{
		"success: void call round trip": {
			method:     "void",
			wantResult: `"void:"`,
		},
		"success: params echo through the borrowed span": {
			method:     "echo",
			params:     RawMessage(`{"k":"v"}`),
			wantResult: `"echo:{\"k\":\"v\"}"`,
		},
		"error: handler error becomes the wire error response": {
			method:  "fail",
			wantErr: true,
		},
	}

	handler := func(ctx context.Context, req *Request) (any, error) {
		if req.Method() == "fail" {
			return nil, Errorf(InvalidParams, "rejected")
		}
		return req.Method() + ":" + string(req.Params()), nil
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, _ := newDirectPair(t, handler)

			var got RawMessage
			_, err := client.Call(t.Context(), tt.method, tt.params, &got)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Call(%q) succeeded, want wire error", tt.method)
				}
				var werr *Error
				if !asError(err, &werr) || werr.Code != InvalidParams {
					t.Fatalf("Call(%q) error = %v, want *Error with InvalidParams", tt.method, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Call(%q): %v", tt.method, err)
			}
			if diff := gocmp.Diff(tt.wantResult, string(got)); diff != "" {
				t.Errorf("result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDirectNotification proves notifications reach the direct handler and
// produce no response.
func TestDirectNotification(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		method string
	}{
		"success: notification dispatched without reply": {method: "note"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			seen := make(chan string, 1)
			client, _ := newDirectPair(t, func(ctx context.Context, req *Request) (any, error) {
				if !req.IsCall() {
					seen <- req.Method()
				}
				return nil, nil
			})
			if err := client.Notify(t.Context(), tt.method, nil); err != nil {
				t.Fatalf("Notify: %v", err)
			}
			if got := <-seen; got != tt.method {
				t.Errorf("handler saw method %q, want %q", got, tt.method)
			}
		})
	}
}

// TestDirectHandlerPanicAnswersCall proves the direct path's panic fallback:
// the caller receives an InternalError response rather than hanging, and the
// connection then fails (surfacing the panic) per the [Handler] contract.
func TestDirectHandlerPanicAnswersCall(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		method string
	}{
		"error: panicking handler answers the call with InternalError": {method: "boom"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, server := newDirectPair(t, func(ctx context.Context, req *Request) (any, error) {
				panic("kaboom")
			})
			_, err := client.Call(t.Context(), tt.method, nil, nil)
			if err == nil {
				t.Fatalf("Call(%q) succeeded, want InternalError from panicking handler", tt.method)
			}
			var werr *Error
			if !asError(err, &werr) || werr.Code != InternalError {
				t.Fatalf("Call(%q) error = %v, want *Error with InternalError", tt.method, err)
			}
			<-server.Done()
			if server.Err() == nil {
				t.Errorf("server.Err() = nil, want the surfaced handler panic")
			}
		})
	}
}

// TestDirectAsyncClonesRequest proves the single-escape-point contract: a
// handler that releases itself with [Async] keeps a valid request afterward
// because the hard release clones the borrowed spans in place, even while the
// successor reader is already overwriting the transport frame.
func TestDirectAsyncClonesRequest(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		first  string
		params string
		others int
	}{
		"success: async handler reads cloned method and params after later requests": {
			first:  "async-method",
			params: `{"keep":"me"}`,
			others: 8,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gotMethod := make(chan [2]string, 1)
			release := make(chan struct{})
			var once sync.Once

			client, _ := newDirectPair(t, func(ctx context.Context, req *Request) (any, error) {
				if req.Method() == "later" {
					return nil, nil
				}
				once.Do(func() {
					Async(ctx)
					// The successor reader is live now; wait until the test has
					// driven several later requests through the same frame buffer.
					<-release
					gotMethod <- [2]string{req.Method(), string(req.Params())}
				})
				return nil, nil
			})

			ctx := t.Context()
			done := make(chan error, 1)
			go func() {
				_, err := client.Call(ctx, tt.first, RawMessage(tt.params), nil)
				done <- err
			}()

			for range tt.others {
				if _, err := client.Call(ctx, "later", RawMessage(`{"overwrite":"frame"}`), nil); err != nil {
					t.Fatalf("later Call: %v", err)
				}
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatalf("async Call: %v", err)
			}

			got := <-gotMethod
			if got[0] != tt.first || got[1] != tt.params {
				t.Errorf("async handler observed (method, params) = (%q, %q), want (%q, %q); the hard release must clone the borrowed spans", got[0], got[1], tt.first, tt.params)
			}
		})
	}
}

// TestDirectBatchAsyncMemberClonesRequest pins the batch arm of the
// single-escape-point contract: a batch member that releases itself with
// [Async] keeps valid method and params after later frames have reused the
// transport buffer, because the hard release cloned the borrowed spans.
func TestDirectBatchAsyncMemberClonesRequest(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		frame  string
		others int
	}{
		"success: async batch member survives later frames": {
			frame:  `[{"jsonrpc":"2.0","id":1,"method":"pin","params":{"keep":"me"}},{"jsonrpc":"2.0","id":2,"method":"quick"}]`,
			others: 8,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			a, b := NewChannelStreamPair(4)
			observed := make(chan [2]string, 1)
			release := make(chan struct{})

			server := NewConn(b)
			server.Go(ctx, func(ctx context.Context, req *Request) (any, error) {
				if req.Method() != "pin" {
					return req.Method(), nil
				}
				Async(ctx)
				<-release
				observed <- [2]string{req.Method(), string(req.Params())}
				return "pinned", nil
			})
			t.Cleanup(func() {
				_ = server.Close()
				<-server.Done()
			})

			fw := a.(frameStream)
			if _, err := fw.WriteFrame(ctx, []byte(tt.frame)); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			// Drive later single requests through the same frame pool while the
			// async member is parked.
			for i := range tt.others {
				wire := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"later","params":{"overwrite":"frame %d"}}`, 100+i, i)
				if _, err := fw.WriteFrame(ctx, []byte(wire)); err != nil {
					t.Fatalf("later WriteFrame: %v", err)
				}
				if _, _, err := fw.ReadFrame(ctx); err != nil {
					t.Fatalf("later ReadFrame: %v", err)
				}
			}
			close(release)

			got := <-observed
			if got[0] != "pin" || got[1] != `{"keep":"me"}` {
				t.Errorf("async batch member observed (method, params) = (%q, %q), want (pin, {\"keep\":\"me\"}); the hard release must clone the borrowed spans", got[0], got[1])
			}
			// Drain the batch response array.
			if _, _, err := fw.ReadFrame(ctx); err != nil {
				t.Fatalf("batch response ReadFrame: %v", err)
			}
		})
	}
}

// TestPooledRequestFieldReset is the white-box mirror of putWaiter's
// discipline: every field of a recycled incomingRequest must be zero after
// the pool-put reset, so a recycled request cannot leak its previous life.
func TestPooledRequestFieldReset(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{}{
		"success: every field zeroed on put": {},
	}
	for name := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ir := getIncomingRequest()
			ir.parent = context.Background()
			ir.id = NewNumberID(7)
			ir.isCall = true
			ir.canceled = true
			ir.request = Request{id: NewNumberID(7), method: "m", params: RawMessage(`1`), isCall: true}
			ir.rel = releaser{active: true}
			ir.replied.done.Store(true)

			// resetIncomingRequest is the same reset the pool put applies; calling
			// it directly keeps ir out of the global pool, which a concurrently
			// running test's dispatch could otherwise recycle mid-assertion.
			resetIncomingRequest(ir)

			if ir.parent != nil || ir.realCtx != nil || ir.realCancel != nil {
				t.Errorf("context fields not reset: parent=%v parent2=%v realCtx=%v", ir.parent, ir.parent, ir.realCtx)
			}
			// Under the poison build the body carries sentinels instead of zeros;
			// either way it must not carry the previous request's data.
			if !strings.Contains(ir.request.method, "POISON") {
				if ir.request.method != "" || ir.request.params != nil || ir.request.isCall || ir.request.id != (ID{}) {
					t.Errorf("reqV2 not reset: %+v", ir.request)
				}
			}
			if ir.rel.active || ir.rel.released || ir.rel.handedOff || ir.rel.ch != nil || ir.rel.ir != nil || ir.rel.conn != nil || ir.rel.ctx != nil {
				t.Errorf("releaser not reset: active=%v released=%v handedOff=%v", ir.rel.active, ir.rel.released, ir.rel.handedOff)
			}
			if ir.id != (ID{}) || ir.isCall || ir.canceled || ir.replied.done.Load() {
				t.Errorf("scalar fields not reset: id=%v isCall=%v canceled=%v replied=%v", ir.id, ir.isCall, ir.canceled, ir.replied.done.Load())
			}
		})
	}
}

// TestDirectBatch proves batch frames still work in direct mode through the
// compat adapter: every call member is answered in one response array.
func TestDirectBatch(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		frame      string
		wantBodies int
	}{
		"success: two calls and one notification answered as a two-element array": {
			frame:      `[{"jsonrpc":"2.0","id":1,"method":"a"},{"jsonrpc":"2.0","id":2,"method":"b"},{"jsonrpc":"2.0","method":"note"}]`,
			wantBodies: 2,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			a, b := NewChannelStreamPair(4)
			server := NewConn(b)
			server.Go(ctx, func(ctx context.Context, req *Request) (any, error) {
				return req.Method(), nil
			})
			t.Cleanup(func() {
				_ = server.Close()
				<-server.Done()
			})

			fw := a.(frameStream)
			if _, err := fw.WriteFrame(ctx, []byte(tt.frame)); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			resp, _, err := fw.ReadFrame(ctx)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if got := strings.Count(string(resp), `"result"`); got != tt.wantBodies {
				t.Errorf("response array %s carries %d results, want %d", resp, got, tt.wantBodies)
			}
		})
	}
}
