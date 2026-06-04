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

	benchmarkMessage Message
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
