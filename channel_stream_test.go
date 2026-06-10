// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestChannelStreamPairRoundTrip(t *testing.T) {
	ctx := t.Context()
	clientStream, serverStream := NewChannelStreamPair(1)
	client := NewConn(clientStream)
	server := NewConn(serverStream)
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, RawMessage(`{"ok":true}`), nil)
	})
	defer func() {
		_ = client.Close()
		<-client.Done()
		_ = server.Close()
		<-server.Done()
	}()

	var got RawMessage
	if _, err := client.Call(ctx, "ok", nil, &got); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("result = %s, want {\"ok\":true}", got)
	}
}

func TestChannelStreamWriteFrameOwnsQueuedBytes(t *testing.T) {
	ctx := t.Context()
	a, b := NewChannelStreamPair(1)
	frameWriter := a.(frameStream)
	frameReader := b.(frameStream)

	frame := []byte(`{"jsonrpc":"2.0","method":"before"}`)
	if _, err := frameWriter.WriteFrame(ctx, frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	copy(frame, []byte(`{"jsonrpc":"2.0","method":"after!"}`))

	got, _, err := frameReader.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	want := `{"jsonrpc":"2.0","method":"before"}`
	if string(got) != want {
		t.Fatalf("queued frame = %q, want %q", got, want)
	}
}

func TestChannelStreamCloseUnblocksWrite(t *testing.T) {
	a, b := NewChannelStreamPair(0)
	defer b.Close()
	writeDone := make(chan error, 1)
	go func() {
		_, err := a.(frameStream).WriteFrame(context.Background(), []byte(`{"jsonrpc":"2.0","method":"blocked"}`))
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		t.Fatalf("WriteFrame returned before close: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-writeDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("WriteFrame after close = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WriteFrame did not unblock after Close")
	}
}

func TestChannelStreamCloseUnblocksRead(t *testing.T) {
	a, b := NewChannelStreamPair(0)
	defer a.Close()
	readDone := make(chan error, 1)
	go func() {
		_, _, err := a.(frameStream).ReadFrame(context.Background())
		readDone <- err
	}()

	select {
	case err := <-readDone:
		t.Fatalf("ReadFrame returned before close: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-readDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("ReadFrame after close = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadFrame did not unblock after Close")
	}
}

func TestChannelStreamWriteFrameAfterCloseFails(t *testing.T) {
	for range 100 {
		a, b := NewChannelStreamPair(1)
		if err := b.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		_, err := a.(frameStream).WriteFrame(t.Context(), []byte(`{"jsonrpc":"2.0","method":"afterClose"}`))
		if !errors.Is(err, io.EOF) {
			t.Fatalf("WriteFrame after close = %v, want io.EOF", err)
		}
	}
}

func TestChannelStreamReadFrameAfterCloseFails(t *testing.T) {
	for range 100 {
		a, b := NewChannelStreamPair(1)
		if _, err := a.(frameStream).WriteFrame(t.Context(), []byte(`{"jsonrpc":"2.0","method":"queued"}`)); err != nil {
			t.Fatalf("WriteFrame before close: %v", err)
		}
		if err := a.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		got, _, err := b.(frameStream).ReadFrame(t.Context())
		if !errors.Is(err, io.EOF) {
			t.Fatalf("ReadFrame after close = frame %q, err %v; want io.EOF", got, err)
		}
	}
}

func TestChannelStreamPairRejectsNegativeCapacity(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewChannelStreamPair(-1) did not panic")
		}
	}()
	NewChannelStreamPair(-1)
}
