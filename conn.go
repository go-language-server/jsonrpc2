// Copyright 2020 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Conn is the common interface to jsonrpc clients and servers.
//
// Conn is bidirectional; it does not have a designated server or client end.
// It manages the jsonrpc2 protocol, connecting responses back to their calls.
type Conn interface {
	// Call invokes the target method and waits for a response.
	// The params will be marshaled to JSON before sending over the wire, and will
	// be handed to the method invoked.
	// The response will be unmarshaled from JSON into the result.
	// The id returned will be unique from this connection, and can be used for
	// logging or tracking.
	Call(ctx context.Context, method string, params, result interface{}) (ID, error)

	// Notify invokes the target method but does not wait for a response.
	// The params will be marshaled to JSON before sending over the wire, and will
	// be handed to the method invoked.
	Notify(ctx context.Context, method string, params interface{}) error

	// Go starts a goroutine to handle the connection.
	// It must be called exactly once for each Conn.
	// It returns immediately.
	// You must block on Done() to wait for the connection to shut down.
	// This is a temporary measure, this should be started automatically in the
	// future.
	Go(ctx context.Context, handler Handler)

	// Close closes the connection and it's underlying stream.
	// It does not wait for the close to complete, use the Done() channel for
	// that.
	Close() error

	// Done returns a channel that will be closed when the processing goroutine
	// has terminated, which will happen if Close() is called or an underlying
	// stream is closed.
	Done() <-chan struct{}

	// Err returns an error if there was one from within the processing goroutine.
	// If err returns non nil, the connection will be already closed or closing.
	Err() error
}

type conn struct {
	seq       int64      // access atomic
	writeMu   sync.Mutex // protects writes to the stream
	stream    Stream
	pendingMu sync.Mutex // protects the pending map
	pending   map[ID]chan *Response

	done chan struct{}
	err  atomic.Value
}

// NewConn creates a new connection object around the supplied stream.
func NewConn(s Stream) Conn {
	conn := &conn{
		stream:  s,
		pending: make(map[ID]chan *Response),
		done:    make(chan struct{}),
	}

	return conn
}

// Notify implemens Conn.
func (c *conn) Notify(ctx context.Context, method string, params interface{}) (err error) {
	notify, err := NewNotification(method, params)
	if err != nil {
		return fmt.Errorf("marshaling notify parameters: %v", err)
	}

	_, err = c.write(ctx, notify)
	return err
}

func (c *conn) replier(req Message) Replier {
	reply := func(ctx context.Context, result interface{}, err error) error {
		call, ok := req.(*Request)
		if !ok {
			// request was a notify, no need to respond
			return nil
		}

		response, err := NewResponse(call.id, result, err)
		if err != nil {
			return err
		}

		_, err = c.write(ctx, response)
		if err != nil {
			// TODO(iancottrell): if a stream write fails, we really need to shut down
			// the whole stream
			return err
		}
		return nil
	}

	return reply
}

func (c *conn) write(ctx context.Context, msg Message) (n int64, err error) {
	c.writeMu.Lock()
	n, err = c.stream.Write(ctx, msg)
	c.writeMu.Unlock()
	return n, err
}

// Go implemens Conn.
func (c *conn) Go(ctx context.Context, handler Handler) {
	go c.run(ctx, handler)
}

func (c *conn) run(ctx context.Context, handler Handler) {
	defer close(c.done)

	for {
		// get the next message
		msg, _, err := c.stream.Read(ctx)
		if err != nil {
			// The stream failed, we cannot continue.
			c.fail(err)
			return
		}

		switch msg := msg.(type) {
		case Requester:
			if err := handler(ctx, c.replier(msg), msg); err != nil {
				// delivery failed, not much we can do
				c.fail(err)
				return
			}

		case *Response:
			// If method is not set, this should be a response, in which case we must
			// have an id to send the response back to the caller.
			c.pendingMu.Lock()
			rchan, ok := c.pending[msg.id]
			c.pendingMu.Unlock()
			if ok {
				rchan <- msg
			}
		}
	}
}

// Close implemens Conn.
func (c *conn) Close() error {
	return c.stream.Close()
}

// Done implemens Conn.
func (c *conn) Done() <-chan struct{} {
	return c.done
}

// Err implemens Conn.
func (c *conn) Err() error {
	if err := c.err.Load(); err != nil {
		return err.(error)
	}
	return nil
}

// fail sets a failure condition on the stream and closes it.
func (c *conn) fail(err error) {
	c.err.Store(err)
	c.stream.Close()
}
