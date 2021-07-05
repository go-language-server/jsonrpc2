// SPDX-FileCopyrightText: 2021 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/segmentio/encoding/json"

	"go.lsp.dev/pkg/event"
)

// Binder builds a connection.
//
// This may be used in servers to generate a new configuration per connection.
//
// Conn itself implements Binder returning itself unmodified, to
// allow for the simple cases where no per connection information is needed.
type Binder interface {
	// Bind is invoked when creating a new connection.
	// The connection is not ready to use when Bind is called.
	Bind(ctx context.Context, conn *Connection) (Conn, error)
}

// BinderFunc type adapts a bind function to implement the Binder interface.
type BinderFunc func(ctx context.Context, conn *Connection) (Conn, error)

// Bind implements Binder.Bind.
func (f BinderFunc) Bind(ctx context.Context, conn *Connection) (Conn, error) {
	return f(ctx, conn)
}

// BinderInterceptor defines a transformation of jsonrpc2 Binders, that may be
// composed to build jsonrpc2 servers.
type BinderInterceptor func(b Binder) Binder

// Conn holds the new connections.
type Conn struct {
	// Framer allows control over the message framing and encoding.
	// If nil, HeaderFramer will be used.
	Framer Framer

	// Preempter allows registration of a pre-queue message handler.
	// If nil, no messages will be preempted.
	Preempter Preempter

	// Handler is used as the queued message handler for inbound messages.
	// If nil, all responses will be ErrNotHandled.
	Handler Handler
}

// Bind returns the unmodified conn.
func (c Conn) Bind(context.Context, *Connection) (Conn, error) {
	return c, nil
}

type asyncResult struct {
	err    error
	result []byte
}

type AsyncRequest struct {
	id       ID
	response chan *Response // the channel a response will be delivered on
	result   chan asyncResult
	ctx      context.Context
}

// ID used for this call.
//
// This can be used to cancel the call if needed.
func (ar *AsyncRequest) ID() ID { return ar.id }

// IsReady can be used to check if the result is already prepared.
//
// This is guaranteed to return true on a result for which Await has already
// returned, or a call that failed to send in the first place.
func (ar *AsyncRequest) IsReady() bool {
	select {
	case r := <-ar.result:
		ar.result <- r

		return true

	default:
		return false
	}
}

// Await async wait the results of a Request.
//
// The response will be unmarshaled from JSON into the result.
func (ar *AsyncRequest) Await(ctx context.Context, result interface{}) error {
	var r asyncResult
	select {
	case resp := <-ar.response:
		// response just arrived, prepare the result
		switch {
		case resp.Error != nil:
			r.err = resp.Error

		default:
			r.result = resp.Result
		}

	case r = <-ar.result:
		// result already available

	case <-ctx.Done():
		return ctx.Err()
	}

	// refill the box for the next caller
	ar.result <- r

	// and unpack the result
	if r.err != nil {
		return r.err
	}

	if result == nil || len(r.result) == 0 {
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(r.result))
	dec.ZeroCopy()
	if err := dec.Decode(result); err != nil {
		return fmt.Errorf("failed ot decode result: %w", err)
	}

	return nil
}

// incoming is used to track an incoming request as it is being handled.
type incoming struct {
	request   *Request        // the request being processed
	baseCtx   context.Context // a base context for the message processing
	handleCtx context.Context // the context for handling the message, child of baseCtx
	cancel    func()          // a function that cancels the handling context
}

// Connection manages the JSON-RPC protocol, connecting responses back to their
// calls.
//
// Connection is bidirectional, it does not have a designated server or client
// end.
type Connection struct {
	closer    io.Closer
	async     async
	writers   chan Writer
	outgoings chan map[ID]chan<- *Response
	incomings chan map[ID]*incoming
	seq       int64 // must only be accessed using atomic operations
}

// newConnection creates a new connection and runs it.
// This is used by the Dial and Serve functions to build the actual connection.
func newConnection(ctx context.Context, rwc io.ReadWriteCloser, binder Binder) (*Connection, error) {
	c := &Connection{
		closer:    rwc,
		writers:   make(chan Writer, 1),
		outgoings: make(chan map[ID]chan<- *Response, 1),
		incomings: make(chan map[ID]*incoming, 1),
	}

	conn, err := binder.Bind(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to invoked Bind: %w", err)
	}
	if conn.Framer == nil {
		conn.Framer = HeaderFramer()
	}
	if conn.Preempter == nil {
		conn.Preempter = noptHandler{}
	}
	if conn.Handler == nil {
		conn.Handler = noptHandler{}
	}

	c.outgoings <- make(map[ID]chan<- *Response)
	c.incomings <- make(map[ID]*incoming)
	c.async.init()

	// the goroutines started here will continue until the underlying stream is closed
	reader := conn.Framer.Reader(rwc)
	readToQueue := make(chan *incoming)
	queueToDeliver := make(chan *incoming)

	go c.readIncoming(ctx, reader, readToQueue)
	go c.manageQueue(ctx, conn.Preempter, readToQueue, queueToDeliver)
	go c.deliverMessages(ctx, conn.Handler, queueToDeliver)

	// releaseing the writer must be the last thing do in case any requests
	// are blocked waiting for the connection to be ready
	c.writers <- conn.Framer.Writer(rwc)

	return c, nil
}

// readIncoming collects inbound messages from the reader and delivers them, either responding
// to outgoing calls or feeding requests to the queue.
func (c *Connection) readIncoming(ctx context.Context, reader Reader, toQueue chan<- *incoming) {
	defer close(toQueue)

	for {
		// get the next message
		// no lock is needed, this is the only reader
		msg, _, err := reader.Read(ctx)
		if err != nil {
			// The stream failed, cannot continue
			c.async.setError(err)

			return
		}

		switch msg := msg.(type) {
		case *Request:
			entry := &incoming{
				request: msg,
			}

			entry.baseCtx = ctx

			// in theory notifications cannot be cancelled, but build them a cancel context anyway
			entry.handleCtx, entry.cancel = context.WithCancel(entry.baseCtx)

			// if the request is a call, add it to the incoming map so it can be
			// cancelled by id
			if msg.IsCall() {
				pending := <-c.incomings
				c.incomings <- pending
				pending[msg.ID] = entry
			}

			// send the message to the incoming queue
			toQueue <- entry

		case *Response:
			// If method is not set, this should be a response. in which case, must
			// have an id to send the response back to the caller.
			c.incomingResponse(msg)
		}
	}
}

func (c *Connection) incomingResponse(msg *Response) {
	pending := <-c.outgoings
	response, ok := pending[msg.ID]
	if ok {
		delete(pending, msg.ID)
	}

	c.outgoings <- pending
	if response != nil {
		response <- msg
	}
}

// manageQueue reads incoming requests, attempts to proceses them with the preempter, or queue them
// up for normal handling.
func (c *Connection) manageQueue(_ context.Context, preempter Preempter, fromRead <-chan *incoming, toDeliver chan<- *incoming) {
	defer close(toDeliver)

	q := []*incoming{}
	ok := true
	for {
		var nextReq *incoming
		if len(q) == 0 {
			// no messages in the queue
			// if q is closed, then done.
			if !ok {
				return
			}

			// not closing, but nothing in the queue, so just block waiting for a read
			nextReq, ok = <-fromRead
		} else {
			// have a non empty queue, so pick whichever of reading or delivering
			// that can make progress on
			select {
			case nextReq, ok = <-fromRead:

			case toDeliver <- q[0]:
				// TODO(zchee): this causes a lot of shuffling, should use a growing ring buffer? compaction?
				q = q[1:]
			}
		}

		if nextReq != nil {
			// TODO(zchee): should allow to limit the queue size?
			var result interface{}
			rerr := nextReq.handleCtx.Err()
			if rerr == nil {
				// only preempt if not already cancelled
				result, rerr = preempter.Preempt(nextReq.handleCtx, nextReq.request)
			}

			switch {
			case errors.Is(rerr, ErrNotHandled):
				// message not handled, add it to the queue for the main handler
				q = append(q, nextReq)

			case errors.Is(rerr, ErrAsyncResponse):
				// message handled but the response will come later

			default:
				// anything else means the message is fully handled
				c.reply(nextReq, result, rerr)
			}
		}
	}
}

func (c *Connection) deliverMessages(_ context.Context, handler Handler, fromQueue <-chan *incoming) {
	defer c.async.done()

	for entry := range fromQueue {
		// cancel any messages in the queue that have a pending cancel for
		var result interface{}
		rerr := entry.handleCtx.Err()
		if rerr == nil {
			// only deliver if not already cancelled
			result, rerr = handler.Handle(entry.handleCtx, entry.request)
		}

		switch {
		case errors.Is(rerr, ErrNotHandled):
			// message not handled, report it back to the caller as an error
			c.reply(entry, nil, fmt.Errorf("%w: %q", ErrMethodNotFound, entry.request.Method))

		case errors.Is(rerr, ErrAsyncResponse):
			// message handled but the response will come later

		default:
			c.reply(entry, result, rerr)
		}
	}
}

// reply is used to reply to an incoming request that has just been handled.
func (c *Connection) reply(entry *incoming, result interface{}, rerr error) {
	if entry.request.IsCall() {
		// have a call finishing, remove it from the incoming map
		pending := <-c.incomings
		defer func() { c.incomings <- pending }()

		delete(pending, entry.request.ID)
	}

	if err := c.response(entry, result, rerr); err != nil {
		// no way to propagate this error
		// TODO(zchee): should do more than just log it?
		event.Error(entry.baseCtx, "jsonrpc2 message delivery failed", err)
	}
}

// response sends a response.
// This is the code shared between reply and SendResponse.
func (c *Connection) response(entry *incoming, result interface{}, rerr error) (err error) {
	if entry.request.IsCall() {
		// send the response
		if result == nil && rerr == nil {
			// call with no response, send an error anyway
			rerr = fmt.Errorf("%w: %q produced no response", ErrInternal, entry.request.Method)
		}

		var response *Response
		response, err = NewResponse(entry.request.ID, result, rerr)
		if err == nil {
			// write the response with the base context, in case the message was cancelled
			err = c.write(entry.baseCtx, response)
		}
	} else {
		switch {
		case rerr != nil:
			// notification failed
			//nolint: errorlint
			err = fmt.Errorf("%w: %q notification failed: %v", ErrInternal, entry.request.Method, rerr)

		case result != nil:
			// notification produced a response, which is an error
			err = fmt.Errorf("%w: %q produced unwanted response", ErrInternal, entry.request.Method)

		default:
			// normal notification finish
		}
	}

	// and just to be clean, invoke and clear the cancel if needed
	if entry.cancel != nil {
		entry.cancel()
		entry.cancel = nil
	}

	if err != nil {
		return fmt.Errorf("response: %w", err)
	}

	return nil
}

// write is used by all things that write outgoing messages, including replies.
// it makes sure that writes are atomic.
func (c *Connection) write(ctx context.Context, msg Message) error {
	writer := <-c.writers
	defer func() { c.writers <- writer }()

	if _, err := writer.Write(ctx, msg); err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}

	return nil
}

// Notify invokes the target method but does not wait for a response.
//
// The params will be marshaled to JSON before sending over the wire, and will
// be handed to the method invoked.
func (c *Connection) Notify(ctx context.Context, method string, params interface{}) error {
	notify, err := NewNotification(method, params)
	if err != nil {
		return fmt.Errorf("marshaling notify parameters: %w", err)
	}

	if err := c.write(ctx, notify); err != nil {
		return fmt.Errorf("failed to write notification: %w", err)
	}

	return nil
}

// Request invokes the target method and returns an object that can be used to await the response.
//
// The params will be marshaled to JSON before sending over the wire, and will
// be handed to the method invoked.
//
// Do not have to wait for the response, it can just be ignored if not needed.
// If sending the call failed, the response will be ready and have the error in it.
func (c *Connection) Request(ctx context.Context, method string, params interface{}) *AsyncRequest {
	r := &AsyncRequest{
		id:     Int64ID(atomic.AddInt64(&c.seq, 1)),
		result: make(chan asyncResult, 1),
	}

	// generate a new request identifier
	req, err := NewRequest(r.id, method, params)
	if err != nil {
		// set the result to failed
		r.result <- asyncResult{err: fmt.Errorf("marshaling call parameters: %w", err)}

		return r
	}

	r.ctx = ctx
	// have to add ourselves to the pending map before send, otherwise racing the response.
	// rchan is buffered in case the response arrives without a listener.
	r.response = make(chan *Response, 1)
	pending := <-c.outgoings
	pending[r.id] = r.response
	c.outgoings <- pending

	// now ready to send
	if err := c.write(ctx, req); err != nil {
		// sending failed, will never get a response, so deliver a fake one
		resp, _ := NewResponse(r.id, nil, err) //nolint:errcheck
		c.incomingResponse(resp)
	}

	return r
}

// Response deliverers a response to an incoming Call.
//
// It is an error to not call this exactly once for any message for which a
// handler has previously returned ErrAsyncResponse. It is also an error to
// call this for any other message.
func (c *Connection) Response(id ID, result interface{}, rerr error) error {
	pending := <-c.incomings
	defer func() { c.incomings <- pending }()

	entry, found := pending[id]
	if !found {
		return nil
	}
	delete(pending, id)

	return c.response(entry, result, rerr)
}

// Cancel is used to cancel an inbound message by ID, it does not cancel
// outgoing messages.
//
// This is only used inside a message handler that is layering a
// cancellation protocol on top of JSON-RPC 2.0.
//
// It will not complain if the ID is not a currently active message, and it will
// not cause any messages that have not arrived yet with that ID to be
// cancelled.
func (c *Connection) Cancel(id ID) {
	pending := <-c.incomings
	defer func() { c.incomings <- pending }()

	if entry, found := pending[id]; found && entry.cancel != nil {
		entry.cancel()
		entry.cancel = nil
	}
}

// Wait blocks until the connection is fully closed, but does not close it.
func (c *Connection) Wait() error {
	return c.async.wait()
}

// Close can be used to close the underlying stream, and then wait for the connection to
// fully shut down.
//
// This does not cancel in flight requests, but waits for them to gracefully complete.
func (c *Connection) Close() error {
	// close the underlying stream
	if err := c.closer.Close(); err != nil && !IsClosingError(err) {
		return fmt.Errorf("close connection: %w", err)
	}

	// and then wait for it to cause the connection to close
	if err := c.Wait(); err != nil && !IsClosingError(err) {
		return err
	}

	return nil
}
