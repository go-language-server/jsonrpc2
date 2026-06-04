// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// The tests in this file pin the Wave-3 non-blocking robustness contracts:
//
//   - a handler panic cannot leak the in-flight counter / incomingByID entry and
//     deadlock a later Close, and the panicking call receives an internal-error
//     response rather than hanging the caller;
//   - a handler that returns for a call without replying yields a deterministic
//     internal-error response;
//   - the same guarantee holds for a batch member, so a missing reply cannot hang
//     the array flush;
//   - canceling the context passed to Go is treated as a clean shutdown by Err.

// pipeConns builds a connected client/server Conn pair over an in-memory
// net.Pipe with ndjson framing. The caller starts each with Go.
func pipeConns(t *testing.T) (client, server *conn) {
	t.Helper()
	a, b := memTransport(NewNDJSONStream)(t)
	return NewConn(a.stream).(*conn), NewConn(b.stream).(*conn)
}

// TestHandlerPanicDoesNotLeak verifies that a panicking handler answers its call
// with an internal error and that a subsequent Close drains and returns promptly
// rather than deadlocking on a leaked in-flight counter.
func TestHandlerPanicDoesNotLeak(t *testing.T) {
	ctx := t.Context()
	client, server := pipeConns(t)

	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, func(context.Context, Replier, Request) error {
		panic("boom")
	})

	_, err := client.Call(ctx, "explode", nil, nil)
	var we *Error
	if !errors.As(err, &we) || we.Code != InternalError {
		t.Fatalf("Call error = %v, want an *Error with InternalError code", err)
	}

	// Close must not deadlock: the panicking handler's cleanup must have
	// decremented the in-flight counter and dropped the incomingByID entry.
	closed := make(chan struct{})
	go func() {
		_ = server.Close()
		<-server.Done()
		_ = client.Close()
		<-client.Done()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close deadlocked after a handler panic (in-flight state leaked)")
	}
}

// TestHandlerReturnsWithoutReply verifies the deterministic outcome when a
// handler returns for a call without ever calling reply: the caller receives an
// internal error rather than blocking forever.
func TestHandlerReturnsWithoutReply(t *testing.T) {
	ctx := t.Context()
	client, server := pipeConns(t)

	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, func(context.Context, Replier, Request) error {
		// Intentionally never reply.
		return nil
	})
	defer func() {
		_ = server.Close()
		<-server.Done()
		_ = client.Close()
		<-client.Done()
	}()

	done := make(chan error, 1)
	go func() {
		_, err := client.Call(ctx, "silent", nil, nil)
		done <- err
	}()

	select {
	case err := <-done:
		var we *Error
		if !errors.As(err, &we) || we.Code != InternalError {
			t.Fatalf("Call error = %v, want an *Error with InternalError code", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call hung: a handler that returned without replying never produced a response")
	}
}

// TestBatchMemberWithoutReplyDoesNotHang verifies that a batch call member whose
// handler returns without replying still contributes a deterministic error
// member to the response array, so the array flush is not blocked.
func TestBatchMemberWithoutReplyDoesNotHang(t *testing.T) {
	t.Parallel()

	handler := func(ctx context.Context, reply Replier, req Request) error {
		switch req.Method() {
		case "ok":
			return reply(ctx, raw(string(orNull(req.Params()))), nil)
		case "silent":
			// Return without replying.
			return nil
		default:
			return MethodNotFoundHandler(ctx, reply, req)
		}
	}
	peer, server := newBatchServer(t, NewNDJSONStream, handler)
	defer func() {
		_ = server.Close()
		<-server.Done()
	}()

	frame := `[` +
		`{"jsonrpc":"2.0","method":"ok","params":7,"id":1},` +
		`{"jsonrpc":"2.0","method":"silent","id":2}` +
		`]`
	peer.writeFrame(t, frame)

	resp, ok := peer.readFrame(t, 2*time.Second)
	if !ok {
		t.Fatal("batch with a non-replying member never flushed its response array")
	}

	members := splitArray(t, resp)
	results := map[int64]string{}
	codes := map[int64]Code{}
	for _, m := range members {
		dm, derr := DecodeMessage([]byte(m))
		if derr != nil {
			t.Fatalf("decode member %q: %v", m, derr)
		}
		r := dm.(*Response)
		id, _ := r.ID().Number()
		if r.Err() != nil {
			var we *Error
			if asError(r.Err(), &we) {
				codes[id] = we.Code
			}
			continue
		}
		results[id] = string(r.Result())
	}

	if diff := gocmp.Diff(map[int64]string{1: "7"}, results); diff != "" {
		t.Errorf("batch success members mismatch (-want +got):\n%s", diff)
	}
	if diff := gocmp.Diff(map[int64]Code{2: InternalError}, codes); diff != "" {
		t.Errorf("batch error members mismatch (-want +got):\n%s", diff)
	}
}

// TestGoContextCancelIsCleanClose verifies the Err cancellation contract: when
// the context passed to Go is canceled (the documented graceful-stop signal),
// the connection terminates and Err reports nil, not context.Canceled.
func TestGoContextCancelIsCleanClose(t *testing.T) {
	parent := t.Context()
	goCtx, cancel := context.WithCancel(parent)

	client, server := pipeConns(t)
	client.Go(parent, MethodNotFoundHandler)
	server.Go(goCtx, MethodNotFoundHandler)

	// Cancel the server's read-loop context; the read loop stops at the next frame
	// boundary. There is no in-flight frame, so the cancellation is observed and
	// the connection drains to done.
	cancel()

	select {
	case <-server.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("server did not terminate after its Go context was canceled")
	}

	if err := server.Err(); err != nil {
		t.Errorf("Err after Go-context cancel = %v, want nil (clean shutdown)", err)
	}

	_ = client.Close()
	<-client.Done()
}
