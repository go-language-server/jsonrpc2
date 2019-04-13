// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"sync"
	"time"

	"github.com/francoispqt/gojay"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

const (
	// Send indicates the message is outgoing.
	Send = "send"
	// Receive indicates the message is incoming.
	Receive = "receive"
)

// Interface represents an interface for issuing requests that speak the JSON-RPC 2 protocol.
type Interface interface {
	Call(ctx context.Context, method string, params, result interface{}) (err error)

	Reply(ctx context.Context, req *Request, result interface{}, err error) error

	Notify(ctx context.Context, method string, params interface{}) (err error)

	Cancel(id ID)

	Run(ctx context.Context) (err error)
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
	Handler    Handler
	Canceler   Canceler
	logger     *zap.Logger
	capacity   int
	overloaded bool
	stream     Stream
	done       chan struct{}
	err        error
	seq        atomic.Int64 // must only be accessed using atomic operations
	pendingMu  sync.Mutex   // protects the pending map
	pending    map[ID]chan *Response
	handlingMu sync.Mutex // protects the handling map
	handling   map[ID]handling
}

var _ Interface = (*Conn)(nil)

type handling struct {
	request *Request
	cancel  context.CancelFunc
	start   time.Time
}

type queueEntry struct {
	ctx     context.Context
	conn    *Conn
	request *Request
}

// Options represents a functional options.
type Options func(*Conn)

// WithHandler apply custom hander to Conn.
func WithHandler(h Handler) Options {
	return func(c *Conn) {
		c.Handler = h
	}
}

// WithCanceler apply custom canceler to Conn.
func WithCanceler(canceler Canceler) Options {
	return func(c *Conn) {
		c.Canceler = canceler
	}
}

// WithLogger apply custom logger to Conn.
func WithLogger(logger *zap.Logger) Options {
	return func(c *Conn) {
		c.logger = logger
	}
}

// WithCapacity apply custom capacity to Conn.
func WithCapacity(capacity int) Options {
	return func(c *Conn) {
		c.capacity = capacity
	}
}

// WithOverloaded apply overloaded boolean to Conn.
func WithOverloaded(overloaded bool) Options {
	return func(c *Conn) {
		c.overloaded = overloaded
	}
}

var defaultHandler = func(ctx context.Context, c *Conn, r *Request) {
	if r.IsNotify() {
		c.Reply(ctx, r, nil, Errorf(CodeMethodNotFound, "method %q not found", r.Method))
	}
}

var defaultCanceler = func(context.Context, *Conn, *Request) {}

// NewConn creates a new connection object that reads and writes messages from
// the supplied stream and dispatches incoming messages to the supplied handler.
func NewConn(ctx context.Context, s Stream, options ...Options) *Conn {
	conn := &Conn{
		Handler:  defaultHandler,  // the default handler reports a method error
		Canceler: defaultCanceler, // the default canceller does nothing
		logger:   zap.NewNop(),    // the default logger does nothing
		stream:   s,
		pending:  make(map[ID]chan *Response),
		handling: make(map[ID]handling),
	}

	for _, opt := range options {
		opt(conn)
	}

	return conn
}

// Cancel cancels a pending Call on the server side.
func (c *Conn) Cancel(id ID) {
	c.handlingMu.Lock()
	handling, found := c.handling[id]
	c.handlingMu.Unlock()

	if found {
		handling.cancel()
	}
}

// Notify is called to send a notification request over the connection.
func (c *Conn) Notify(ctx context.Context, method string, params interface{}) error {
	p, err := c.marshalInterface(params)
	if err != nil {
		return Errorf(CodeParseError, "failed to marshaling notify parameters: %w", err)
	}

	req := &NotificationMessage{
		JSONRPC: Version,
		Method:  method,
		Params:  p,
	}
	data, err := gojay.MarshalJSONObject(req)
	if err != nil {
		return Errorf(CodeParseError, "failed to marshaling notify request: %w", err)
	}

	c.logger.Debug(Send,
		zap.String("req.Method", req.Method),
		zap.Any("req.Params", req.Params),
	)

	err = c.stream.Write(ctx, data)
	if err != nil {
		return Errorf(CodeInternalError, "failed to write notify request data to steam: %w", err)
	}

	return nil
}

// Call sends a request over the connection and then waits for a response.
func (c *Conn) Call(ctx context.Context, method string, params, result interface{}) error {
	p, err := c.marshalInterface(params)
	if err != nil {
		return Errorf(CodeParseError, "failed to marshaling call parameters: %w", err)
	}

	id := ID{Number: c.seq.Add(1)}
	req := &Request{
		JSONRPC: Version,
		ID:      &id,
		Method:  method,
		Params:  p,
	}

	// marshal the request now it is complete
	data, err := gojay.MarshalJSONObject(req)
	if err != nil {
		return Errorf(CodeParseError, "failed to marshaling call request: %w", err)
	}
	c.logger.Debug("gojay.MarshalJSONObject(req)", zap.ByteString("data", data))

	rchan := make(chan *Response)

	c.pendingMu.Lock()
	c.pending[id] = rchan
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	start := time.Now()
	c.logger.Debug(Send,
		zap.String("req.JSONRPC", req.JSONRPC),
		zap.String("id", id.String()),
		zap.String("req.method", req.Method),
		zap.Any("req.params", req.Params),
	)

	if err := c.stream.Write(ctx, data); err != nil {
		return Errorf(CodeInternalError, "failed to write call request data to steam: %w", err)
	}

	select {
	case resp := <-rchan:
		c.logger.Debug(Receive,
			zap.String("req.JSONRPC", req.JSONRPC),
			zap.String("id", id.String()),
			zap.Duration("elapsed", time.Since(start)),
			zap.String("req.method", req.Method),
			zap.Any("resp.Result", resp.Result),
			zap.Error(resp.Error),
		)

		if resp.Error != nil {
			return resp.Error
		}
		if result == nil || resp.Result == nil {
			return nil
		}

		if err := gojay.Unsafe.Unmarshal(*resp.Result, result); err != nil {
			return Errorf(CodeParseError, "failed to unmarshalling result: %w", err)
		}

		return nil

	case <-ctx.Done():
		c.Canceler(ctx, c, req)

		return ctx.Err()
	}
}

// Reply sends a reply to the given request.
func (c *Conn) Reply(ctx context.Context, req *Request, result interface{}, err error) error {
	if req.IsNotify() {
		return NewError(CodeInvalidRequest, "reply not invoked with a valid call")
	}

	c.handlingMu.Lock()
	handling, found := c.handling[*req.ID]
	if found {
		delete(c.handling, *req.ID)
	}
	c.handlingMu.Unlock()
	if !found {
		return Errorf(CodeInternalError, "not a call in progress: %w", req.ID)
	}

	elapsed := time.Since(handling.start)

	resp := &Response{
		JSONRPC: Version,
		ID:      req.ID,
	}

	if err == nil {
		if resp.Result, err = c.marshalInterface(result); err != nil {
			return err
		}
	} else {
		resp.Error = NewError(CodeParseError, err)
	}

	data, err := gojay.MarshalJSONObject(resp)
	if err != nil {
		c.logger.Error(Send,
			zap.String("resp.ID", resp.ID.String()),
			zap.Duration("elapsed", elapsed),
			zap.String("req.Method", req.Method),
			zap.Any("resp.Result", resp.Result),
			zap.Error(err),
		)
		return Errorf(CodeParseError, "failed to marshaling reply response: %w", err)
	}

	c.logger.Debug(Send,
		zap.String("resp.ID", resp.ID.String()),
		zap.Duration("elapsed", elapsed),
		zap.String("req.Method", req.Method),
		zap.Any("resp.Result", resp.Result),
		zap.Error(resp.Error),
	)

	if err := c.stream.Write(ctx, data); err != nil {
		return Errorf(CodeInternalError, "failed to write response data to steam: %w", err)
	}

	return nil
}

func (c *Conn) deliver(ctx context.Context, q chan queueEntry, request *Request) bool {
	e := queueEntry{
		ctx:     ctx,
		conn:    c,
		request: request,
	}

	if !c.overloaded {
		q <- e
		return true
	}

	select {
	case q <- e:
		return true
	default:
		return false
	}
}

// Run run the jsonrpc2 server.
func (c *Conn) Run(ctx context.Context) (err error) {
	q := make(chan queueEntry, c.capacity)
	defer close(q)

	// start the queue processor
	go func() {
		for e := range q {
			if e.ctx.Err() != nil {
				continue
			}
			c.Handler(e.ctx, e.conn, e.request)
		}
	}()

	for {
		data, err := c.stream.Read(ctx) // get the data for a message
		if err != nil {
			return err // read the stream failed, cannot continue
		}

		c.logger.Debug(Receive, zap.ByteString("data", data), zap.Int("len(data)", len(data)))
		// if len(data) == 0 {
		// 	continue // stream is empty, continue
		// }

		// read a combined message
		msg := &Combined{}
		if err := gojay.Unsafe.UnmarshalJSONObject(data, msg); err != nil {
			// a badly formed message arrived, log it and continue
			// we trust the stream to have isolated the error to just this message
			c.logger.Debug(Receive,
				zap.Error(Errorf(CodeParseError, "unmarshal failed: %v", err)),
			)
			continue
		}

		// work out which kind of message we have
		switch {
		case msg.Method != "": // handle the Request because msg.Method not empty
			req := &Request{
				JSONRPC: Version,
				ID:      msg.ID,
				Method:  msg.Method,
				Params:  msg.Params,
			}

			if req.IsNotify() {
				// handle the Notify because msg.ID is nil
				c.logger.Debug(Receive,
					zap.String("req.ID", req.ID.String()),
					zap.String("req.Method", req.Method),
					zap.Any("req.Params", req.Params),
				)
				// add to the processor queue
				c.deliver(ctx, q, req)
				// TODO: log when we drop a message?
			} else {
				// handle the Call, add to the processor queue.
				ctxReq, cancelReq := context.WithCancel(ctx)
				c.handlingMu.Lock()
				c.handling[*req.ID] = handling{
					request: req,
					cancel:  cancelReq,
					start:   time.Now(),
				}
				c.handlingMu.Unlock()
				c.logger.Debug(Receive,
					zap.String("req.ID", req.ID.String()),
					zap.String("req.Method", req.Method),
					zap.Any("req.Params", req.Params),
				)

				if !c.deliver(ctxReq, q, req) {
					// queue is full, reject the message by directly replying
					c.Reply(ctx, req, nil, Errorf(CodeServerOverloaded, "no room in queue"))
				}
			}

		case msg.ID != nil: // handle the response
			// get the pending entry from the map
			c.pendingMu.Lock()
			rchan := c.pending[*msg.ID]
			if rchan != nil {
				delete(c.pending, *msg.ID)
			}
			c.pendingMu.Unlock()

			// send the reply to the channel
			resp := &Response{
				JSONRPC: Version,
				ID:      msg.ID,
				Result:  msg.Result,
				Error:   msg.Error,
			}
			rchan <- resp
			close(rchan) // for the range channel loop

		default:
			c.logger.Warn(Receive, zap.Error(NewError(CodeInvalidParams, "ignoring because message not a call, notify or response")))
		}
	}
}

// marshalInterface marshal obj to RawMessage.
func (c *Conn) marshalInterface(obj interface{}) (*RawMessage, error) {
	data, err := gojay.MarshalAny(obj)
	if err != nil {
		return nil, err
	}
	msg := RawMessage(gojay.EmbeddedJSON(data))
	c.logger.Debug("marshalInterface", zap.String("msg", msg.String()))

	return &msg, nil
}
