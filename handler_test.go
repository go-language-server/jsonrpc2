// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"testing"
)

func TestMethodNotFoundHandler(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		req     Request
		wantErr bool
	}{
		"error: call is answered with ErrMethodNotFound": {
			req:     Request{id: NewNumberID(1), method: "missing", isCall: true},
			wantErr: true,
		},
		"success: notification is dropped without failing the connection": {
			req: Request{method: "missing"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result, err := MethodNotFoundHandler(t.Context(), &tt.req)
			if result != nil {
				t.Fatalf("result = %v, want nil", result)
			}
			if tt.wantErr && !errors.Is(err, ErrMethodNotFound) {
				t.Fatalf("err = %v, want ErrMethodNotFound", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("err = %v, want nil for a notification", err)
			}
		})
	}
}

func TestCancelHandlerCancellerUnknownID(t *testing.T) {
	t.Parallel()
	// A canceller for an id that is not being handled must be a safe no-op.
	_, canceller := CancelHandler(MethodNotFoundHandler)
	canceller(NewNumberID(999))
	canceller(NewStringID("nope"))
}

func TestAsyncOnNonRequestContextIsNoop(t *testing.T) {
	t.Parallel()
	// Calling Async on a context without a release token must not panic.
	Async(t.Context())
}

func TestAsyncDoubleCallPanics(t *testing.T) {
	t.Parallel()
	rel := &releaser{ch: make(chan struct{})}
	ctx := context.WithValue(t.Context(), asyncKey{}, rel)
	Async(ctx) // first hard release
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic on a second Async call")
		}
	}()
	Async(ctx) // second hard release must panic
}
