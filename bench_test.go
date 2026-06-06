// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"net"
	"testing"
)

var (
	benchmarkDecodeMinimal    = []byte(`{"jsonrpc":"2.0","method":"void","id":1}`)
	benchmarkDecodeMedium     = []byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"textDocument":{"uri":"file:///tmp/example.go"},"position":{"line":123,"character":45}},"id":99}`)
	benchmarkDecodeLarge64KiB = makeBenchmarkLargeCall(64 << 10)
	benchmarkDecodeLarge1MiB  = makeBenchmarkLargeCall(1 << 20)

	benchmarkMessage  Message
	benchmarkRequests []*ParsedMessage
	benchmarkViews    []ParsedMessageView
	benchmarkView     MessageView
	benchmarkFrame    FrameView
	benchmarkEncoded  []byte
)

type benchmarkDecodeEnvelopeCase struct {
	name    string
	input   []byte
	batch   bool
	wantErr bool
}

var benchmarkDecodeEnvelopeCases = []benchmarkDecodeEnvelopeCase{
	{"Call", benchmarkDecodeMedium, false, false},
	{"StringID", []byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"line":1},"id":"abc"}`), false, false},
	{"Notification", []byte(`{"jsonrpc":"2.0","method":"window/logMessage","params":{"type":3,"message":"hello"}}`), false, false},
	{"Response", []byte(`{"jsonrpc":"2.0","id":99,"result":{"contents":{"kind":"markdown","value":"hello"}}}`), false, false},
	{"ErrorResponse", []byte(`{"jsonrpc":"2.0","id":99,"error":{"code":-32602,"message":"invalid params","data":{"field":"position"}}}`), false, false},
	{"EscapedMethodAndID", []byte(`{"jsonrpc":"2.0","method":"textDocument\/hover\n","params":{"method":"nested"},"id":"a\u002fb"}`), false, false},
	{"NestedMethodInParams", []byte(`{"jsonrpc":"2.0","method":"outer","params":{"method":"inner","id":7},"id":1}`), false, false},
	{"DuplicateTopLevelFields", []byte(`{"jsonrpc":"2.0","method":"first","method":"second","params":null,"id":1}`), false, false},
	{"LargeParams64KiB", benchmarkDecodeLarge64KiB, false, false},
	{"LargeParams1MiB", benchmarkDecodeLarge1MiB, false, false},
	{"InvalidJSON", []byte(`{"jsonrpc":"2.0","method":`), false, true},
	{"InvalidJSONRPC", []byte(`{"jsonrpc":"1.0","method":"m","id":1}`), false, true},
	{"BatchRequests", []byte(`[{"jsonrpc":"2.0","method":"one","id":1},{"jsonrpc":"2.0","method":"two","params":{"x":2},"id":2},{"jsonrpc":"2.0","method":"note","params":[1,2,3]}]`), true, false},
	{"MixedBatchRequests", []byte(`[{"jsonrpc":"2.0","method":"one","id":1},{"jsonrpc":"2.0","method":1,"id":2},{"jsonrpc":"2.0","id":3,"result":true},{"jsonrpc":"2.0","method":"note"}]`), true, false},
}

func makeBenchmarkLargeCall(n int) []byte {
	const prefix = `{"jsonrpc":"2.0","method":"large","params":{"text":"`
	const suffix = `"},"id":1}`

	out := make([]byte, 0, len(prefix)+n+len(suffix))
	out = append(out, prefix...)
	for range n {
		out = append(out, 'a')
	}
	out = append(out, suffix...)
	return out
}

// BenchmarkVoidRoundTrip measures the AC-P1 hot path: a call with nil params to
// a handler that replies with a nil result, in the default synchronous dispatch
// mode, over an in-memory net.Pipe. It reports ns/op and allocs/op.
func BenchmarkVoidRoundTrip(b *testing.B) {
	ctx := b.Context()
	ca, cb := net.Pipe()
	client := NewConn(NewNDJSONStream(ca))
	server := NewConn(NewNDJSONStream(cb))
	client.Go(ctx, MethodNotFoundHandler)
	server.Go(ctx, func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, nil, nil)
	})
	defer func() {
		_ = client.Close()
		<-client.Done()
		_ = server.Close()
		<-server.Done()
	}()

	// Warm up one round-trip before timing.
	if _, err := client.Call(ctx, "void", nil, nil); err != nil {
		b.Fatalf("warmup: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := client.Call(ctx, "void", nil, nil); err != nil {
			b.Fatalf("Call: %v", err)
		}
	}
}

func BenchmarkDecodeMinimalLegacy(b *testing.B) {
	msg := benchmarkDecodeMinimal
	b.ReportAllocs()
	for b.Loop() {
		got, err := DecodeMessage(msg)
		if err != nil {
			b.Fatalf("DecodeMessage: %v", err)
		}
		benchmarkMessage = got
	}
}

func BenchmarkDecodeMediumLegacy(b *testing.B) {
	msg := benchmarkDecodeMedium
	b.ReportAllocs()
	for b.Loop() {
		got, err := DecodeMessage(msg)
		if err != nil {
			b.Fatalf("DecodeMessage: %v", err)
		}
		benchmarkMessage = got
	}
}

func BenchmarkDecodeEnvelope(b *testing.B) {
	for _, tc := range benchmarkDecodeEnvelopeCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.input)))
			if tc.batch {
				for b.Loop() {
					got, err := ParseRequests(tc.input)
					if tc.wantErr {
						if err == nil {
							b.Fatalf("ParseRequests succeeded, want error")
						}
						continue
					}
					if err != nil {
						b.Fatalf("ParseRequests: %v", err)
					}
					benchmarkRequests = got
				}
				return
			}
			for b.Loop() {
				got, err := DecodeMessage(tc.input)
				if tc.wantErr {
					if err == nil {
						b.Fatalf("DecodeMessage succeeded, want error")
					}
					benchmarkMessage = nil
					continue
				}
				if err != nil {
					b.Fatalf("DecodeMessage: %v", err)
				}
				benchmarkMessage = got
			}
		})
	}
}

func BenchmarkDecodeViewEnvelope(b *testing.B) {
	for _, tc := range benchmarkDecodeEnvelopeCases {
		if tc.batch {
			continue
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.input)))
			for b.Loop() {
				got, err := ScanMessageView(tc.input)
				if tc.wantErr {
					if err == nil {
						b.Fatalf("ScanMessageView succeeded, want error")
					}
					benchmarkView = MessageView{}
					continue
				}
				if err != nil {
					b.Fatalf("ScanMessageView: %v", err)
				}
				benchmarkView = got
			}
		})
	}
}

func BenchmarkDecodeRequestViewsEnvelope(b *testing.B) {
	for _, tc := range benchmarkDecodeEnvelopeCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.input)))
			for b.Loop() {
				got, err := ScanRequestViews(tc.input)
				if err != nil {
					b.Fatalf("ScanRequestViews: %v", err)
				}
				if tc.wantErr {
					if len(got) == 0 || got[0].Err == nil {
						b.Fatalf("ScanRequestViews succeeded, want per-entry error")
					}
					benchmarkViews = got
					continue
				}
				benchmarkViews = got
			}
		})
	}
}

func BenchmarkDecodeAppendRequestViewsEnvelope(b *testing.B) {
	for _, tc := range benchmarkDecodeEnvelopeCases {
		b.Run(tc.name, func(b *testing.B) {
			var views []ParsedMessageView
			if tc.batch {
				views = make([]ParsedMessageView, 0, 8)
			} else {
				views = make([]ParsedMessageView, 0, 1)
			}

			b.ReportAllocs()
			b.SetBytes(int64(len(tc.input)))
			for b.Loop() {
				got, err := AppendRequestViews(views[:0], tc.input)
				if err != nil {
					b.Fatalf("AppendRequestViews: %v", err)
				}
				if tc.wantErr {
					if len(got) == 0 || got[0].Err == nil {
						b.Fatalf("AppendRequestViews succeeded, want per-entry error")
					}
					benchmarkViews = got
					continue
				}
				benchmarkViews = got
			}
		})
	}
}

func BenchmarkDecodeMinimalFastView(b *testing.B) {
	msg := benchmarkDecodeMinimal
	b.ReportAllocs()
	for b.Loop() {
		got, err := ScanMessageView(msg)
		if err != nil {
			b.Fatalf("ScanMessageView: %v", err)
		}
		benchmarkView = got
	}
}

func BenchmarkAppendEnvelope(b *testing.B) {
	call := NewCall(NewNumberID(1), "textDocument/hover", RawMessage(`{"line":1}`))
	batch := []Message{
		call,
		NewNotification("$/progress", RawMessage(`{"token":1}`)),
		NewResponse(NewNumberID(1), RawMessage(`{"ok":true}`), nil),
	}

	b.Run("EncodeMessage/Call", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			got, err := EncodeMessage(call)
			if err != nil {
				b.Fatalf("EncodeMessage: %v", err)
			}
			benchmarkEncoded = got
		}
	})
	b.Run("AppendMessage/Call", func(b *testing.B) {
		buf := make([]byte, 0, 128)
		b.ReportAllocs()
		for b.Loop() {
			benchmarkEncoded = AppendMessage(buf[:0], call)
		}
	})
	b.Run("AppendCall/Call", func(b *testing.B) {
		buf := make([]byte, 0, 128)
		b.ReportAllocs()
		for b.Loop() {
			benchmarkEncoded = AppendCall(buf[:0], call.ID(), call.Method(), call.Params())
		}
	})
	b.Run("AppendResponse/Result", func(b *testing.B) {
		buf := make([]byte, 0, 128)
		b.ReportAllocs()
		for b.Loop() {
			benchmarkEncoded = AppendResponse(buf[:0], NewNumberID(1), RawMessage(`{"ok":true}`), nil)
		}
	})
	b.Run("AppendBatch/Mixed", func(b *testing.B) {
		buf := make([]byte, 0, 384)
		b.ReportAllocs()
		for b.Loop() {
			benchmarkEncoded = AppendBatch(buf[:0], batch)
		}
	})
}

func BenchmarkDecodeMediumFastView(b *testing.B) {
	msg := benchmarkDecodeMedium
	b.ReportAllocs()
	for b.Loop() {
		got, err := ScanMessageView(msg)
		if err != nil {
			b.Fatalf("ScanMessageView: %v", err)
		}
		benchmarkView = got
	}
}

func BenchmarkDecodeMinimalView(b *testing.B) {
	msg := benchmarkDecodeMinimal
	b.ReportAllocs()
	for b.Loop() {
		got, err := ScanFrameView(msg)
		if err != nil {
			b.Fatalf("ScanFrameView: %v", err)
		}
		benchmarkFrame = got
	}
}

func BenchmarkDecodeMediumView(b *testing.B) {
	msg := benchmarkDecodeMedium
	b.ReportAllocs()
	for b.Loop() {
		got, err := ScanFrameView(msg)
		if err != nil {
			b.Fatalf("ScanFrameView: %v", err)
		}
		benchmarkFrame = got
	}
}

// BenchmarkSyncClientVoidRoundTrip measures the A1c synchronous-client mode: the
// caller owns the read loop (no background reader goroutine on the client side),
// so a void round trip collapses the dedicated-reader-to-Call hand-off that
// BenchmarkVoidRoundTrip pays. The server is an ordinary Conn with a background
// reader, so this is a real RPC over a real net.Pipe transport. It is a distinct
// mode (no concurrent calls, no server-initiated requests), reported separately
// from the concurrent Conn round trip.
func BenchmarkSyncClientVoidRoundTrip(b *testing.B) {
	ctx := b.Context()
	ca, cb := net.Pipe()
	client, err := NewSyncClient(NewNDJSONStream(ca))
	if err != nil {
		b.Fatalf("NewSyncClient: %v", err)
	}
	server := NewConn(NewNDJSONStream(cb))
	server.Go(ctx, func(ctx context.Context, reply Replier, req Request) error {
		return reply(ctx, nil, nil)
	})
	defer func() {
		_ = client.Close()
		_ = server.Close()
		<-server.Done()
	}()

	if _, err := client.Call(ctx, "void", nil, nil); err != nil {
		b.Fatalf("warmup: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := client.Call(ctx, "void", nil, nil); err != nil {
			b.Fatalf("Call: %v", err)
		}
	}
}

func BenchmarkOutgoingCallSlotsAddTake(b *testing.B) {
	waiters := makeBenchmarkWaiters(256)
	b.ReportAllocs()
	for b.Loop() {
		var slots outgoingCallSlots
		for i, w := range waiters {
			slots.Add(NewNumberID(int64(i+1)), w)
		}
		for i := range waiters {
			if _, ok := slots.Take(NewNumberID(int64(i + 1))); !ok {
				b.Fatalf("Take(%d) failed", i+1)
			}
		}
	}
}

func BenchmarkDenseCallSlotsAddTake(b *testing.B) {
	waiters := makeBenchmarkWaiters(256)
	b.ReportAllocs()
	for b.Loop() {
		var slots denseCallSlots
		for i, w := range waiters {
			slots.Add(NewNumberID(int64(i+1)), w)
		}
		for i := range waiters {
			if _, ok := slots.Take(NewNumberID(int64(i + 1))); !ok {
				b.Fatalf("Take(%d) failed", i+1)
			}
		}
	}
}

func makeBenchmarkWaiters(n int) []*waiter {
	waiters := make([]*waiter, n)
	for i := range waiters {
		waiters[i] = &waiter{ready: make(chan *Response, 1)}
	}
	return waiters
}
