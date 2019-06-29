// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package jsonrpc2 is an implementation of the JSON-RPC 2 specification for Go.
package jsonrpc2

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

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

	Notify(ctx context.Context, method string, params interface{}) (err error)

	Cancel(id ID)

	Run(ctx context.Context) (err error)
}

// Handler is an option you can pass to NewConn to handle incoming requests.
// If the request returns false from IsNotify then the Handler must eventually
// call Reply on the Conn with the supplied request.
// Handlers are called synchronously, they should pass the work off to a go
// routine if they are going to take a long time.
type Handler func(context.Context, *Request)

// Canceler is an option you can pass to NewConn which is invoked for
// canceled outgoing requests.
// The request will have the ID filled in, which can be used to propagate the
// cancel to the other process if needed.
// It is okay to use the connection to send notifications, but the context will
// be in the canceled state, so you must do it with the background context
// instead.
type Canceler func(context.Context, *Conn, ID)

type handling struct {
	request *Request
	cancel  context.CancelFunc
	start   time.Time
}

// Conn is a JSON RPC 2 client server connection.
// Conn is bidirectional; it does not have a designated server or client end.
type Conn struct {
	seq                *atomic.Int64 // must only be accessed using atomic operations
	Handler            Handler
	Canceler           Canceler
	Logger             *zap.Logger
	Capacity           int
	RejectIfOverloaded bool
	stream             Stream
	err                error
	pending            map[ID]chan *Response
	pendingMu          sync.Mutex // protects the pending map
	handling           map[ID]*Request
	handlingMu         sync.Mutex // protects the handling map
}

var _ Interface = (*Conn)(nil)

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

// WithLogger apply custom Logger to Conn.
func WithLogger(logger *zap.Logger) Options {
	return func(c *Conn) {
		c.Logger = logger
	}
}

// WithCapacity apply custom capacity to Conn.
func WithCapacity(capacity int) Options {
	return func(c *Conn) {
		c.Capacity = capacity
	}
}

// WithOverloaded apply RejectIfOverloaded boolean to Conn.
func WithOverloaded(rejectIfOverloaded bool) Options {
	return func(c *Conn) {
		c.RejectIfOverloaded = rejectIfOverloaded
	}
}

type requestState int

const (
	requestWaiting = requestState(iota)
	requestSerial
	requestParallel
	requestReplied
	requestDone
)

// Request is sent to a server to represent a Call or Notify operaton.
type Request struct {
	conn        *Conn
	cancel      context.CancelFunc
	start       time.Time
	state       requestState
	nextRequest chan struct{}

	// Method is a string containing the method name to invoke.
	Method string
	// Params is either a struct or an array with the parameters of the method.
	Params *json.RawMessage
	// The id of this request, used to tie the response back to the request.
	// Will be either a string or a number. If not set, the request is a notify,
	// and no response is possible.
	ID *ID
}

var defaultHandler = func(ctx context.Context, req *Request) {
	if req.IsNotify() {
		req.Reply(ctx, req, Errorf(MethodNotFound, "method %q not found", req.Method))
	}
}

var defaultCanceler = func(context.Context, *Conn, ID) {}

var defaultLogger = zap.NewNop()

// NewConn creates a new connection object that reads and writes messages from
// the supplied stream and dispatches incoming messages to the supplied handler.
func NewConn(s Stream, options ...Options) *Conn {
	conn := &Conn{
		seq:      new(atomic.Int64),
		stream:   s,
		pending:  make(map[ID]chan *Response),
		handling: make(map[ID]*Request),
	}

	for _, opt := range options {
		opt(conn)
	}

	// the default handler reports a method error
	if conn.Handler == nil {
		conn.Handler = defaultHandler
	}
	// the default canceller does nothing
	if conn.Canceler == nil {
		conn.Canceler = defaultCanceler
	}
	// the default Logger does nothing
	if conn.Logger == nil {
		conn.Logger = defaultLogger
	}

	return conn
}

// Cancel cancels a pending Call on the server side.
func (c *Conn) Cancel(id ID) {
	c.Logger.Debug("Cancel")
	c.handlingMu.Lock()
	handling, found := c.handling[id]
	c.handlingMu.Unlock()

	if found {
		handling.cancel()
	}
}

// Notify is called to send a notification request over the connection.
func (c *Conn) Notify(ctx context.Context, method string, params interface{}) error {
	c.Logger.Debug("Notify", zap.String("method", method), zap.Any("params", params))
	p, err := marshalInterface(params)
	if err != nil {
		return Errorf(ParseError, "failed to marshaling notify parameters: %v", err)
	}

	req := &NotificationMessage{
		JSONRPC: Version,
		Method:  method,
		Params:  p,
	}
	data, err := json.Marshal(req) // TODO(zchee): use gojay
	if err != nil {
		return Errorf(ParseError, "failed to marshaling notify request: %v", err)
	}

	c.Logger.Debug(Send,
		zap.String("req.Method", req.Method),
		zap.Any("req.Params", req.Params),
	)

	err = c.stream.Write(ctx, data)
	if err != nil {
		return Errorf(InternalError, "failed to write notify request data to steam: %v", err)
	}

	return nil
}

// Call sends a request over the connection and then waits for a response.
func (c *Conn) Call(ctx context.Context, method string, params, result interface{}) error {
	c.Logger.Debug("Call", zap.String("method", method), zap.Any("params", params))
	p, err := marshalInterface(params)
	if err != nil {
		return Errorf(ParseError, "failed to marshaling call parameters: %v", err)
	}

	id := ID{Number: c.seq.Add(1)}
	req := &request{
		JSONRPC: Version,
		ID:      &id,
		Method:  method,
		Params:  p,
	}

	// marshal the request now it is complete
	data, err := json.Marshal(req) // TODO(zchee): use gojay
	if err != nil {
		return Errorf(ParseError, "failed to marshaling call request: %v", err)
	}

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

	c.Logger.Debug(Send,
		zap.String("req.JSONRPC", req.JSONRPC),
		zap.String("id", id.String()),
		zap.String("req.method", req.Method),
		zap.Any("req.params", req.Params),
	)

	if err := c.stream.Write(ctx, data); err != nil {
		return Errorf(InternalError, "failed to write call request data to steam: %v", err)
	}

	// wait for the response
	select {
	case resp := <-rchan:
		elapsed := time.Since(start)
		c.Logger.Debug(Receive,
			zap.Stringer("resp.ID", resp.ID),
			zap.Duration("elapsed", elapsed),
			zap.String("req.Method", req.Method),
			zap.Any("resp.Result", resp.Result),
			zap.Any("resp.Error", resp.Error),
		)

		// is it an error response?
		if resp.Error != nil {
			return resp.Error
		}

		if result == nil || resp.Result == nil {
			return nil
		}

		// TODO(zchee): use gojay
		if err := json.Unmarshal(*resp.Result, result); err != nil {
			return Errorf(ParseError, "failed to unmarshalling result: %v", err)
		}

		return nil

	case <-ctx.Done():
		// allow the handler to propagate the cancel
		c.Canceler(ctx, c, id)

		return ctx.Err()
	}
}

// Conn returns the connection that created this request.
func (r *Request) Conn() *Conn { return r.conn }

// IsNotify returns true if this request is a notification.
func (r *Request) IsNotify() bool {
	return r.ID == nil
}

// Parallel indicates that the system is now allowed to process other requests
// in parallel with this one.
// It is safe to call any number of times, but must only be called from the
// request handling go routine.
// It is implied by both reply and by the handler returning.
func (r *Request) Parallel() {
	if r.state >= requestParallel {
		return
	}
	r.state = requestParallel
	close(r.nextRequest)
}

// Reply sends a reply to the given request.
func (r *Request) Reply(ctx context.Context, result interface{}, err error) error {
	r.conn.Logger.Debug("Reply")
	if r.state >= requestReplied {
		return fmt.Errorf("reply invoked more than once")
	}

	if r.IsNotify() {
		return NewError(InvalidRequest, "reply not invoked with a valid call")
	}

	// reply ends the handling phase of a call, so if we are not yet
	// parallel we should be now. The go routine is allowed to continue
	// to do work after replying, which is why it is important to unlock
	// the rpc system at this point.
	r.Parallel()
	r.state = requestReplied

	elapsed := time.Since(r.start)

	var raw *json.RawMessage
	if err == nil {
		raw, err = marshalInterface(result)
	}

	resp := &Response{
		JSONRPC: Version,
		Result:  raw,
		ID:      r.ID,
	}

	if err != nil {
		if callErr, ok := err.(*Error); ok {
			resp.Error = callErr
		} else {
			resp.Error = Errorf(0, "%s", err)
		}
	}

	data, err := json.Marshal(resp) // TODO(zchee): use gojay
	if err != nil {
		r.conn.Logger.Error(Send,
			zap.String("resp.ID", resp.ID.String()),
			zap.Duration("elapsed", elapsed),
			zap.String("r.Method", r.Method),
			zap.Any("resp.Result", resp.Result),
			zap.Error(err),
		)
		return Errorf(ParseError, "failed to marshaling reply response: %v", err)
	}

	r.conn.Logger.Debug(Send,
		zap.String("resp.ID", resp.ID.String()),
		zap.String("r.Method", r.Method),
		zap.Any("resp.Result", resp.Result),
	)

	if err := r.conn.stream.Write(ctx, data); err != nil {
		// TODO(iancottrell): if a stream write fails, we really need to shut down
		// the whole stream
		return Errorf(InternalError, "failed to write response data to steam: %v", err)
	}

	return nil
}

func (c *Conn) setHandling(r *Request, active bool) {
	if r.ID == nil {
		return
	}
	r.conn.handlingMu.Lock()
	defer r.conn.handlingMu.Unlock()
	if active {
		r.conn.handling[*r.ID] = r
	} else {
		delete(r.conn.handling, *r.ID)
	}
}

// Run blocks until the connection is terminated, and returns any error that
// caused the termination.
//
// It must be called exactly once for each Conn.
// It returns only when the reader is closed or there is an error in the stream.
func (c *Conn) Run(ctx context.Context) (err error) {
	// we need to make the next request "lock" in an unlocked state to allow
	// the first incoming request to proceed. All later requests are unlocked
	// by the preceding request going to parallel mode.
	nextReq := make(chan struct{})
	close(nextReq)

	for {
		data, err := c.stream.Read(ctx) // get the data for a message
		if err != nil {
			return err // read the stream failed, cannot continue
		}

		msg := new(Combined)
		if err := json.Unmarshal(data, msg); err != nil { // TODO(zchee): use gojay
			// a badly formed message arrived, log it and continue
			// we trust the stream to have isolated the error to just this message
			c.Logger.Debug(Receive,
				zap.Error(Errorf(ParseError, "unmarshal failed: %v", err)),
			)
			continue
		}

		// work out which kind of message we have
		switch {
		case msg.Method != "": // handle the Request because msg.Method not empty
			reqCtx, cancelReq := context.WithCancel(ctx)
			currentReq := nextReq
			nextReq = make(chan struct{})
			req := &Request{
				conn:        c,
				cancel:      cancelReq,
				nextRequest: nextReq,
				start:       time.Now(),
				Method:      msg.Method,
				Params:      msg.Params,
				ID:          msg.ID,
			}

			c.setHandling(req, true)

			go func() {
				<-currentReq
				req.state = requestSerial
				defer func() {
					c.setHandling(req, false)
					if !req.IsNotify() && req.state < requestReplied {
						req.Reply(reqCtx, nil, Errorf(InternalError, "method %q did not reply", req.Method))
					}
					req.Parallel()
					cancelReq()
				}()

				c.Logger.Debug(Receive,
					zap.Stringer("req.ID", req.ID),
					zap.String("req.Method", req.Method),
					zap.Any("req.Params", req.Params),
				)
				c.Handler(reqCtx, req)
			}()

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
				Result:  msg.Result,
				Error:   msg.Error,
				ID:      msg.ID,
			}
			rchan <- resp
			close(rchan) // for the range channel loop

		default:
			c.Logger.Warn(Receive, zap.Error(NewError(InvalidParams, "ignoring because message not a call, notify or response")))
		}
	}
}

// marshalInterface marshal obj to RawMessage.
// TODO(zchee): use gojay
func marshalInterface(obj interface{}) (*json.RawMessage, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(data)

	return &raw, nil
}
