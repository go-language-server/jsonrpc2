// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestScanPipelineResultResponse(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		frame  string
		id     int64
		result string
	}{
		{
			name:   "object",
			frame:  `{"jsonrpc":"2.0","id":42,"result":{"ok":true}}`,
			id:     42,
			result: `{"ok":true}`,
		},
		{
			name:   "null",
			frame:  `{"jsonrpc":"2.0","id":43,"result":null}`,
			id:     43,
			result: `null`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id, result, ok, err := scanPipelineResultResponse([]byte(tt.frame))
			if err != nil {
				t.Fatalf("scanPipelineResultResponse error: %v", err)
			}
			if !ok {
				t.Fatal("scanPipelineResultResponse ok = false, want true")
			}
			if got, ok := id.Number(); !ok || got != tt.id {
				t.Fatalf("id = %v, %v; want %d, true", got, ok, tt.id)
			}
			if string(result) != tt.result {
				t.Fatalf("result = %q, want %q", result, tt.result)
			}
		})
	}
}

func TestScanPipelineResultResponseFallbacks(t *testing.T) {
	t.Parallel()

	for _, frame := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"missing"}}`),
		[]byte(`{"jsonrpc":"2.0","id":0,"result":null}`),
		[]byte(`{"jsonrpc":"2.0","id":"1","result":null}`),
		[]byte(`{"jsonrpc":"2.0","id":9223372036854775808,"result":null}`),
		[]byte(`{"id":1,"jsonrpc":"2.0","result":null}`),
		[]byte(`[{"jsonrpc":"2.0","id":1,"result":null}]`),
	} {
		if _, _, ok, err := scanPipelineResultResponse(frame); ok || err != nil {
			t.Fatalf("scanPipelineResultResponse(%q) = ok %v, err %v; want fallback", frame, ok, err)
		}
	}
}

func TestScanPipelineResultResponseInvalid(t *testing.T) {
	t.Parallel()

	for _, frame := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"result":`),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":null} trailing`),
	} {
		if _, _, ok, err := scanPipelineResultResponse(frame); ok || err == nil {
			t.Fatalf("scanPipelineResultResponse(%q) = ok %v, err %v; want invalid/parse", frame, ok, err)
		}
	}
}

func TestPipelineClientDeliverBatchResponse(t *testing.T) {
	t.Parallel()

	client := NewPipelineClient(NewNDJSONStream(noopReadWriteCloser{}))
	waiters := []*pipelineWaiter{
		getPipelineWaiter(nil),
		getPipelineWaiter(nil),
	}
	for i, w := range waiters {
		client.calls.Add(NewNumberID(int64(i+1)), w)
	}
	t.Cleanup(func() {
		for _, w := range waiters {
			putPipelineWaiter(w)
		}
	})

	err := client.deliverBatchResponse([]byte(`[
		{"jsonrpc":"2.0","id":1,"result":null},
		{"jsonrpc":"2.0","id":2,"result":{"ok":true}}
	]`))
	if err != nil {
		t.Fatalf("deliverBatchResponse: %v", err)
	}
	for i, w := range waiters {
		select {
		case <-w.ready:
			if w.err != nil {
				t.Fatalf("waiter %d err = %v, want nil", i, w.err)
			}
		default:
			t.Fatalf("waiter %d was not delivered", i)
		}
	}
}

func TestPipelineClientDeliverBatchErrorResponse(t *testing.T) {
	t.Parallel()

	client := NewPipelineClient(NewNDJSONStream(noopReadWriteCloser{}))
	w := getPipelineWaiter(nil)
	client.calls.Add(NewNumberID(1), w)
	t.Cleanup(func() { putPipelineWaiter(w) })

	err := client.deliverBatchResponse([]byte(`[
		{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"missing"}}
	]`))
	if err != nil {
		t.Fatalf("deliverBatchResponse: %v", err)
	}
	select {
	case <-w.ready:
		if !errors.Is(w.err, ErrMethodNotFound) {
			t.Fatalf("waiter err = %v, want %v", w.err, ErrMethodNotFound)
		}
	default:
		t.Fatal("waiter was not delivered")
	}
}

func TestPipelineClientDeliverEmptyBatchResponseInvalid(t *testing.T) {
	t.Parallel()

	client := NewPipelineClient(NewNDJSONStream(noopReadWriteCloser{}))
	w := getPipelineWaiter(nil)
	client.calls.Add(NewNumberID(1), w)

	err := client.deliverBatchResponse([]byte(`[]`))
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("deliverBatchResponse([]) = %v, want %v", err, ErrInvalidRequest)
	}
	select {
	case <-w.ready:
		t.Fatal("empty response batch delivered a pending waiter")
	default:
	}
	if got := client.retireNumberCall(1); got != w {
		t.Fatalf("retireNumberCall returned %p, want %p", got, w)
	}
	putPipelineWaiter(w)
}

func TestPipelineClientQueuedCanceledCallDelivers(t *testing.T) {
	t.Parallel()

	client := NewPipelineClient(NewNDJSONStream(noopReadWriteCloser{}))
	w := getPipelineWaiter(nil)
	client.calls.Add(NewNumberID(1), w)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := client.writeQueuedCallFrames(context.Background(), []pipelineQueuedCall{{
		ctx:    ctx,
		id:     1,
		method: "void",
	}})
	if err != nil {
		t.Fatalf("writeQueuedCallFrames: %v", err)
	}
	select {
	case <-w.ready:
		if !errors.Is(w.err, context.Canceled) {
			t.Fatalf("waiter err = %v, want context.Canceled", w.err)
		}
	default:
		t.Fatal("canceled queued call was not delivered")
	}
	if client.calls.Len() != 0 {
		t.Fatalf("pending calls = %d, want 0", client.calls.Len())
	}
	putPipelineWaiter(w)
}

func TestPipelineClientQueuedCanceledCallNotWritten(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var cc countingConn
		client := NewPipelineClient(NewNDJSONStream(&cc))
		// Keep one earlier call pending and enqueue a second canceled call,
		// mirroring the queued path without involving the global waiter pool:
		// pooled waiter channels may have been created outside this synctest
		// bubble and cannot be selected on from inside it.
		pending := &pipelineWaiter{ready: make(chan struct{}, 1)}
		canceled := &pipelineWaiter{ready: make(chan struct{}, 1)}
		client.calls.Add(NewNumberID(1), pending)
		client.calls.Add(NewNumberID(2), canceled)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		client.enqueueCall(pipelineQueuedCall{ctx: ctx, id: 2, method: "canceled"})
		synctest.Wait()

		if writes := cc.writes.Load(); writes != 0 {
			t.Fatalf("conn.Write calls = %d, want 0 for canceled queued call", writes)
		}
		select {
		case <-canceled.ready:
			if !errors.Is(canceled.err, context.Canceled) {
				t.Fatalf("canceled waiter err = %v, want context.Canceled", canceled.err)
			}
		default:
			t.Fatal("canceled waiter was not delivered")
		}
		if got := client.retireNumberCall(1); got != pending {
			t.Fatalf("retireNumberCall returned %p, want %p", got, pending)
		}
	})
}

func TestPipelineClientConcurrentCalls(t *testing.T) {
	t.Parallel()

	const n = 16
	ca, cb := net.Pipe()
	client := NewPipelineClient(NewNDJSONStream(ca))
	server := NewServer(NewNDJSONStream(cb))

	release := make(chan struct{})
	var releaseOnce sync.Once
	closeRelease := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	seen := make(chan int64, n)
	server.Go(t.Context(), AsyncHandler(func(ctx context.Context, reply Replier, req Request) error {
		call := req.(*Call)
		id, ok := call.ID().Number()
		if !ok {
			t.Errorf("request id = %v, want numeric", call.ID())
		} else {
			seen <- id
		}
		<-release
		return reply(ctx, nil, nil)
	}))
	t.Cleanup(func() {
		closeRelease()
		_ = client.Close()
		<-client.Done()
		_ = server.Close()
		<-server.Done()
	})
	client.Go(t.Context(), MethodNotFoundHandler)

	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_, err := client.Call(t.Context(), "void", nil, nil)
			errs <- err
		}()
	}

	got := map[int64]bool{}
	for range n {
		select {
		case id := <-seen:
			got[id] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %d concurrent calls; saw %d", n, len(got))
		}
	}
	closeRelease()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("PipelineClient.Call: %v", err)
		}
	}
	for id := int64(1); id <= n; id++ {
		if !got[id] {
			t.Fatalf("did not observe id %d in concurrent calls; got %v", id, got)
		}
	}
}

type noopReadWriteCloser struct{}

func (noopReadWriteCloser) Read([]byte) (int, error)    { return 0, errors.New("read unsupported") }
func (noopReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (noopReadWriteCloser) Close() error                { return nil }
