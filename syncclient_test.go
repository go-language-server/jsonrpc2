// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// syncClientServer starts an ordinary Conn server answering "echo" (params back
// under "got"), "fail" (an InvalidParams error), and "void" (nil result), paired
// with a SyncClient over a net.Pipe with the given framer. It returns the client
// and a cleanup function.
func syncClientServer(t *testing.T, framer Framer) (*SyncClient, func()) {
	t.Helper()
	ca, cb := net.Pipe()
	client, err := NewSyncClient(framer(ca))
	if err != nil {
		t.Fatalf("NewSyncClient: %v", err)
	}
	server := NewConn(framer(cb))
	ctx, cancel := context.WithCancel(context.Background())
	server.Go(ctx, func(ctx context.Context, reply Replier, req Request) error {
		switch req.Method() {
		case "echo":
			return reply(ctx, raw(`{"got":`+string(orNull(req.Params()))+`}`), nil)
		case "fail":
			return reply(ctx, nil, NewError(InvalidParams, "bad params"))
		default:
			return reply(ctx, nil, nil)
		}
	})
	cleanup := func() {
		cancel()
		_ = client.Close()
		_ = server.Close()
		<-server.Done()
	}
	return client, cleanup
}

func TestSyncClientCallRoundTrip(t *testing.T) {
	t.Parallel()
	for name, framer := range map[string]Framer{
		"ndjson": NewNDJSONStream,
		"header": NewHeaderStream,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			client, cleanup := syncClientServer(t, framer)
			defer cleanup()

			var got RawMessage
			id, err := client.Call(ctx, "echo", raw(`{"x":1}`), &got)
			if err != nil {
				t.Fatalf("Call echo: %v", err)
			}
			if !id.IsValid() {
				t.Fatalf("Call returned invalid id")
			}
			if want := `{"got":{"x":1}}`; string(got) != want {
				t.Fatalf("echo result: got %s want %s", got, want)
			}

			// A second call reuses the single read loop and gets a distinct id.
			var got2 RawMessage
			id2, err := client.Call(ctx, "echo", raw(`{"x":2}`), &got2)
			if err != nil {
				t.Fatalf("Call echo 2: %v", err)
			}
			if id2 == id {
				t.Fatalf("second call reused id %v", id)
			}
			if want := `{"got":{"x":2}}`; string(got2) != want {
				t.Fatalf("echo result 2: got %s want %s", got2, want)
			}
		})
	}
}

func TestSyncClientCallError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, cleanup := syncClientServer(t, NewNDJSONStream)
	defer cleanup()

	var got RawMessage
	if _, err := client.Call(ctx, "fail", nil, &got); err == nil {
		t.Fatal("Call fail: expected error, got nil")
	} else if e, ok := err.(*Error); !ok || e.Code != InvalidParams {
		t.Fatalf("Call fail: got %v want InvalidParams *Error", err)
	}

	// The client is still usable after an error response.
	if _, err := client.Call(ctx, "void", nil, nil); err != nil {
		t.Fatalf("Call void after error: %v", err)
	}
}

func TestSyncClientNotifyThenCall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ca, cb := net.Pipe()
	client, err := NewSyncClient(NewNDJSONStream(ca))
	if err != nil {
		t.Fatalf("NewSyncClient: %v", err)
	}
	server := NewConn(NewNDJSONStream(cb))
	var notes sync.WaitGroup
	notes.Add(1)
	var seen string
	var seenMu sync.Mutex
	serverCtx, cancel := context.WithCancel(context.Background())
	server.Go(serverCtx, func(ctx context.Context, reply Replier, req Request) error {
		if req.Method() == "note" {
			seenMu.Lock()
			seen = string(orNull(req.Params()))
			seenMu.Unlock()
			notes.Done()
			return reply(ctx, nil, nil)
		}
		return reply(ctx, nil, nil)
	})
	defer func() {
		cancel()
		_ = client.Close()
		_ = server.Close()
		<-server.Done()
	}()

	// A notification produces no response and does not block the next call.
	if err := client.Notify(ctx, "note", raw(`"hi"`)); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if _, err := client.Call(ctx, "void", nil, nil); err != nil {
		t.Fatalf("Call void after notify: %v", err)
	}

	done := make(chan struct{})
	go func() { notes.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not observe the notification")
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if seen != `"hi"` {
		t.Fatalf("notification params: got %s want %q", seen, `"hi"`)
	}
}

// TestSyncClientEquivalentToConn asserts the SyncClient produces the same result
// bytes as an ordinary Conn client for the same request, proving the mode change
// does not alter observable RPC behavior.
func TestSyncClientEquivalentToConn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// SyncClient path.
	sc, scCleanup := syncClientServer(t, NewNDJSONStream)
	defer scCleanup()
	var scResult RawMessage
	if _, err := sc.Call(ctx, "echo", raw(`{"a":[1,2,3]}`), &scResult); err != nil {
		t.Fatalf("SyncClient echo: %v", err)
	}

	// Conn path.
	ca, cb := net.Pipe()
	cc := NewConn(NewNDJSONStream(ca))
	server := NewConn(NewNDJSONStream(cb))
	connCtx, cancel := context.WithCancel(context.Background())
	cc.Go(connCtx, MethodNotFoundHandler)
	server.Go(connCtx, func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, raw(`{"got":`+string(orNull(req.Params()))+`}`), nil)
	})
	defer func() {
		cancel()
		_ = cc.Close()
		<-cc.Done()
		_ = server.Close()
		<-server.Done()
	}()
	var connResult RawMessage
	if _, err := cc.Call(ctx, "echo", raw(`{"a":[1,2,3]}`), &connResult); err != nil {
		t.Fatalf("Conn echo: %v", err)
	}

	if diff := gocmp.Diff(string(connResult), string(scResult)); diff != "" {
		t.Fatalf("SyncClient vs Conn result mismatch (-conn +sync):\n%s", diff)
	}
}

// TestCallMarshalErrorZeroID locks the cross-client contract that a local marshal
// failure — which happens before anything is sent or registered — returns a zero
// ID, never a "would-have-been" id. Conn and SyncClient must behave identically.
func TestCallMarshalErrorZeroID(t *testing.T) {
	t.Parallel()
	// A channel cannot be marshaled by any codec, so marshalParams fails before
	// the call is assigned an id, registered, or written.
	badParams := make(chan int)

	t.Run("conn", func(t *testing.T) {
		t.Parallel()
		ca, cb := net.Pipe()
		defer cb.Close()
		c := NewConn(NewNDJSONStream(ca))
		defer c.Close()
		id, err := c.Call(t.Context(), "m", badParams, nil)
		if err == nil {
			t.Fatal("Conn.Call: expected a marshal error, got nil")
		}
		if id.IsValid() {
			t.Fatalf("Conn.Call returned valid id %v on marshal error; want zero ID", id)
		}
	})

	t.Run("syncclient", func(t *testing.T) {
		t.Parallel()
		ca, cb := net.Pipe()
		defer cb.Close()
		sc, err := NewSyncClient(NewNDJSONStream(ca))
		if err != nil {
			t.Fatalf("NewSyncClient: %v", err)
		}
		defer sc.Close()
		id, err := sc.Call(t.Context(), "m", badParams, nil)
		if err == nil {
			t.Fatal("SyncClient.Call: expected a marshal error, got nil")
		}
		if id.IsValid() {
			t.Fatalf("SyncClient.Call returned valid id %v on marshal error; want zero ID", id)
		}
	})
}
