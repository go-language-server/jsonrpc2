// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// connPair builds two connected [Conn] endpoints over the supplied framer and a
// transport. The transport is one of an in-memory net.Pipe or a pair of real
// os.Pipe file descriptors, selected by the caller, so the same tests run over
// both. The endpoints are not yet reading; the caller drives them with Go.
type transport func(t *testing.T) (a, b connEnd)

// connEnd bundles a stream with a cleanup hook for the underlying fds.
type connEnd struct {
	stream Stream
}

// memTransport pairs two endpoints over an in-memory, synchronous net.Pipe.
func memTransport(framer Framer) transport {
	return func(t *testing.T) (connEnd, connEnd) {
		t.Helper()
		ca, cb := net.Pipe()
		return connEnd{stream: framer(ca)}, connEnd{stream: framer(cb)}
	}
}

// osPipeTransport pairs two endpoints over two real os.Pipe pairs, exercising
// the framer over actual file descriptors. Closing either stream closes its fds,
// which interrupts a blocked Read on the peer.
func osPipeTransport(framer Framer) transport {
	return func(t *testing.T) (connEnd, connEnd) {
		t.Helper()
		ar, aw, err := os.Pipe()
		if err != nil {
			t.Fatalf("os.Pipe: %v", err)
		}
		br, bw, err := os.Pipe()
		if err != nil {
			t.Fatalf("os.Pipe: %v", err)
		}
		// a reads from ar, writes to bw; b reads from br, writes to aw.
		return connEnd{stream: framer(&fdConn{r: ar, w: bw})},
			connEnd{stream: framer(&fdConn{r: br, w: aw})}
	}
}

// fdConn adapts a read fd and a write fd into an io.ReadWriteCloser whose Close
// closes both, interrupting any blocked Read on the peer.
type fdConn struct {
	r *os.File
	w *os.File
}

func (c *fdConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *fdConn) Write(p []byte) (int, error) { return c.w.Write(p) }

func (c *fdConn) Close() error {
	err1 := c.r.Close()
	err2 := c.w.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// transports enumerates the framer x transport matrix used by the e2e tests.
func transports() map[string]transport {
	return map[string]transport{
		"ndjson/mem":    memTransport(NewNDJSONStream),
		"ndjson/ospipe": osPipeTransport(NewNDJSONStream),
		"header/mem":    memTransport(NewHeaderStream),
		"header/ospipe": osPipeTransport(NewHeaderStream),
	}
}

// echoHandler replies to a call with its params echoed back under "echo", and to
// a notification by recording it on recorded.
type echoHandler struct {
	mu       sync.Mutex
	recorded []string
}

func (h *echoHandler) handle(ctx context.Context, reply Replier, req Request) error {
	switch req.Method() {
	case "echo":
		return reply(ctx, raw(`{"got":`+string(orNull(req.Params()))+`}`), nil)
	case "fail":
		return reply(ctx, nil, NewError(InvalidParams, "bad params"))
	case "note":
		h.mu.Lock()
		h.recorded = append(h.recorded, string(orNull(req.Params())))
		h.mu.Unlock()
		return reply(ctx, nil, nil)
	default:
		return MethodNotFoundHandler(ctx, reply, req)
	}
}

// raw is a brief alias for a raw, pre-encoded JSON payload.
func raw(s string) RawMessage { return RawMessage(s) }

func orNull(b RawMessage) RawMessage {
	if len(b) == 0 {
		return RawMessage("null")
	}
	return b
}

func TestCallRoundTrip(t *testing.T) {
	t.Parallel()
	for name, newTransport := range transports() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			ea, eb := newTransport(t)
			client := NewConn(ea.stream)
			h := &echoHandler{}
			server := NewConn(eb.stream)
			client.Go(ctx, MethodNotFoundHandler)
			server.Go(ctx, h.handle)
			defer closeBoth(t, client, server)

			tests := map[string]struct {
				method  string
				params  any
				want    string
				wantErr *Error
			}{
				"success: echo with params": {
					method: "echo",
					params: map[string]int{"n": 7},
					want:   `{"got":{"n":7}}`,
				},
				"success: echo nil params": {
					method: "echo",
					params: nil,
					want:   `{"got":null}`,
				},
				"error: handler returns wire error": {
					method:  "fail",
					params:  nil,
					wantErr: NewError(InvalidParams, "bad params"),
				},
			}
			for tn, tt := range tests {
				t.Run(tn, func(t *testing.T) {
					var got RawMessage
					id, err := client.Call(ctx, tt.method, tt.params, &got)
					if !id.IsValid() {
						t.Fatalf("expected a valid id, got %v", id)
					}
					if tt.wantErr != nil {
						var we *Error
						if !errors.As(err, &we) {
							t.Fatalf("want *Error, got %v", err)
						}
						if we.Code != tt.wantErr.Code {
							t.Fatalf("code: got %d want %d", we.Code, tt.wantErr.Code)
						}
						return
					}
					if err != nil {
						t.Fatalf("Call: %v", err)
					}
					if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
						t.Fatalf("result mismatch (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
}

func TestNotifyNoResponse(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := memTransport(NewNDJSONStream)(t)
	client := NewConn(ea.stream)
	h := &echoHandler{}
	server := NewConn(eb.stream)
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, h.handle)
	defer closeBoth(t, client, server)

	if err := client.Notify(ctx, "note", map[string]int{"x": 1}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	// Round-trip a call to ensure the notification was processed before asserting.
	if _, err := client.Call(ctx, "echo", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if diff := gocmp.Diff([]string{`{"x":1}`}, h.recorded); diff != "" {
		t.Fatalf("recorded notifications mismatch (-want +got):\n%s", diff)
	}
}

func TestCancelPropagatesToHandler(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := memTransport(NewNDJSONStream)(t)
	client := NewConn(ea.stream)

	started := make(chan struct{})
	handlerErr := make(chan error, 1)
	// CancelHandler exposes a canceller keyed by id; AsyncHandler frees the read
	// loop so the cancel notification can be observed while the call is blocked.
	base := func(ctx context.Context, reply Replier, req Request) error {
		if req.Method() == "block" {
			close(started)
			<-ctx.Done()
			handlerErr <- ctx.Err()
			return reply(ctx, nil, ctx.Err())
		}
		return reply(ctx, nil, nil)
	}
	h, canceller := CancelHandler(AsyncHandler(base))
	server := NewConn(eb.stream)
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, h)
	defer closeBoth(t, client, server)

	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = client.Call(ctx, "block", nil, nil)
	}()

	<-started
	canceller(NewNumberID(1))

	select {
	case err := <-handlerErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handler ctx err: got %v want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not canceled")
	}
	<-callDone
}

func TestClientContextCancelStopsWaiting(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := memTransport(NewNDJSONStream)(t)
	client := NewConn(ea.stream)
	release := make(chan struct{})
	server := NewConn(eb.stream)
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, AsyncHandler(func(ctx context.Context, reply Replier, req Request) error {
		<-release
		return reply(ctx, nil, nil)
	}))
	defer closeBoth(t, client, server)
	defer close(release)

	callCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		_, err := client.Call(callCtx, "slow", nil, nil)
		errc <- err
	}()

	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Call after cancel: got %v want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return after ctx cancel")
	}
}

func TestCanceledCallLateResponseDoesNotReachLaterCall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := memTransport(NewNDJSONStream)(t)
	client := NewConn(ea.stream)

	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	slowReplied := make(chan error, 1)
	server := NewConn(eb.stream)
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, AsyncHandler(func(ctx context.Context, reply Replier, req Request) error {
		switch req.Method() {
		case "slow":
			close(slowStarted)
			<-slowRelease
			err := reply(ctx, "late", nil)
			slowReplied <- err
			return err
		case "fast":
			return reply(ctx, "fast", nil)
		default:
			return reply(ctx, nil, ErrMethodNotFound)
		}
	}))
	defer closeBoth(t, client, server)

	callCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		_, err := client.Call(callCtx, "slow", nil, nil)
		errc <- err
	}()

	<-slowStarted
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Call error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled Call did not return")
	}

	var got string
	if _, err := client.Call(ctx, "fast", nil, &got); err != nil {
		t.Fatalf("second Call: %v", err)
	}
	if got != "fast" {
		t.Fatalf("second Call result = %q, want fast", got)
	}

	close(slowRelease)
	select {
	case err := <-slowReplied:
		if err != nil {
			t.Fatalf("late reply: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late reply did not flush")
	}

	got = ""
	if _, err := client.Call(ctx, "fast", nil, &got); err != nil {
		t.Fatalf("third Call after late response: %v", err)
	}
	if got != "fast" {
		t.Fatalf("third Call result = %q, want fast", got)
	}
}

func TestGracefulClose(t *testing.T) {
	t.Parallel()
	for name, newTransport := range transports() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			ea, eb := newTransport(t)
			client := NewConn(ea.stream)
			server := NewConn(eb.stream)
			client.Go(ctx, MethodNotFoundHandler)
			server.Go(ctx, func(ctx context.Context, reply Replier, req Request) error {
				return reply(ctx, "ok", nil)
			})

			var s string
			if _, err := client.Call(ctx, "m", nil, &s); err != nil {
				t.Fatalf("Call: %v", err)
			}

			if err := client.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			select {
			case <-client.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("client.Done did not close")
			}
			if err := client.Err(); err != nil {
				t.Fatalf("client.Err after clean close: %v", err)
			}
			// New calls are rejected after close.
			if _, err := client.Call(ctx, "m", nil, &s); !errors.Is(err, ErrClientClosing) {
				t.Fatalf("Call after close: got %v want ErrClientClosing", err)
			}
			if err := client.Notify(ctx, "m", nil); !errors.Is(err, ErrClientClosing) {
				t.Fatalf("Notify after close: got %v want ErrClientClosing", err)
			}
			// The peer observes the closed stream and terminates cleanly.
			select {
			case <-server.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("server.Done did not close after peer close")
			}
			if err := server.Err(); err != nil {
				t.Fatalf("server.Err after peer close: %v", err)
			}
		})
	}
}

func TestSyncAsyncParity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	base := func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, raw(`{"n":`+string(orNull(req.Params()))+`}`), nil)
	}

	modes := map[string]Handler{
		"sync":  base,
		"async": AsyncHandler(base),
	}
	results := make(map[string][]string)
	for mode, handler := range modes {
		ea, eb := memTransport(NewNDJSONStream)(t)
		client := NewConn(ea.stream)
		server := NewConn(eb.stream)
		client.Go(ctx, MethodNotFoundHandler)
		server.Go(ctx, handler)

		var got []string
		for i := range 20 {
			var r RawMessage
			if _, err := client.Call(ctx, "m", i, &r); err != nil {
				t.Fatalf("%s call %d: %v", mode, i, err)
			}
			got = append(got, string(r))
		}
		results[mode] = got
		closeBoth(t, client, server)
	}
	if diff := gocmp.Diff(results["sync"], results["async"]); diff != "" {
		t.Fatalf("sync vs async result mismatch (-sync +async):\n%s", diff)
	}
}

func TestAsyncRunsConcurrently(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := memTransport(NewNDJSONStream)(t)
	client := NewConn(ea.stream)

	const n = 8
	var inFlight atomic.Int32
	var maxConcurrent atomic.Int32
	gate := make(chan struct{})

	server := NewConn(eb.stream)
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, AsyncHandler(func(ctx context.Context, reply Replier, req Request) error {
		cur := inFlight.Add(1)
		for {
			old := maxConcurrent.Load()
			if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
				break
			}
		}
		<-gate // hold until all are in flight
		inFlight.Add(-1)
		return reply(ctx, nil, nil)
	}))
	defer closeBoth(t, client, server)

	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			_, _ = client.Call(ctx, "m", nil, nil)
		})
	}
	// Wait until all handlers are concurrently parked, then release.
	deadline := time.After(2 * time.Second)
	for maxConcurrent.Load() < n {
		select {
		case <-deadline:
			t.Fatalf("only %d handlers ran concurrently, want %d", maxConcurrent.Load(), n)
		default:
			runtime.Gosched()
		}
	}
	close(gate)
	wg.Wait()
}

func TestConcurrencyStress(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := osPipeTransport(NewNDJSONStream)(t)
	client := NewConn(ea.stream)
	server := NewConn(eb.stream)
	client.Go(ctx, MethodNotFoundHandler)
	// The server echoes the numeric params back as the result so each response
	// can be verified against the request that produced it.
	server.Go(ctx, AsyncHandler(func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, raw(string(orNull(req.Params()))), nil)
	}))
	defer closeBoth(t, client, server)

	const goroutines = 8
	const perGoroutine = 200
	var wg sync.WaitGroup
	errc := make(chan error, goroutines)
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perGoroutine {
				want := g*perGoroutine + i
				var got RawMessage
				if _, err := client.Call(ctx, "echo", want, &got); err != nil {
					errc <- fmt.Errorf("g%d i%d: %w", g, i, err)
					return
				}
				if string(got) != fmt.Sprintf("%d", want) {
					errc <- fmt.Errorf("g%d i%d: got %q want %d", g, i, got, want)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
}

func TestGoroutineLeak(t *testing.T) {
	// Not parallel: NumGoroutine is process-global.
	ctx := t.Context()
	base := runtime.NumGoroutine()

	for range 20 {
		ea, eb := memTransport(NewNDJSONStream)(t)
		client := NewConn(ea.stream)
		server := NewConn(eb.stream)
		client.Go(ctx, MethodNotFoundHandler)
		server.Go(ctx, AsyncHandler(func(ctx context.Context, reply Replier, req Request) error {
			return reply(ctx, "ok", nil)
		}))
		for range 5 {
			var s string
			if _, err := client.Call(ctx, "m", nil, &s); err != nil {
				t.Fatalf("Call: %v", err)
			}
		}
		_ = client.Close()
		<-client.Done()
		_ = server.Close()
		<-server.Done()
	}

	// Poll for the count to settle back to baseline; scheduled-out goroutines may
	// take a moment to exit.
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

func TestCloseDrainsInFlight(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := memTransport(NewNDJSONStream)(t)
	client := NewConn(ea.stream)

	started := make(chan struct{})
	release := make(chan struct{})
	server := NewConn(eb.stream)
	client.Go(ctx, MethodNotFoundHandler)
	// The handler is released for concurrent handling so the read loop is free,
	// then it parks until the test releases it: the request is genuinely in flight
	// while Close is called.
	server.Go(ctx, AsyncHandler(func(ctx context.Context, reply Replier, req Request) error {
		close(started)
		<-release
		return reply(ctx, "done", nil)
	}))

	// The parked call's response is not guaranteed to flush once the server starts
	// shutting down, so the call is driven with its own cancelable context and is
	// abandoned at the end of the test.
	callCtx, cancelCall := context.WithCancel(ctx)
	defer cancelCall()
	go func() {
		_, _ = client.Call(callCtx, "park", nil, nil)
	}()
	<-started

	// Close the server while its handler is parked. Close must block until the
	// in-flight handler drains, so it must not have returned yet.
	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned while a handler was still in flight")
	case <-time.After(200 * time.Millisecond):
	}

	// Release the handler; Close must now complete cleanly even though the late
	// response is dropped by the shutting-down writer.
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close after drain: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the handler drained")
	}
	select {
	case <-server.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("server.Done did not close")
	}
	if err := server.Err(); err != nil {
		t.Fatalf("server.Err after drain close: %v", err)
	}

	// Abandon the parked client call, then close the client cleanly.
	cancelCall()
	_ = client.Close()
	<-client.Done()
}

// recordingPreempter handles the "preempt" method inline and defers everything
// else to the handler, recording which methods it saw.
type recordingPreempter struct {
	mu   sync.Mutex
	seen []string
}

func (p *recordingPreempter) Preempt(ctx context.Context, req Request) (any, error) {
	p.mu.Lock()
	p.seen = append(p.seen, req.Method())
	p.mu.Unlock()
	if req.Method() == "preempt" {
		return raw(`"preempted"`), nil
	}
	if req.Method() == "nil-defer" {
		return nil, nil
	}
	if req.Method() == "result-not-handled" {
		return raw(`"ignored"`), ErrNotHandled
	}
	return nil, ErrNotHandled
}

func TestPreempter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	ea, eb := memTransport(NewNDJSONStream)(t)
	client := NewConn(ea.stream)

	pre := &recordingPreempter{}
	var handlerSawPreempt atomic.Bool
	server := NewConn(eb.stream, WithPreempter(pre))
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, func(ctx context.Context, reply Replier, req Request) error {
		if req.Method() == "preempt" {
			handlerSawPreempt.Store(true)
		}
		return reply(ctx, raw(`"handled"`), nil)
	})
	defer closeBoth(t, client, server)

	// A preempted method is answered by the preempter and never reaches the handler.
	var got RawMessage
	if _, err := client.Call(ctx, "preempt", nil, &got); err != nil {
		t.Fatalf("Call preempt: %v", err)
	}
	if string(got) != `"preempted"` {
		t.Fatalf("preempt result: got %s want \"preempted\"", got)
	}

	// A non-preempted method falls through to the handler.
	if _, err := client.Call(ctx, "normal", nil, &got); err != nil {
		t.Fatalf("Call normal: %v", err)
	}
	if string(got) != `"handled"` {
		t.Fatalf("normal result: got %s want \"handled\"", got)
	}

	// A nil handled value also falls through to the handler.
	if _, err := client.Call(ctx, "nil-defer", nil, &got); err != nil {
		t.Fatalf("Call nil-defer: %v", err)
	}
	if string(got) != `"handled"` {
		t.Fatalf("nil-defer result: got %s want \"handled\"", got)
	}

	// ErrNotHandled always falls through, even if a stale handled value is present.
	if _, err := client.Call(ctx, "result-not-handled", nil, &got); err != nil {
		t.Fatalf("Call result-not-handled: %v", err)
	}
	if string(got) != `"handled"` {
		t.Fatalf("result-not-handled result: got %s want \"handled\"", got)
	}
	if handlerSawPreempt.Load() {
		t.Fatal("handler observed a preempted request")
	}
}

// closeBoth closes both connections and waits for them to terminate, failing the
// test on a close error.
func closeBoth(t *testing.T, conns ...Conn) {
	t.Helper()
	for _, c := range conns {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
	for _, c := range conns {
		select {
		case <-c.Done():
		case <-time.After(2 * time.Second):
			t.Errorf("Done did not close")
		}
	}
}

// TestCloseRacesConcurrentWrites stresses the lock-guarded idle->close edge
// against concurrent writes. Many goroutines drive Call/Notify while Close fires
// partway through; the test asserts that Done still closes within a generous
// timeout on every iteration. A flaw in the idle-detection-to-close transition
// (for example, observing idle on one path while the close edge is decided on
// another) would manifest as a Close that hangs forever — a liveness bug the race
// detector cannot catch — so this timeout assertion is the gate rather than -race
// alone.
func TestCloseRacesConcurrentWrites(t *testing.T) {
	t.Parallel()
	for iter := range 50 {
		ea, eb := memTransport(NewNDJSONStream)(t)
		client := NewConn(ea.stream)
		server := NewConn(eb.stream)
		ctx, cancel := context.WithCancel(context.Background())
		client.Go(ctx, MethodNotFoundHandler)
		server.Go(ctx, func(ctx context.Context, reply Replier, req Request) error {
			return reply(ctx, nil, nil)
		})

		// Drive a burst of concurrent writers; some will race the Close.
		var wg sync.WaitGroup
		for w := range 8 {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for range 20 {
					// Errors are expected once shutdown begins; only liveness matters.
					if w%2 == 0 {
						_, _ = client.Call(ctx, "void", nil, nil)
					} else {
						_ = client.Notify(ctx, "void", nil)
					}
				}
			}(w)
		}

		// Close both ends while the writers are mid-burst.
		go func() { _ = client.Close() }()
		go func() { _ = server.Close() }()

		for _, conn := range []Conn{client, server} {
			select {
			case <-conn.Done():
			case <-time.After(5 * time.Second):
				cancel()
				t.Fatalf("iter %d: Done did not close within timeout (possible idle/close split-brain)", iter)
			}
		}
		cancel()
		wg.Wait()
	}
}

func TestConnReadNextDirectUnmarshalResult(t *testing.T) {
	ctx := context.Background()
	frame := []byte(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`)
	stream := &singleFrameStream{frame: frame}
	c := &conn{stream: stream, codec: DefaultCodec, done: make(chan struct{})}

	var got struct {
		OK bool `json:"ok"`
	}
	w := getWaiter(&got)
	c.state.outgoingCalls.Add(NewNumberID(7), w)

	req, msgs, resp, batch, err := c.readNext(ctx)
	if err != nil {
		t.Fatalf("readNext error: %v", err)
	}
	if req != nil || msgs != nil || resp != nil || batch {
		t.Fatalf("readNext = req %T, msgs %d, resp %T, batch %v; want delivered response", req, len(msgs), resp, batch)
	}

	select {
	case <-w.ready:
	case <-time.After(time.Second):
		t.Fatal("waiter was not delivered")
	}
	if w.err != nil {
		t.Fatalf("waiter err = %v", w.err)
	}
	if !w.resultReady {
		t.Fatal("waiter resultReady = false, want true")
	}
	if !got.OK {
		t.Fatalf("direct-unmarshaled result OK = false, want true")
	}
	if c.state.outgoingCalls.Len() != 0 {
		t.Fatalf("outgoing calls len = %d, want 0", c.state.outgoingCalls.Len())
	}
	putWaiter(w)
}

func TestConnReadNextDirectUnmarshalSkipsLargeResult(t *testing.T) {
	ctx := context.Background()
	large := strings.Repeat("x", maxConnDirectUnmarshalResult+1)
	frame := []byte(`{"jsonrpc":"2.0","id":9,"result":"` + large + `"}`)
	stream := &singleFrameStream{frame: frame}
	c := &conn{stream: stream, codec: DefaultCodec, done: make(chan struct{})}

	var got RawMessage
	w := getWaiter(&got)
	c.state.outgoingCalls.Add(NewNumberID(9), w)

	req, msgs, resp, batch, err := c.readNext(ctx)
	if err != nil {
		t.Fatalf("readNext error: %v", err)
	}
	if req != nil || msgs != nil || resp == nil || batch {
		t.Fatalf("readNext = req %T, msgs %d, resp %T, batch %v; want owned response fallback", req, len(msgs), resp, batch)
	}
	if len(resp.result) <= maxConnDirectUnmarshalResult {
		t.Fatalf("response result len = %d, want > %d", len(resp.result), maxConnDirectUnmarshalResult)
	}
	select {
	case <-w.ready:
		t.Fatal("waiter delivered on oversized direct-unmarshal fallback")
	default:
	}
	if c.state.outgoingCalls.Len() != 1 {
		t.Fatalf("outgoing calls len = %d, want 1 before deliverResponse", c.state.outgoingCalls.Len())
	}
	if taken, ok := c.state.outgoingCalls.Take(NewNumberID(9)); !ok || taken != w {
		t.Fatalf("outgoing call was not left pending for deliverResponse")
	}
	putWaiter(w)
}

func TestConnCallFallbackAcceptsExtraResponseFields(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		frame string
	}{
		{
			name:  "extra field after result",
			frame: `{"jsonrpc":"2.0","id":1,"result":{"ok":true},"meta":1}`,
		},
		{
			name:  "extra field before result",
			frame: `{"jsonrpc":"2.0","id":1,"meta":1,"result":{"ok":true}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stream := newScriptedFrameStream([]byte(tt.frame))
			client := NewConn(stream)
			client.Go(t.Context(), MethodNotFoundHandler)
			defer func() {
				_ = client.Close()
				<-client.Done()
			}()

			var got struct {
				OK bool `json:"ok"`
			}
			if _, err := client.Call(t.Context(), "ok", nil, &got); err != nil {
				t.Fatalf("Call: %v", err)
			}
			if !got.OK {
				t.Fatalf("result OK = false, want true")
			}
		})
	}
}

func TestConnCallFallbackRejectsMalformedCanonicalResponse(t *testing.T) {
	t.Parallel()

	stream := newScriptedFrameStream([]byte(`{"jsonrpc":"2.0","id":1,"result":`))
	client := NewConn(stream)
	client.Go(t.Context(), MethodNotFoundHandler)
	defer func() {
		_ = client.Close()
		<-client.Done()
	}()

	var got RawMessage
	if _, err := client.Call(t.Context(), "broken", nil, &got); !errors.Is(err, ErrParse) {
		t.Fatalf("Call malformed response error = %v, want ErrParse", err)
	}
	if !errors.Is(client.Err(), ErrParse) {
		t.Fatalf("Conn.Err = %v, want ErrParse", client.Err())
	}
}

type singleFrameStream struct {
	frame []byte
}

func (s *singleFrameStream) ReadFrame(context.Context) ([]byte, int64, error) {
	return s.frame, int64(len(s.frame)), nil
}

func (s *singleFrameStream) WriteFrame(context.Context, []byte) (int64, error) {
	return 0, errors.New("unexpected WriteFrame")
}

func (s *singleFrameStream) Read(context.Context) (Message, int64, error) {
	return nil, 0, errors.New("unexpected Read")
}

func (s *singleFrameStream) Write(context.Context, Message) (int64, error) {
	return 0, errors.New("unexpected Write")
}

func (s *singleFrameStream) Close() error { return nil }

type scriptedFrameStream struct {
	frame  []byte
	frames chan []byte
	writes chan Message
	closed chan struct{}
	read   sync.Once
	close  sync.Once
}

func newScriptedFrameStream(frame []byte) *scriptedFrameStream {
	return &scriptedFrameStream{
		frame:  frame,
		frames: make(chan []byte, 1),
		writes: make(chan Message, 1),
		closed: make(chan struct{}),
	}
}

func (s *scriptedFrameStream) ReadFrame(ctx context.Context) ([]byte, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-s.closed:
		return nil, 0, io.EOF
	case frame, ok := <-s.frames:
		if !ok {
			return nil, 0, io.EOF
		}
		return frame, int64(len(frame)), nil
	}
}

func (s *scriptedFrameStream) WriteFrame(ctx context.Context, data []byte) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.closed:
		return 0, io.EOF
	default:
		return int64(len(data)), nil
	}
}

func (s *scriptedFrameStream) Read(context.Context) (Message, int64, error) {
	return nil, 0, errors.New("unexpected Read")
}

func (s *scriptedFrameStream) Write(ctx context.Context, msg Message) (int64, error) {
	return s.write(ctx, msg)
}

func (s *scriptedFrameStream) write(ctx context.Context, msg Message) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.closed:
		return 0, io.EOF
	case s.writes <- msg:
		s.read.Do(func() {
			s.frames <- s.frame
			close(s.frames)
		})
		return 1, nil
	}
}

func (s *scriptedFrameStream) Close() error {
	s.close.Do(func() { close(s.closed) })
	return nil
}
