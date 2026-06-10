// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestSingleClientModeRoundTrip(t *testing.T) {
	t.Parallel()

	ca, cb := net.Pipe()
	client, err := NewSingleClient(NewNDJSONStream(ca))
	if err != nil {
		t.Fatalf("NewSingleClient: %v", err)
	}
	server := NewServer(NewNDJSONStream(cb))
	server.Go(t.Context(), func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, RawMessage(`{"mode":"single"}`), nil)
	})
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-server.Done()
	})

	var got RawMessage
	if _, err := client.Call(t.Context(), "mode", nil, &got); err != nil {
		t.Fatalf("SingleClient.Call: %v", err)
	}
	if string(got) != `{"mode":"single"}` {
		t.Fatalf("SingleClient result = %q, want single mode result", got)
	}
}

func TestPipelineClientModeBridgeRoundTrip(t *testing.T) {
	t.Parallel()

	ca, cb := net.Pipe()
	client := NewPipelineClient(NewNDJSONStream(ca))
	server := NewServer(NewNDJSONStream(cb))
	client.Go(t.Context(), MethodNotFoundHandler)
	server.Go(t.Context(), func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, RawMessage(`{"mode":"pipeline"}`), nil)
	})
	t.Cleanup(func() {
		_ = client.Close()
		<-client.Done()
		_ = server.Close()
		<-server.Done()
	})

	var got RawMessage
	if _, err := client.Call(t.Context(), "mode", nil, &got); err != nil {
		t.Fatalf("PipelineClient.Call: %v", err)
	}
	if string(got) != `{"mode":"pipeline"}` {
		t.Fatalf("PipelineClient result = %q, want pipeline mode result", got)
	}
}

func TestPipelineClientErrorResponse(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	ca, cb := net.Pipe()
	client := NewPipelineClient(NewNDJSONStream(ca))
	server := NewServer(NewNDJSONStream(cb))
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, nil, ErrMethodNotFound)
	})
	t.Cleanup(func() {
		closeBoth(t, client, server)
	})

	if _, err := client.Call(ctx, "missing", nil, nil); !errors.Is(err, ErrMethodNotFound) {
		t.Fatalf("PipelineClient.Call error = %v, want %v", err, ErrMethodNotFound)
	}
}

func TestPipelineClientNotify(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	ca, cb := net.Pipe()
	client := NewPipelineClient(NewNDJSONStream(ca))
	server := NewServer(NewNDJSONStream(cb))
	gotc := make(chan string, 1)
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, func(ctx context.Context, reply Replier, req Request) error {
		gotc <- req.Method()
		return nil
	})
	t.Cleanup(func() {
		closeBoth(t, client, server)
	})

	if err := client.Notify(ctx, "window/logMessage", nil); err != nil {
		t.Fatalf("PipelineClient.Notify: %v", err)
	}
	select {
	case got := <-gotc:
		if got != "window/logMessage" {
			t.Fatalf("PipelineClient.Notify method = %q, want window/logMessage", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient.Notify did not reach server")
	}
}

func TestPipelineClientContextCancelStopsWaiting(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	ca, cb := net.Pipe()
	client := NewPipelineClient(NewNDJSONStream(ca))
	server := NewServer(NewNDJSONStream(cb))
	release := make(chan struct{})
	started := make(chan struct{})
	var releaseOnce sync.Once
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, AsyncHandler(func(ctx context.Context, reply Replier, req Request) error {
		close(started)
		<-release
		return reply(ctx, nil, nil)
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		closeBoth(t, client, server)
	})

	callCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		_, err := client.Call(callCtx, "slow", nil, nil)
		errc <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient slow call did not reach server")
	}

	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PipelineClient.Call after cancel: got %v want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient.Call did not return after ctx cancel")
	}
}

func TestPipelineClientCloseAbortsInFlightCall(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	ca, cb := net.Pipe()
	client := NewPipelineClient(NewNDJSONStream(ca))
	server := NewServer(NewNDJSONStream(cb))
	release := make(chan struct{})
	started := make(chan struct{})
	var releaseOnce sync.Once
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, AsyncHandler(func(ctx context.Context, reply Replier, req Request) error {
		close(started)
		<-release
		return reply(ctx, nil, nil)
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		_ = server.Close()
		<-server.Done()
	})

	errc := make(chan error, 1)
	go func() {
		_, err := client.Call(ctx, "slow", nil, nil)
		errc <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient slow call did not reach server")
	}

	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("PipelineClient.Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient.Close did not return")
	}
	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient.Done did not close")
	}
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClientClosing) {
			t.Fatalf("PipelineClient.Call after Close = %v, want %v", err, ErrClientClosing)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient.Call did not return after Close")
	}
	releaseOnce.Do(func() { close(release) })
}

func TestPipelineClientCanceledCallLateResponseDoesNotReachLaterCall(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	ca, cb := net.Pipe()
	client := NewPipelineClient(NewNDJSONStream(ca))
	server := NewServer(NewNDJSONStream(cb))

	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	slowReplied := make(chan error, 1)
	var slowReleaseOnce sync.Once
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
	t.Cleanup(func() {
		slowReleaseOnce.Do(func() { close(slowRelease) })
		closeBoth(t, client, server)
	})

	callCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() {
		_, err := client.Call(callCtx, "slow", nil, nil)
		errc <- err
	}()

	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient slow call did not reach server")
	}
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PipelineClient canceled Call error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient canceled Call did not return")
	}

	var got string
	if _, err := client.Call(ctx, "fast", nil, &got); err != nil {
		t.Fatalf("PipelineClient second Call: %v", err)
	}
	if got != "fast" {
		t.Fatalf("PipelineClient second Call result = %q, want fast", got)
	}

	slowReleaseOnce.Do(func() { close(slowRelease) })
	select {
	case err := <-slowReplied:
		if err != nil {
			t.Fatalf("PipelineClient late reply: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PipelineClient late reply did not flush")
	}

	got = ""
	if _, err := client.Call(ctx, "fast", nil, &got); err != nil {
		t.Fatalf("PipelineClient third Call after late response: %v", err)
	}
	if got != "fast" {
		t.Fatalf("PipelineClient third Call result = %q, want fast", got)
	}
}

func TestPeerAndServerModeBidirectionalCall(t *testing.T) {
	t.Parallel()

	ca, cb := net.Pipe()
	peer := NewPeer(NewNDJSONStream(ca))
	server := NewServer(NewNDJSONStream(cb))
	peer.Go(t.Context(), func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, RawMessage(`"peer-answer"`), nil)
	})
	server.Go(t.Context(), func(ctx context.Context, reply Replier, req Request) error {
		Async(ctx)
		var got RawMessage
		if _, err := server.Call(ctx, "askPeer", nil, &got); err != nil {
			return reply(ctx, nil, err)
		}
		return reply(ctx, got, nil)
	})
	t.Cleanup(func() {
		_ = peer.Close()
		<-peer.Done()
		_ = server.Close()
		<-server.Done()
	})

	var got RawMessage
	if _, err := peer.Call(t.Context(), "askServer", nil, &got); err != nil {
		t.Fatalf("Peer.Call: %v", err)
	}
	if string(got) != `"peer-answer"` {
		t.Fatalf("bidirectional result = %q, want peer answer", got)
	}
}

func TestBatchClientRawFrame(t *testing.T) {
	t.Parallel()

	ca, cb := net.Pipe()
	client, err := NewBatchClient(NewNDJSONStream(ca))
	if err != nil {
		t.Fatalf("NewBatchClient: %v", err)
	}
	serverStream := NewNDJSONStream(cb)
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverStream.Close()
	})

	readc := make(chan []byte, 1)
	errc := make(chan error, 1)
	go func() {
		frame, _, err := serverStream.(frameStream).ReadFrame(t.Context())
		if err != nil {
			errc <- err
			return
		}
		readc <- frame
	}()

	const batch = `[{"jsonrpc":"2.0","method":"a","id":1},{"jsonrpc":"2.0","method":"b"}]`
	if _, err := client.WriteFrame(t.Context(), []byte(batch)); err != nil {
		t.Fatalf("BatchClient.WriteFrame: %v", err)
	}

	select {
	case got := <-readc:
		if string(got) != batch {
			t.Fatalf("batch frame = %q, want %q", got, batch)
		}
	case err := <-errc:
		t.Fatalf("server ReadFrame: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for batch frame")
	}
}

func TestBatchClientRejectsMessageOnlyStream(t *testing.T) {
	t.Parallel()

	_, err := NewBatchClient(messageOnlyStream{})
	if !errors.Is(err, errFrameStreamRequired) {
		t.Fatalf("NewBatchClient error = %v, want %v", err, errFrameStreamRequired)
	}
}

func TestDenseCallSlotsSequentialAndOutOfOrder(t *testing.T) {
	t.Parallel()

	var slots denseCallSlots
	w1 := newSlotTestWaiter()
	w2 := newSlotTestWaiter()
	w3 := newSlotTestWaiter()
	slots.Add(NewNumberID(1), w1)
	slots.Add(NewNumberID(2), w2)
	slots.Add(NewNumberID(3), w3)

	if got, ok := slots.Take(NewNumberID(2)); !ok || got != w2 {
		t.Fatalf("Take(2) = %p, %v; want %p, true", got, ok, w2)
	}
	if slots.Len() != 2 {
		t.Fatalf("Len after out-of-order take = %d, want 2", slots.Len())
	}
	if got, ok := slots.Take(NewNumberID(1)); !ok || got != w1 {
		t.Fatalf("Take(1) = %p, %v; want %p, true", got, ok, w1)
	}
	if got, ok := slots.Take(NewNumberID(3)); !ok || got != w3 {
		t.Fatalf("Take(3) = %p, %v; want %p, true", got, ok, w3)
	}
	if slots.Len() != 0 {
		t.Fatalf("Len after drain-by-take = %d, want 0", slots.Len())
	}
}

func TestDenseCallSlotsGrowAndDrain(t *testing.T) {
	t.Parallel()

	var slots denseCallSlots
	const n = initialOutgoingCallSlots + 3
	waiters := make(map[int64]*waiter, n)
	for id := int64(1); id <= n; id++ {
		w := newSlotTestWaiter()
		waiters[id] = w
		slots.Add(NewNumberID(id), w)
	}
	if slots.Len() != len(waiters) {
		t.Fatalf("Len after Add = %d, want %d", slots.Len(), len(waiters))
	}

	got := make(map[int64]*waiter, n)
	slots.Drain(func(id ID, w *waiter) {
		n, _ := id.Number()
		got[n] = w
	})
	if slots.Len() != 0 {
		t.Fatalf("Len after Drain = %d, want 0", slots.Len())
	}
	for id, want := range waiters {
		if got[id] != want {
			t.Fatalf("Drain[%d] = %p, want %p", id, got[id], want)
		}
	}
}

func TestDenseCallSlotsOutOfOrderRegistration(t *testing.T) {
	t.Parallel()

	var slots denseCallSlots
	w9 := newSlotTestWaiter()
	w8 := newSlotTestWaiter()
	slots.Add(NewNumberID(9), w9)
	slots.Add(NewNumberID(8), w8)

	if got, ok := slots.Take(NewNumberID(8)); !ok || got != w8 {
		t.Fatalf("Take(8) = %p, %v; want %p, true", got, ok, w8)
	}
	if got, ok := slots.Take(NewNumberID(9)); !ok || got != w9 {
		t.Fatalf("Take(9) = %p, %v; want %p, true", got, ok, w9)
	}
}

func TestDenseCallSlotsReuseAfterEmptyWithMonotonicIDs(t *testing.T) {
	t.Parallel()

	var slots denseCallSlots
	for id := int64(1); id <= initialOutgoingCallSlots*4; id++ {
		w := newSlotTestWaiter()
		slots.Add(NewNumberID(id), w)
		if got, ok := slots.Take(NewNumberID(id)); !ok || got != w {
			t.Fatalf("Take(%d) = %p, %v; want %p, true", id, got, ok, w)
		}
		if slots.Len() != 0 {
			t.Fatalf("Len after Take(%d) = %d, want 0", id, slots.Len())
		}
		if len(slots.slots) != initialOutgoingCallSlots {
			t.Fatalf("slot capacity after Take(%d) = %d, want %d", id, len(slots.slots), initialOutgoingCallSlots)
		}
	}
}

type messageOnlyStream struct{}

func (messageOnlyStream) Read(context.Context) (Message, int64, error) {
	return nil, 0, errors.ErrUnsupported
}

func (messageOnlyStream) Write(context.Context, Message) (int64, error) {
	return 0, errors.ErrUnsupported
}

func (messageOnlyStream) Close() error { return nil }
