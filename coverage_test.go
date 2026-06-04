// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"go.lsp.dev/jsonrpc2"
)

// countingCodec wraps the default codec and records how many times Marshal and
// Unmarshal are invoked, so a test can prove a connection built with
// [jsonrpc2.WithCodec] actually routes its payloads through the supplied codec.
type countingCodec struct {
	marshals   atomic.Int64
	unmarshals atomic.Int64
}

func (c *countingCodec) Marshal(v any) ([]byte, error) {
	c.marshals.Add(1)
	return jsonrpc2.DefaultCodec.Marshal(v)
}

func (c *countingCodec) Unmarshal(data []byte, v any) error {
	c.unmarshals.Add(1)
	return jsonrpc2.DefaultCodec.Unmarshal(data, v)
}

// TestWithCodec proves that a connection created with [jsonrpc2.WithCodec] uses
// the supplied codec for the client's call params and result, rather than the
// default codec, by counting the codec's invocations across a typed round-trip.
func TestWithCodec(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	ca, cb := net.Pipe()
	cc := &countingCodec{}
	client := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(ca), jsonrpc2.WithCodec(cc))
	server := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(cb))

	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	server.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		// Echo the params straight back as the result.
		return reply(ctx, req.Params(), nil)
	})
	defer func() {
		_ = client.Close()
		_ = server.Close()
	}()

	type payload struct {
		Name string `json:"name"`
	}
	var got payload
	if _, err := client.Call(ctx, "echo", payload{Name: "hello"}, &got); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if diff := gocmp.Diff(payload{Name: "hello"}, got); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}

	// The client marshaled the params and unmarshaled the result through the
	// custom codec at least once each.
	if n := cc.marshals.Load(); n < 1 {
		t.Errorf("custom codec Marshal calls = %d, want >= 1", n)
	}
	if n := cc.unmarshals.Load(); n < 1 {
		t.Errorf("custom codec Unmarshal calls = %d, want >= 1", n)
	}
}

// TestWithCodecNilIgnored asserts that WithCodec(nil) is a no-op: the connection
// keeps the default codec and still round-trips, so a caller cannot accidentally
// disable payload encoding by passing a nil codec.
func TestWithCodecNilIgnored(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	ca, cb := net.Pipe()
	client := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(ca), jsonrpc2.WithCodec(nil))
	server := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(cb))
	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	server.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		return reply(ctx, req.Params(), nil)
	})
	defer func() {
		_ = client.Close()
		_ = server.Close()
	}()

	var got jsonrpc2.RawMessage
	if _, err := client.Call(ctx, "echo", jsonrpc2.RawMessage(`{"k":1}`), &got); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if diff := gocmp.Diff(`{"k":1}`, string(got)); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}
}

// TestNewRawStreamRoundTrip exercises [jsonrpc2.NewRawStream], the gopls-named
// alias for the newline-delimited JSON framing, by round-tripping a call over a
// pair of connections built with it.
func TestNewRawStreamRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	ca, cb := net.Pipe()
	client := jsonrpc2.NewConn(jsonrpc2.NewRawStream(ca))
	server := jsonrpc2.NewConn(jsonrpc2.NewRawStream(cb))
	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	server.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		return reply(ctx, req.Params(), nil)
	})
	defer func() {
		_ = client.Close()
		_ = server.Close()
	}()

	var got jsonrpc2.RawMessage
	if _, err := client.Call(ctx, "echo", jsonrpc2.RawMessage(`[1,2,3]`), &got); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if diff := gocmp.Diff(`[1,2,3]`, string(got)); diff != "" {
		t.Errorf("NewRawStream round-trip mismatch (-want +got):\n%s", diff)
	}
}

// TestRequestContextDeadline asserts that the context passed to a handler honors
// the connection's reading deadline plumbing: a handler observing ctx.Deadline()
// sees the deadline of the context that [jsonrpc2.Conn.Go] was started with,
// exercising the request context's Deadline delegation.
func TestRequestContextDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	wantDeadline, _ := parent.Deadline()

	ca, cb := net.Pipe()
	client := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(ca))
	server := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(cb))

	gotDeadline := make(chan time.Time, 1)
	client.Go(parent, jsonrpc2.MethodNotFoundHandler)
	server.Go(parent, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		dl, ok := ctx.Deadline()
		if ok {
			gotDeadline <- dl
		} else {
			gotDeadline <- time.Time{}
		}
		return reply(ctx, nil, nil)
	})
	defer func() {
		_ = client.Close()
		_ = server.Close()
	}()

	if _, err := client.Call(parent, "probe", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}

	select {
	case dl := <-gotDeadline:
		if !dl.Equal(wantDeadline) {
			t.Errorf("handler ctx.Deadline() = %v, want %v (inherited from Go's context)", dl, wantDeadline)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never reported its deadline")
	}
}

// TestListenAndServe exercises [jsonrpc2.ListenAndServe] over both a TCP loopback
// address and a unix socket: it starts the server, round-trips a call, and then
// cancels the context to unwind it, confirming the listener-owning entry point
// works the same as constructing a listener and calling [jsonrpc2.Serve].
func TestListenAndServe(t *testing.T) {
	tests := map[string]struct {
		network string
		addr    func(t *testing.T) string
	}{
		"success: tcp loopback": {
			network: "tcp",
			addr:    func(*testing.T) string { return "127.0.0.1:0" },
		},
		"success: unix socket": {
			network: "unix",
			addr: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "lasrv.sock")
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			// Bind first so the test can learn the actual address, then close that
			// listener and let ListenAndServe rebind it (loopback ":0" would
			// otherwise pick an unknowable port).
			probe, err := net.Listen(tt.network, tt.addr(t))
			if err != nil {
				t.Fatalf("probe listen: %v", err)
			}
			addr := probe.Addr().String()
			network := tt.network
			probe.Close()

			server := jsonrpc2.HandlerServer(echoHandler)
			var (
				runErr error
				wg     sync.WaitGroup
			)
			wg.Go(func() {
				runErr = jsonrpc2.ListenAndServe(ctx, network, addr, server, 0)
			})

			// ListenAndServe needs a moment to bind; retry the dial until it is up.
			client, nc := dialWithRetry(t, network, addr)
			client.Go(ctx, jsonrpc2.MethodNotFoundHandler)

			var got jsonrpc2.RawMessage
			if _, err := client.Call(ctx, "echo", jsonrpc2.RawMessage(`{"ok":true}`), &got); err != nil {
				t.Fatalf("Call: %v", err)
			}
			if diff := gocmp.Diff(`{"ok":true}`, string(got)); diff != "" {
				t.Errorf("echo result mismatch (-want +got):\n%s", diff)
			}

			_ = client.Close()
			nc.Close()

			cancel()
			wg.Wait()
			if !errors.Is(runErr, context.Canceled) {
				t.Errorf("ListenAndServe returned %v, want context.Canceled", runErr)
			}
		})
	}
}

// dialWithRetry dials addr, retrying briefly so a test does not race the server's
// bind. It returns a client connection over the LSP header framing the server
// uses, together with the underlying net.Conn for teardown.
func dialWithRetry(t *testing.T, network, addr string) (jsonrpc2.Conn, net.Conn) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		nc, err := net.DialTimeout(network, addr, time.Second)
		if err == nil {
			return jsonrpc2.NewConn(jsonrpc2.NewStream(nc)), nc
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s %s: %v", network, addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
