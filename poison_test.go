// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

//go:build jsonrpc2poison

package jsonrpc2

import (
	"context"
	"net"
	"testing"
)

// TestPoisonRetainedRequestFailsLoudly exercises the pooled-request poison
// mode: a handler that illegally retains *Request past its return observes
// either the poison sentinel or the next request's data -- never, silently,
// its own stale request. Poison turns silent reuse into an attributable
// failure on a best-effort basis; the deterministic guard is that the
// original method must no longer be readable.
func TestPoisonRetainedRequestFailsLoudly(t *testing.T) {
	tests := map[string]struct {
		method string
	}{
		"success: retained request reads poison after recycle": {method: "first"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			ca, cb := net.Pipe()
			client := NewConn(NewNDJSONStream(ca))
			server := NewConn(NewNDJSONStream(cb))
			client.Go(ctx, MethodNotFoundHandler)

			retained := make(chan *Request, 1)
			server.Go(ctx, func(ctx context.Context, req *Request) (any, error) {
				// Illegal retention: the pointer must not outlive the handler.
				select {
				case retained <- req:
				default:
				}
				return nil, nil
			})
			defer func() {
				_ = client.Close()
				<-client.Done()
				_ = server.Close()
				<-server.Done()
			}()

			if _, err := client.Call(ctx, tt.method, nil, nil); err != nil {
				t.Fatalf("first Call: %v", err)
			}
			req := <-retained
			// Drive a second request through so the pooled struct is recycled.
			if _, err := client.Call(ctx, "second", nil, nil); err != nil {
				t.Fatalf("second Call: %v", err)
			}

			if got := req.Method(); got != requestPoisonMethod && got != "second" {
				t.Fatalf("retained request Method() = %q, want the poison sentinel (or the recycled request under pool churn); silent stale data means poison mode is broken", got)
			}
			if got := req.Method(); got == tt.method {
				t.Fatalf("retained request still reads its original method %q after recycle; retention was silent", got)
			}
		})
	}
}
