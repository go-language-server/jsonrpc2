// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"go.lsp.dev/jsonrpc2"
)

// This file exercises the public surface the way a downstream consumer such as
// go.lsp.dev/protocol would: a method map
// dispatched through a single [jsonrpc2.Handler], served by [jsonrpc2.Serve] +
// [jsonrpc2.HandlerServer], and driven by a client [jsonrpc2.Conn.Call] with
// typed params and results routed through the default codec.

// hoverParams and hoverResult are representative typed payloads, modeling the
// kind of request/response shapes a Language Server Protocol consumer sends.
type hoverParams struct {
	URI  string `json:"uri"`
	Line int    `json:"line"`
}

type hoverResult struct {
	Contents string `json:"contents"`
}

type sumParams struct {
	Values []int `json:"values"`
}

type sumResult struct {
	Total int `json:"total"`
}

// methodMap is a gopls-style dispatch table: method name to a typed handler. It
// is the pattern go.lsp.dev/protocol builds on top of this package.
type methodMap map[string]func(ctx context.Context, params jsonrpc2.RawMessage) (any, error)

// handler adapts the method map to a single [jsonrpc2.Handler]. It decodes the
// raw params with the default codec, invokes the typed handler, and returns the
// typed result (or the JSON-RPC error the handler returns) as the response.
func (m methodMap) handler(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	fn, ok := m[req.Method()]
	if !ok {
		return nil, jsonrpc2.ErrMethodNotFound
	}
	return fn(ctx, req.Params())
}

// TestDownstreamCompatShim proves the gopls-style usage compiles and round-trips
// typed params/results through the default codec over a real network listener.
func TestDownstreamCompatShim(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	mm := methodMap{
		"textDocument/hover": func(_ context.Context, params jsonrpc2.RawMessage) (any, error) {
			var p hoverParams
			if err := jsonrpc2.DefaultCodec.Unmarshal(params, &p); err != nil {
				return nil, jsonrpc2.ErrInvalidParams
			}
			return hoverResult{Contents: p.URI + " line " + strconv.Itoa(p.Line)}, nil
		},
		"math/sum": func(_ context.Context, params jsonrpc2.RawMessage) (any, error) {
			var p sumParams
			if err := jsonrpc2.DefaultCodec.Unmarshal(params, &p); err != nil {
				return nil, jsonrpc2.ErrInvalidParams
			}
			total := 0
			for _, v := range p.Values {
				total += v
			}
			return sumResult{Total: total}, nil
		},
		"fail/invalid": func(context.Context, jsonrpc2.RawMessage) (any, error) {
			return nil, jsonrpc2.ErrInvalidParams
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := jsonrpc2.HandlerServer(mm.handler)
	go func() { _ = jsonrpc2.Serve(ctx, ln, server, 0) }()

	client, nc := dialConn(t, "tcp", ln.Addr().String())
	defer nc.Close()
	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	defer client.Close()

	t.Run("success: typed hover round-trip", func(t *testing.T) {
		var got hoverResult
		if _, err := client.Call(ctx, "textDocument/hover", hoverParams{URI: "file:///a.go", Line: 12}, &got); err != nil {
			t.Fatalf("Call: %v", err)
		}
		want := hoverResult{Contents: "file:///a.go line 12"}
		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("hover result mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("success: typed sum round-trip", func(t *testing.T) {
		var got sumResult
		if _, err := client.Call(ctx, "math/sum", sumParams{Values: []int{1, 2, 3, 4}}, &got); err != nil {
			t.Fatalf("Call: %v", err)
		}
		want := sumResult{Total: 10}
		if diff := gocmp.Diff(want, got); diff != "" {
			t.Errorf("sum result mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("error: handler returns a coded error", func(t *testing.T) {
		var got sumResult
		_, err := client.Call(ctx, "fail/invalid", sumParams{}, &got)
		if !errors.Is(err, jsonrpc2.ErrInvalidParams) {
			t.Errorf("Call error = %v, want ErrInvalidParams", err)
		}
	})

	t.Run("error: unknown method", func(t *testing.T) {
		_, err := client.Call(ctx, "no/such/method", nil, nil)
		if !errors.Is(err, jsonrpc2.ErrMethodNotFound) {
			t.Errorf("Call error = %v, want ErrMethodNotFound", err)
		}
	})
}

// TestDownstreamNotify exercises the fire-and-forget half of the public surface
// the way a downstream consumer drives it: a client [jsonrpc2.Conn.Notify] whose
// effect on the server is observed only through a subsequent round-trip call,
// matching the way a notification (which carries no id and no response) is
// confirmed in real LSP-style usage.
func TestDownstreamNotify(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// notified is closed by the server handler the first time it observes the
	// "didChange" notification, giving the test a deterministic, sleep-free
	// handshake instead of polling shared state.
	notified := make(chan jsonrpc2.RawMessage, 1)
	server := jsonrpc2.HandlerServer(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
		switch req.Method() {
		case "didChange":
			// A notification: there is nothing to return, but record that it
			// arrived. The params bytes are borrowed and die when the handler
			// returns, so copy them before handing them to the channel.
			select {
			case notified <- jsonrpc2.RawMessage(string(req.Params())):
			default:
			}
			return nil, nil
		case "ping":
			return jsonrpc2.RawMessage(`"pong"`), nil
		default:
			return jsonrpc2.MethodNotFoundHandler(ctx, req)
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = jsonrpc2.Serve(ctx, ln, server, 0) }()

	client, nc := dialConn(t, "tcp", ln.Addr().String())
	defer nc.Close()
	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	defer client.Close()

	if err := client.Notify(ctx, "didChange", jsonrpc2.RawMessage(`{"version":7}`)); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	// A round-trip after the notification guarantees the server's read loop has
	// drained the notification (header framing preserves wire order), so the
	// channel receive below cannot race ahead of it.
	var pong jsonrpc2.RawMessage
	if _, err := client.Call(ctx, "ping", nil, &pong); err != nil {
		t.Fatalf("ping Call: %v", err)
	}
	if diff := gocmp.Diff(`"pong"`, string(pong)); diff != "" {
		t.Errorf("ping result mismatch (-want +got):\n%s", diff)
	}

	select {
	case params := <-notified:
		if diff := gocmp.Diff(`{"version":7}`, string(params)); diff != "" {
			t.Errorf("notification params mismatch (-want +got):\n%s", diff)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never observed the notification")
	}
}

// TestDownstreamCancel exercises the server-side cancellation surface the way a
// downstream consumer composes it: the server handler is wrapped with
// [jsonrpc2.CancelHandler] (over [jsonrpc2.AsyncHandler] so the read loop is free
// while a call blocks), a long-running handler blocks on its context, and the
// returned canceller is invoked with the in-flight call's id to unblock it. The
// call then completes with the cancellation error.
//
// The handler reports its own call id through a channel, so the test cancels the
// real id the connection assigned rather than guessing it; every step is
// channel-synchronized, with no sleeps, to stay clean under -race.
func TestDownstreamCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	gotID := make(chan jsonrpc2.ID, 1)
	handlerErr := make(chan error, 1)
	base := func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
		if req.Method() != "longRunning" {
			return nil, jsonrpc2.ErrMethodNotFound
		}
		// Report the id the connection assigned so the test can cancel it.
		gotID <- req.ID()
		<-ctx.Done()
		handlerErr <- ctx.Err()
		// Return the cancellation as the call's error response: the connection
		// writes the response from the handler's return values outside the
		// canceled per-call context, so the outcome is delivered to the caller
		// without the detached-context reply the closure API needed.
		return nil, ctx.Err()
	}
	h, canceller := jsonrpc2.CancelHandler(jsonrpc2.AsyncHandler(base))
	server := jsonrpc2.HandlerServer(h)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = jsonrpc2.Serve(ctx, ln, server, 0) }()

	client, nc := dialConn(t, "tcp", ln.Addr().String())
	defer nc.Close()
	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	defer client.Close()

	callErr := make(chan error, 1)
	go func() {
		_, err := client.Call(ctx, "longRunning", nil, nil)
		callErr <- err
	}()

	// Cancel the exact id the server is handling.
	var id jsonrpc2.ID
	select {
	case id = <-gotID:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started")
	}
	canceller(id)

	select {
	case err := <-handlerErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("handler ctx error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler was not canceled")
	}

	// The blocked call returns the error response built from the handler's
	// return values. The handler returned ctx.Err(), which crosses the wire as a
	// JSON-RPC error object, so the client observes a *jsonrpc2.Error carrying
	// the cancellation message rather than the sentinel context.Canceled itself.
	select {
	case err := <-callErr:
		if err == nil {
			t.Errorf("Call error = nil, want the cancellation error response")
			break
		}
		var werr *jsonrpc2.Error
		if !errors.As(err, &werr) {
			t.Errorf("Call error = %T (%v), want *jsonrpc2.Error", err, err)
			break
		}
		if werr.Message != context.Canceled.Error() {
			t.Errorf("Call error message = %q, want %q", werr.Message, context.Canceled.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call never returned")
	}
}
