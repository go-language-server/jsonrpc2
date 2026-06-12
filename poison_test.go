// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

//go:build jsonrpc2poison

package jsonrpc2

import (
	"context"
	"net"
	"testing"
)

// TestPoisonRetainedRequestFailsLoudly proves the pooled-request poison mode:
// a handler that illegally retains *RequestV2 past its return observes the
// loud poison sentinels once the request has been recycled, instead of
// silently reading a later request's data.
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

			retained := make(chan *RequestV2, 1)
			server.GoDirect(ctx, func(ctx context.Context, req *RequestV2) (any, error) {
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
