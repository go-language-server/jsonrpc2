// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"net"
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
		frame, _, err := serverStream.(frameStream).ReadFrame(context.Background())
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

type messageOnlyStream struct{}

func (messageOnlyStream) Read(context.Context) (Message, int64, error) {
	return nil, 0, errors.ErrUnsupported
}

func (messageOnlyStream) Write(context.Context, Message) (int64, error) {
	return 0, errors.ErrUnsupported
}

func (messageOnlyStream) Close() error { return nil }
