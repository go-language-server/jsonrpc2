// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2_test

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"

	"go.lsp.dev/jsonrpc2"
)

// echoHandler replies to "echo" calls with their params and drops everything
// else with a method-not-found error.
func echoHandler(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	if req.Method() == "echo" {
		return reply(ctx, req.Params(), nil)
	}
	return reply(ctx, nil, jsonrpc2.ErrMethodNotFound)
}

// dialConn dials addr on network and returns a client [jsonrpc2.Conn] over the
// LSP header framing that the server uses, together with the underlying net.Conn
// so the caller can close it. It retries briefly so a test does not race a
// server that binds its listener asynchronously (such as ListenAndServe).
func dialConn(t *testing.T, network, addr string) (jsonrpc2.Conn, net.Conn) {
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

// TestServeRoundTrip exercises the gopls-style serving surface end to end over a
// real net.Listener: HandlerServer + Serve accept a connection and answer a
// typed round-trip call. It runs over both a TCP loopback listener and a unix
// socket.
func TestServeRoundTrip(t *testing.T) {
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
				return unixSocketAddr(t, "jsonrpc2.sock")
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			ln, err := net.Listen(tt.network, tt.addr(t))
			if err != nil {
				t.Fatalf("listen: %v", err)
			}

			server := jsonrpc2.HandlerServer(echoHandler)
			var (
				serveErr error
				wg       sync.WaitGroup
			)
			wg.Go(func() {
				serveErr = jsonrpc2.Serve(ctx, ln, server, 0)
			})

			client, nc := dialConn(t, tt.network, ln.Addr().String())
			client.Go(ctx, jsonrpc2.MethodNotFoundHandler)

			var got jsonrpc2.RawMessage
			if _, err := client.Call(ctx, "echo", jsonrpc2.RawMessage(`{"k":1}`), &got); err != nil {
				t.Fatalf("Call: %v", err)
			}
			if diff := gocmp.Diff(`{"k":1}`, string(got)); diff != "" {
				t.Errorf("echo result mismatch (-want +got):\n%s", diff)
			}

			if _, err := client.Call(ctx, "missing", nil, nil); !errors.Is(err, jsonrpc2.ErrMethodNotFound) {
				t.Errorf("Call(missing) error = %v, want %v", err, jsonrpc2.ErrMethodNotFound)
			}

			_ = client.Close()
			nc.Close()

			// Cancel the parent context to stop Serve, then confirm it unwinds.
			cancel()
			wg.Wait()
			if !errors.Is(serveErr, context.Canceled) {
				t.Errorf("Serve returned %v, want context.Canceled", serveErr)
			}
		})
	}
}

// TestServeIdleTimeout asserts that Serve returns ErrIdleTimeout after the
// configured idle duration elapses with no active connections, exercising the
// connect/disconnect churn that re-arms the idle timer.
func TestServeIdleTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	server := jsonrpc2.HandlerServer(jsonrpc2.MethodNotFoundHandler)
	var (
		runErr error
		wg     sync.WaitGroup
	)
	wg.Go(func() {
		runErr = jsonrpc2.Serve(ctx, ln, server, 100*time.Millisecond)
	})

	// Churn a few connections so the idle timer is stopped and re-armed, then let
	// the idle period elapse with nothing connected.
	connect := func() net.Conn {
		nc, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return nc
	}
	c1 := connect()
	c2 := connect()
	c1.Close()
	c2.Close()
	c3 := connect()
	c3.Close()

	wg.Wait()
	if !errors.Is(runErr, jsonrpc2.ErrIdleTimeout) {
		t.Errorf("Serve returned %v, want ErrIdleTimeout", runErr)
	}
}

// TestServeContextCancelShutsDown asserts that canceling the context terminates
// Serve even while a connection is actively being served, and that the
// in-flight server goroutine is unwound (its stream closed) before Serve
// returns.
func TestServeContextCancelShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	served := make(chan struct{})
	server := jsonrpc2.ServerFunc(func(ctx context.Context, conn jsonrpc2.Conn) error {
		conn.Go(ctx, jsonrpc2.MethodNotFoundHandler)
		close(served)
		<-conn.Done()
		return conn.Err()
	})

	var (
		runErr error
		wg     sync.WaitGroup
	)
	wg.Go(func() {
		runErr = jsonrpc2.Serve(ctx, ln, server, 0)
	})

	nc, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer nc.Close()

	// Wait until the server is actively serving the connection, then cancel.
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("server never began serving the connection")
	}

	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}

	if !errors.Is(runErr, context.Canceled) {
		t.Errorf("Serve returned %v, want context.Canceled", runErr)
	}
}

// TestServeNoGoroutineLeak asserts that Serve leaks no goroutine: after it
// returns, the goroutine count settles back to the pre-Serve baseline.
func TestServeNoGoroutineLeak(t *testing.T) {
	baseline := goroutineCount(t)

	ctx, cancel := context.WithCancel(t.Context())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := jsonrpc2.HandlerServer(echoHandler)
	var (
		runErr error
		wg     sync.WaitGroup
	)
	wg.Go(func() {
		runErr = jsonrpc2.Serve(ctx, ln, server, 0)
	})

	// Serve a couple of real connections, then close them.
	for range 3 {
		client, nc := dialConn(t, "tcp", ln.Addr().String())
		client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
		var got jsonrpc2.RawMessage
		if _, err := client.Call(ctx, "echo", jsonrpc2.RawMessage(`1`), &got); err != nil {
			t.Fatalf("Call: %v", err)
		}
		_ = client.Close()
		nc.Close()
	}

	cancel()
	wg.Wait()
	if !errors.Is(runErr, context.Canceled) {
		t.Errorf("Serve returned %v, want context.Canceled", runErr)
	}
	ln.Close()

	// The client connections spawned read goroutines too; allow everything to
	// settle, then confirm the count returned to baseline.
	if leaked := waitForGoroutines(t, baseline); leaked > 0 {
		t.Errorf("goroutine leak: %d goroutine(s) above baseline %d remain", leaked, baseline)
	}
}

// goroutineCount returns the current number of goroutines after a short settle.
func goroutineCount(t *testing.T) int {
	t.Helper()
	runtime.GC()
	return runtime.NumGoroutine()
}

// waitForGoroutines polls the goroutine count until it falls to within a small
// tolerance of baseline or a deadline elapses, returning the residual count
// above baseline. The tolerance absorbs runtime-internal goroutines that the
// test framework or scheduler may transiently hold.
func waitForGoroutines(t *testing.T, baseline int) int {
	t.Helper()
	const tolerance = 2
	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.GC()
		got := runtime.NumGoroutine()
		over := got - baseline
		if over <= tolerance {
			return 0
		}
		if time.Now().After(deadline) {
			return over
		}
		time.Sleep(20 * time.Millisecond)
	}
}
