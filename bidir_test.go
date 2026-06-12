// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"runtime"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// Bidirectional / server-initiated-call (reentrancy) tests.
//
// A [Conn] is symmetric: either end may issue calls and notifications, and a
// handler may call back into the same connection while serving a request. This
// is the pattern LSP relies on (a server that issues requests to the client
// mid-request). The deadlock-avoidance contract is the subject of these tests:
//
//   - A handler runs inline on the read loop in the synchronous case, so a
//     server-initiated *call* back to the peer (which waits for a response)
//     deadlocks unless the handler first calls [Async] to release the read loop;
//     otherwise the response to the callback can never be read, because the read
//     loop is blocked running the handler. That deadlocking case is intentionally
//     not exercised here (it would hang the suite); it is documented instead.
//   - A server-initiated *notification* back to the peer needs no [Async], because
//     Notify only writes and never waits for a response.

// TestBidirectionalServerInitiatedCall exercises the canonical LSP pattern: A
// calls "ask" on B; B's handler releases the read loop with [Async] and then
// issues a server-initiated call "answer" back to A, awaits A's result, and
// replies to "ask" with a value derived from it. It proves a handler can call
// back into the same connection mid-request once it releases with [Async].
func TestBidirectionalServerInitiatedCall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := memTransport(NewNDJSONStream)(t)

	// A serves "answer" with a known value: this is the server-initiated call B
	// makes back into the connection while serving "ask".
	a := NewConn(ea.stream)
	a.Go(ctx, func(ctx context.Context, req *Request) (any, error) {
		if req.Method() == "answer" {
			return raw(`"forty-two"`), nil
		}
		return MethodNotFoundHandler(ctx, req)
	})

	// B serves "ask" by releasing the read loop with Async, calling back to A's
	// "answer", and replying with a value derived from A's result. The handler
	// closure observes b only after Go starts the read loop.
	b := NewConn(eb.stream)
	b.Go(ctx, func(ctx context.Context, req *Request) (any, error) {
		if req.Method() != "ask" {
			return MethodNotFoundHandler(ctx, req)
		}
		// Release the read loop so the response to the callback below can be read;
		// without this the reentrant call deadlocks.
		Async(ctx)
		var inner RawMessage
		if _, err := b.Call(ctx, "answer", nil, &inner); err != nil {
			return nil, err
		}
		return raw(`{"relayed":` + string(inner) + `}`), nil
	})
	defer closeBoth(t, a, b)

	// Drive the call on its own goroutine with a deadline so a regression in the
	// Async handoff fails fast instead of hanging the suite.
	type result struct {
		got RawMessage
		err error
	}
	resc := make(chan result, 1)
	go func() {
		var got RawMessage
		_, err := a.Call(ctx, "ask", nil, &got)
		resc <- result{got: got, err: err}
	}()

	select {
	case r := <-resc:
		if r.err != nil {
			t.Fatalf("Call ask: %v", r.err)
		}
		if diff := gocmp.Diff(`{"relayed":"forty-two"}`, string(r.got)); diff != "" {
			t.Fatalf("round-tripped result mismatch (-want +got):\n%s", diff)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bidirectional call did not complete: handler likely deadlocked")
	}
}

// TestServerInitiatedNotificationFromHandlerWithoutAsync proves the call/notify
// distinction: a handler may issue a server-initiated [Conn.Notify] back to the
// peer without calling [Async], because Notify only writes and never waits for a
// response. The original call still returns, and the peer observes the
// notification.
func TestServerInitiatedNotificationFromHandlerWithoutAsync(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := memTransport(NewNDJSONStream)(t)

	// A records the server-initiated notification on a buffered channel (capacity
	// one) so A's handler never blocks on the send.
	observed := make(chan string, 1)
	a := NewConn(ea.stream)
	a.Go(ctx, func(ctx context.Context, req *Request) (any, error) {
		if req.Method() == "event" {
			observed <- string(orNull(req.Params()))
			return nil, nil // a notification has no response.
		}
		return MethodNotFoundHandler(ctx, req)
	})

	// B serves "ask" by notifying A back (no Async needed) and then replying.
	b := NewConn(eb.stream)
	b.Go(ctx, func(ctx context.Context, req *Request) (any, error) {
		if req.Method() != "ask" {
			return MethodNotFoundHandler(ctx, req)
		}
		if err := b.Notify(ctx, "event", raw(`{"n":1}`)); err != nil {
			return nil, err
		}
		return raw(`"ok"`), nil
	})
	defer closeBoth(t, a, b)

	type result struct {
		got RawMessage
		err error
	}
	resc := make(chan result, 1)
	go func() {
		var got RawMessage
		_, err := a.Call(ctx, "ask", nil, &got)
		resc <- result{got: got, err: err}
	}()

	select {
	case r := <-resc:
		if r.err != nil {
			t.Fatalf("Call ask: %v", r.err)
		}
		if diff := gocmp.Diff(`"ok"`, string(r.got)); diff != "" {
			t.Fatalf("call result mismatch (-want +got):\n%s", diff)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call with server-initiated notification did not return")
	}

	select {
	case p := <-observed:
		if diff := gocmp.Diff(`{"n":1}`, p); diff != "" {
			t.Fatalf("observed notification mismatch (-want +got):\n%s", diff)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("peer did not observe the server-initiated notification")
	}
}

// TestGoroutineLeakBidirectional drives a bidirectional call cycle (which forces
// the [Async] handoff to spawn a successor reader) and asserts that, after both
// connections close, the goroutine count settles back to baseline. It confirms
// the handed-off successor reader unwinds on Close and leaks no goroutine.
func TestGoroutineLeakBidirectional(t *testing.T) {
	// Not parallel: NumGoroutine is process-global.
	ctx := t.Context()
	base := runtime.NumGoroutine()

	for range 20 {
		ea, eb := memTransport(NewNDJSONStream)(t)

		a := NewConn(ea.stream)
		a.Go(ctx, func(ctx context.Context, req *Request) (any, error) {
			if req.Method() == "answer" {
				return raw(`"v"`), nil
			}
			return MethodNotFoundHandler(ctx, req)
		})

		b := NewConn(eb.stream)
		b.Go(ctx, func(ctx context.Context, req *Request) (any, error) {
			if req.Method() != "ask" {
				return MethodNotFoundHandler(ctx, req)
			}
			Async(ctx)
			var inner RawMessage
			if _, err := b.Call(ctx, "answer", nil, &inner); err != nil {
				return nil, err
			}
			return raw(string(inner)), nil
		})

		for range 3 {
			var got RawMessage
			if _, err := a.Call(ctx, "ask", nil, &got); err != nil {
				t.Fatalf("Call ask: %v", err)
			}
		}

		_ = a.Close()
		<-a.Done()
		_ = b.Close()
		<-b.Done()
	}

	// Poll for the count to settle back to baseline; scheduled-out goroutines
	// (notably the handed-off successor readers) may take a moment to exit.
	deadline := time.Now().Add(2 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		runtime.GC()
		runtime.Gosched()
		got = runtime.NumGoroutine()
		if got <= base+1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline %d, ended at %d", base, got)
}
