// Copyright 2020 The Go Language Server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import "context"

// Handler is the interface used to hook into the message handling of an rpc
// connection.
type Handler interface {
	// Deliver is invoked to handle incoming requests.
	// If the request returns false from IsNotify then the Handler must eventually
	// call Reply on the Conn with the supplied request.
	// Handlers are called synchronously, they should pass the work off to a go
	// routine if they are going to take a long time.
	// If Deliver returns true all subsequent handlers will be invoked with
	// delivered set to true, and should not attempt to deliver the message.
	Deliver(ctx context.Context, r *Request, delivered bool) bool

	// Cancel is invoked for canceled outgoing requests.
	// It is okay to use the connection to send notifications, but the context will
	// be in the canceled state, so you must do it with the background context
	// instead.
	// If Cancel returns true all subsequent handlers will be invoked with
	// canceled set to true, and should not attempt to cancel the message.
	Cancel(ctx context.Context, conn *Conn, id ID, canceled bool) bool

	// Log is invoked for all messages flowing through a Conn.
	// direction indicates if the message being received or sent
	// id is the message id, if not set it was a notification
	// elapsed is the time between a call being seen and the response, and is
	// negative for anything that is not a response.
	// method is the method name specified in the message
	// payload is the parameters for a call or notification, and the result for a
	// response

	// Request is called near the start of processing any request.
	Request(ctx context.Context, conn *Conn, direction Direction, r *WireRequest) context.Context

	// Response is called near the start of processing any response.
	Response(ctx context.Context, conn *Conn, direction Direction, r *WireResponse) context.Context

	// Done is called when any request is fully processed.
	// For calls, this means the response has also been processed, for notifies
	// this is as soon as the message has been written to the stream.
	// If err is set, it implies the request failed.
	Done(ctx context.Context, err error)

	// Read is called with a count each time some data is read from the stream.
	// The read calls are delayed until after the data has been interpreted so
	// that it can be attributed to a request/response.
	Read(ctx context.Context, n int64) context.Context

	// Write is called each time some data is written to the stream.
	Write(ctx context.Context, n int64) context.Context

	// Error is called with errors that cannot be delivered through the normal
	// mechanisms, for instance a failure to process a notify cannot be delivered
	// back to the other party.
	Error(ctx context.Context, err error)
}

// Direction is used to indicate to a logger whether the logged message was being
// sent or received.
type Direction bool

const (
	// Send indicates the message is outgoing.
	Send = Direction(true)
	// Receive indicates the message is incoming.
	Receive = Direction(false)
)

func (d Direction) String() string {
	switch d {
	case Send:
		return "send"
	case Receive:
		return "receive"
	default:
		panic("unreachable")
	}
}

// EmptyHandler represents a empty handler which implements Handler interface.
type EmptyHandler struct{}

// compile time check whether the emptyHandler implements Handler interface.
var _ Handler = (*EmptyHandler)(nil)

func (EmptyHandler) Deliver(ctx context.Context, r *Request, delivered bool) bool {
	if delivered {
		return false
	}
	if !r.IsNotify() {
		if err := r.Reply(ctx, nil, Errorf(MethodNotFound, "method %s not found", r.Method)); err != nil {
			return false
		}
	}
	return true
}

// Cancel implements Handler interface.
func (EmptyHandler) Cancel(context.Context, *Conn, ID, bool) bool { return false }

// Request implements Handler interface.
func (EmptyHandler) Request(ctx context.Context, _ *Conn, _ Direction, _ *WireRequest) context.Context {
	return ctx
}

// Response implements Handler interface.
func (EmptyHandler) Response(ctx context.Context, _ *Conn, _ Direction, _ *WireResponse) context.Context {
	return ctx
}

// Done implements Handler interface.
func (EmptyHandler) Done(context.Context, error) {}

// Read implements Handler interface.
func (EmptyHandler) Read(ctx context.Context, _ int64) context.Context { return ctx }

// Write implements Handler interface.
func (EmptyHandler) Write(ctx context.Context, _ int64) context.Context { return ctx }

// Error implements Handler interface.
func (EmptyHandler) Error(context.Context, error) {}
