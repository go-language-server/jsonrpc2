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
	var gotErr error
	reply := func(ctx context.Context, result any, err error) error {
		gotErr = err
		return nil
	}
	req := NewCall(NewNumberID(1), "missing", nil)
	if err := MethodNotFoundHandler(t.Context(), reply, req); err != nil {
		t.Fatalf("handler returned %v", err)
	}
	if !errors.Is(gotErr, ErrMethodNotFound) {
		t.Fatalf("reply error: got %v want ErrMethodNotFound", gotErr)
	}
}

func TestReplyHandler(t *testing.T) {
	t.Parallel()
	noopReply := func(ctx context.Context, result any, err error) error { return nil }
	req := NewCall(NewNumberID(1), "m", nil)

	tests := map[string]struct {
		inner     Handler
		wantPanic bool
	}{
		"success: replies exactly once": {
			inner: func(ctx context.Context, reply Replier, req Request) error {
				return reply(ctx, nil, nil)
			},
			wantPanic: false,
		},
		"error: never replies": {
			inner: func(ctx context.Context, reply Replier, req Request) error {
				return nil
			},
			wantPanic: true,
		},
		"error: replies twice": {
			inner: func(ctx context.Context, reply Replier, req Request) error {
				_ = reply(ctx, nil, nil)
				return reply(ctx, nil, nil)
			},
			wantPanic: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			h := ReplyHandler(tt.inner)
			defer func() {
				r := recover()
				if tt.wantPanic && r == nil {
					t.Fatal("expected a panic, got none")
				}
				if !tt.wantPanic && r != nil {
					t.Fatalf("unexpected panic: %v", r)
				}
			}()
			_ = h(t.Context(), noopReply, req)
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
