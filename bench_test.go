// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"net"
	"testing"
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
