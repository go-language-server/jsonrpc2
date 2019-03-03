// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"sync"
)

type Interface interface {
	Call(ctx context.Context, method string, params, result interface{}) error

	Reply(ctx context.Context, req *Request, result interface{}, err error) error

	Notify(ctx context.Context, method string, params interface{}) error

	Cancel(id ID)

	Wait(ctx context.Context) error
}

// Handler is an option you can pass to NewConn to handle incoming requests.
// If the request returns false from IsNotify then the Handler must eventually
// call Reply on the Conn with the supplied request.
// Handlers are called synchronously, they should pass the work off to a go
// routine if they are going to take a long time.
type Handler func(context.Context, *Conn, *Request)

// Canceler is an option you can pass to NewConn which is invoked for
// canceled outgoing requests.
// The request will have the ID filled in, which can be used to propagate the
// cancel to the other process if needed.
// It is okay to use the connection to send notifications, but the context will
// be in the canceled state, so you must do it with the background context
// instead.
type Canceler func(context.Context, *Conn, *Request)

// Conn is a JSON RPC 2 client server connection.
// Conn is bidirectional; it does not have a designated server or client end.
type Conn struct {
	handle    Handler
	cancel    Canceler
	stream    Stream
	done      chan struct{}
	err       error
	seq       int64      // must only be accessed using atomic operations
	pendingMu sync.Mutex // protects the pending map
	pending   map[ID]chan *Response
}

var _ Interface = (*Conn)(nil)

type Options func(*Conn)

func WithHandler(h Handler) Options {
	return func(c *Conn) {
		c.handle = h
	}
}

func WithCanceler(cancel Canceler) Options {
	return func(c *Conn) {
		c.cancel = cancel
	}
}

// NewConn creates a new connection object that reads and writes messages from
// the supplied stream and dispatches incoming messages to the supplied handler.
func NewConn(ctx context.Context, s Stream, options ...Options) *Conn {
	conn := &Conn{
		stream:  s,
		done:    make(chan struct{}),
		pending: make(map[ID]chan *Response),
	}
	for _, opt := range options {
		opt(conn)
	}

	if conn.handle == nil {
		// the default handler reports a method error
		conn.handle = func(ctx context.Context, c *Conn, r *Request) {
			if r.IsNotify() {
				c.Reply(ctx, r, nil, NewErrorf(MethodNotFound, "method %q not found", r.Method))
			}
		}
	}
	if conn.cancel == nil {
		// the default canceller does nothing
		conn.cancel = func(context.Context, *Conn, *Request) {}
	}

	go func() {
		conn.err = conn.run(ctx)
		close(conn.done)
	}()

	return conn
}

func (c *Conn) run(ctx context.Context) error { return nil }

func (c *Conn) Call(ctx context.Context, method string, params, result interface{}) error { return nil }

func (c *Conn) Reply(ctx context.Context, req *Request, result interface{}, err error) error {
	return nil
}

func (c *Conn) Notify(ctx context.Context, method string, params interface{}) error { return nil }

func (c *Conn) Cancel(id ID) {}

func (c *Conn) Wait(ctx context.Context) error { return nil }
