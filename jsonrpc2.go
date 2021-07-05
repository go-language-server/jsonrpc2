// SPDX-FileCopyrightText: 2019 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
)

// Preempter handles messages on a connection before they are queued to the main
// handler.
//
// Primarily this is used for cancel handlers or notifications for which out of
// order processing is not an issue.
type Preempter interface {
	// Preempt is invoked for each incoming request before it is queued.
	// If the request is a call, it must return a value or an error for the reply.
	// Preempt should not block or start any new messages on the connection.
	Preempt(ctx context.Context, req *Request) (interface{}, error)
}

// PreempterFunc type adapts a preempt function to implement the Preempt interface.
type PreempterFunc func(ctx context.Context, req *Request) (interface{}, error)

// Preempt implements Preempter.Preempt.
func (f PreempterFunc) Preempt(ctx context.Context, req *Request) (interface{}, error) {
	return f(ctx, req)
}

// PreempterInterceptor defines a transformation of Preempter.
type PreempterInterceptor func(p Preempter) Preempter

// Handler handles messages on a connection.
type Handler interface {
	// Handle is invoked for each incoming request.
	// If the request is a call, it must return a value or an error for the reply.
	Handle(ctx context.Context, req *Request) (interface{}, error)
}

// HandlerFunc type adapts a handle function to implement the Handler interface.
type HandlerFunc func(ctx context.Context, req *Request) (interface{}, error)

// Handle implements Handler.Handle.
func (f HandlerFunc) Handle(ctx context.Context, req *Request) (interface{}, error) {
	return f(ctx, req)
}

// HandlerInterceptor defines a transformation of Handler.
type HandlerInterceptor func(h Handler) Handler

// noptHandler no-op handler implemented Preempter and Handler interfacess.
type noptHandler struct{}

// Preempt implements Preempter.Preempt.
func (noptHandler) Preempt(context.Context, *Request) (interface{}, error) {
	return nil, ErrNotHandled
}

// Handle implements Handler.Handle.
func (noptHandler) Handle(context.Context, *Request) (interface{}, error) {
	return nil, ErrNotHandled
}
