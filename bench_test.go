// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"net"
	"testing"
)

var (
	benchmarkDecodeMinimal = []byte(`{"jsonrpc":"2.0","method":"void","id":1}`)
	benchmarkDecodeMedium  = []byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"textDocument":{"uri":"file:///tmp/example.go"},"position":{"line":123,"character":45}},"id":99}`)

	benchmarkMessage  Message
	benchmarkRequests []*ParsedMessage
	benchmarkView     MessageView
	benchmarkFrame    FrameView
)

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
	tests := []struct {
		name  string
		input []byte
		batch bool
	}{
		{"Call", []byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"textDocument":{"uri":"file:///tmp/example.go"},"position":{"line":123,"character":45}},"id":99}`), false},
		{"Notification", []byte(`{"jsonrpc":"2.0","method":"window/logMessage","params":{"type":3,"message":"hello"}}`), false},
		{"Response", []byte(`{"jsonrpc":"2.0","id":99,"result":{"contents":{"kind":"markdown","value":"hello"}}}`), false},
		{"ErrorResponse", []byte(`{"jsonrpc":"2.0","id":99,"error":{"code":-32602,"message":"invalid params","data":{"field":"position"}}}`), false},
		{"BatchRequests", []byte(`[{"jsonrpc":"2.0","method":"one","id":1},{"jsonrpc":"2.0","method":"two","params":{"x":2},"id":2},{"jsonrpc":"2.0","method":"note","params":[1,2,3]}]`), true},
	}

	for _, tc := range tests {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			if tc.batch {
				for b.Loop() {
					got, err := ParseRequests(tc.input)
					if err != nil {
						b.Fatalf("ParseRequests: %v", err)
					}
					benchmarkRequests = got
				}
				return
			}
			for b.Loop() {
				got, err := DecodeMessage(tc.input)
				if err != nil {
					b.Fatalf("DecodeMessage: %v", err)
				}
				benchmarkMessage = got
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
