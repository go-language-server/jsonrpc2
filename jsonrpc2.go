// Copyright 2019 The Go Language Server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// Interface represents an interface for issuing requests that speak the JSON-RPC 2 protocol.
type Interface interface {
	Cancel(id ID)
	Notify(ctx context.Context, method string, params interface{}) (err error)
	Call(ctx context.Context, method string, params, result interface{}) (err error)
	Run(ctx context.Context) (err error)
}

// Conn is a JSON-RPC 2 client server connection.
// Conn is bidirectional; it does not have a designated server or client end.
type Conn struct {
	seq        int64 // must only be accessed using atomic operations
	handlers   []Handler
	stream     Stream
	pending    map[ID]chan *WireResponse
	pendingMu  sync.Mutex // protects the pending map
	handling   map[ID]*Request
	handlingMu sync.RWMutex // protects the handling map
	logger     *zap.Logger
}

// compile time check whether the Conn implements Interface interface.
var _ Interface = (*Conn)(nil)

// Options represents a functional options.
type Options func(*Conn)

// WithHandlers apply custom hander to Conn.
func WithHandlers(hs ...Handler) Options {
	return func(c *Conn) {
		c.handlers = hs
	}
}

// WithLogger apply custom Logger to Conn.
func WithLogger(logger *zap.Logger) Options {
	return func(c *Conn) {
		c.logger = logger
	}
}

// NewConn creates a new connection object that reads and writes messages from
// the supplied stream and dispatches incoming messages to the supplied handler.
func NewConn(s Stream, options ...Options) *Conn {
	conn := &Conn{
		stream:   s,
		pending:  make(map[ID]chan *WireResponse),
		handling: make(map[ID]*Request),
	}

	for _, opt := range options {
		opt(conn)
	}

	// the default handler reports a method error
	if conn.handlers == nil {
		h := EmptyHandler{}
		conn.handlers = []Handler{h}
	}
	// the default Logger does nothing
	if conn.logger == nil {
		conn.logger = zap.NewNop()
	}

	return conn
}

// AddHandler adds a new handler to the set the connection will invoke.
// Handlers are invoked in the reverse order of how they were added, this
// allows the most recent addition to be the first one to attempt to handle a
// message.
func (c *Conn) AddHandler(handler Handler) {
	// prepend the new handlers so we use them first
	c.handlers = append([]Handler{handler}, c.handlers...)
}

// Cancel cancels a pending Call on the server side.
// The call is identified by its id.
// JSON-RPC 2 does not specify a cancel message, so cancellation support is not
// directly wired in. This method allows a higher level protocol to choose how
// to propagate the cancel.
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
	p, err := marshalInterface(params)
	if err != nil {
		return fmt.Errorf("failed to marshaling notify parameters: %v", err)
	}
	req := &WireRequest{
		Method: method,
		Params: p,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshaling notify request: %v", err)
	}

	for _, h := range c.handlers {
		ctx = h.Request(ctx, c, Send, req)
	}
	defer func() {
		for _, h := range c.handlers {
			h.Done(ctx, err)
		}
	}()

	n, err := c.stream.Write(ctx, data)
	for _, h := range c.handlers {
		ctx = h.Write(ctx, n)
	}

	return err
}

// Call sends a request over the connection and then waits for a response.
func (c *Conn) Call(ctx context.Context, method string, params, result interface{}) error {
	// generate a new request identifier
	id := NewIntID(atomic.AddInt64(&c.seq, 1))
	p, err := marshalInterface(params)
	if err != nil {
		return fmt.Errorf("failed to marshaling call parameters: %v", err)
	}
	req := &WireRequest{
		ID:     &id,
		Method: method,
		Params: p,
	}

	// marshal the request now it is complete
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshaling call request: %v", err)
	}
	for _, h := range c.handlers {
		ctx = h.Request(ctx, c, Send, req)
	}

	// We have to add ourselves to the pending map before we send, otherwise we
	// are racing the response. Also add a buffer to rchan, so that if we get a
	// wire response between the time this call is canceled and id is deleted
	// from c.pending, the send to rchan will not block.
	rchan := make(chan *WireResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = rchan
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		for _, h := range c.handlers {
			h.Done(ctx, err)
		}
	}()

	n, err := c.stream.Write(ctx, data)
	for _, h := range c.handlers {
		ctx = h.Write(ctx, n)
	}
	if err != nil {
		// sending failed, we will never get a response, so don't leave it pending
		return err
	}

	// now wait for the response
	select {
	case resp := <-rchan:
		for _, h := range c.handlers {
			ctx = h.Response(ctx, c, Receive, resp)
		}
		// is it an error response?
		if resp.Error != nil {
			return resp.Error
		}
		if result == nil || resp.Result == nil {
			return nil
		}

		if err := json.Unmarshal(*resp.Result, result); err != nil {
			return fmt.Errorf("failed to unmarshalling result: %v", err)
		}
		return nil

	case <-ctx.Done():
		// allow the handler to propagate the cancel
		canceled := false
		for _, h := range c.handlers {
			if h.Cancel(ctx, c, id, canceled) {
				canceled = true
			}
		}
		return ctx.Err()
	}
}

func (c *Conn) setHandling(r *Request, active bool) {
	if r.ID == nil {
		return
	}
	r.conn.handlingMu.Lock()
	if active {
		r.conn.handling[*r.ID] = r
	} else {
		delete(r.conn.handling, *r.ID)
	}
	r.conn.handlingMu.Unlock()
}

// Run blocks until the connection is terminated, and returns any error that
// caused the termination.
//
// It must be called exactly once for each Conn.
// It returns only when the reader is closed or there is an error in the stream.
func (c *Conn) Run(ctx context.Context) error {
	// we need to make the next request "lock" in an unlocked state to allow
	// the first incoming request to proceed. All later requests are unlocked
	// by the preceding request going to parallel mode.
	nextReqCh := make(chan struct{})
	close(nextReqCh)

	for {
		data, n, err := c.stream.Read(ctx) // get the data for a message
		if err != nil {
			return err // failed to reads stream, cannot continue
		}

		// read a combined message
		msg := &Combined{}
		if err := json.Unmarshal(data, msg); err != nil {
			errMsg := fmt.Errorf("failed to unmarshal: %v", err)
			// a badly formed message arrived, log it and continue
			// we trust the stream to have isolated the error to just this message
			for _, h := range c.handlers {
				h.Error(ctx, errMsg)
			}
			continue
		}

		// work out which kind of message we have
		switch {
		case msg.Method != "": // msg.Method not empty, handle the Request
			// if method is set it must be a request
			reqCtx, cancelReq := context.WithCancel(ctx)
			curReqCh := nextReqCh
			nextReqCh = make(chan struct{})
			req := &Request{
				conn:      c,
				cancel:    cancelReq,
				nextReqCh: nextReqCh,
				WireRequest: WireRequest{
					Method: msg.Method,
					Params: msg.Params,
					ID:     msg.ID,
				},
			}
			for _, h := range c.handlers {
				reqCtx = h.Request(reqCtx, c, Receive, &req.WireRequest)
				reqCtx = h.Read(reqCtx, n)
			}
			c.setHandling(req, true)

			go func() {
				<-curReqCh
				req.state = reqSerial
				defer func() {
					c.setHandling(req, false)
					if !req.IsNotify() && req.state < reqReplied {
						req.Reply(reqCtx, nil, Errorf(InternalError, "method %s did not reply", req.Method))
					}

					req.Parallel()
					for _, h := range c.handlers {
						h.Done(reqCtx, err)
					}
					cancelReq()
				}()

				delivered := false
				for _, h := range c.handlers {
					if h.Deliver(reqCtx, req, delivered) {
						delivered = true
					}
				}
			}()

		case msg.ID != nil: // msg.ID not nil, handle the response
			// If method is not set, this should be a response, in which case we must
			// have an id to send the response back to the caller.
			c.pendingMu.Lock()
			rchan, ok := c.pending[*msg.ID]
			c.pendingMu.Unlock()
			if ok {
				resp := &WireResponse{
					Result: msg.Result,
					Error:  msg.Error,
					ID:     msg.ID,
				}
				rchan <- resp
			}

		default:
			for _, h := range c.handlers {
				h.Error(ctx, fmt.Errorf("message not a call, notify or response, ignoring"))
			}
		}
	}
}

type reqState uint8

const (
	reqWaiting reqState = iota
	reqSerial
	reqParallel
	reqReplied
	reqDone
)

// Request is sent to a server to represent a Call or Notify operaton.
type Request struct {
	conn      *Conn
	cancel    context.CancelFunc
	state     reqState
	nextReqCh chan struct{}

	WireRequest
}

// Conn returns the connection that created this request.
func (r *Request) Conn() *Conn { return r.conn }

// IsNotify returns true if this request is a notification.
func (r *Request) IsNotify() bool { return r.ID == nil }

// Parallel indicates that the system is now allowed to process other requests
// in parallel with this one.
// It is safe to call any number of times, but must only be called from the
// request handling go routine.
// It is implied by both reply and by the handler returning.
func (r *Request) Parallel() {
	if r.state >= reqParallel {
		return
	}
	r.state = reqParallel
	close(r.nextReqCh)
}

// Reply sends a reply to the given request.
func (r *Request) Reply(ctx context.Context, result interface{}, reqErr error) error {
	if r.state >= reqReplied {
		return fmt.Errorf("reply invoked more than once")
	}
	if r.IsNotify() {
		return fmt.Errorf("reply not invoked with a valid call: %v, %v", r.Method, r.Params)
	}

	// reply ends the handling phase of a call, so if we are not yet
	// parallel we should be now. The go routine is allowed to continue
	// to do work after replying, which is why it is important to unlock
	// the rpc system at this point.
	r.Parallel()
	r.state = reqReplied

	var raw *json.RawMessage
	var err error
	if reqErr == nil {
		raw, err = marshalInterface(result)
	}
	if err != nil {
		return err
	}
	resp := &WireResponse{
		Result: raw,
		ID:     r.ID,
	}
	if reqErr != nil {
		var callErr *Error
		if errors.As(reqErr, &callErr) {
			resp.Error = callErr
		} else {
			resp.Error = Errorf(0, "%s", reqErr)
		}
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	for _, h := range r.conn.handlers {
		ctx = h.Response(ctx, r.conn, Send, resp)
	}
	n, err := r.conn.stream.Write(ctx, data)
	for _, h := range r.conn.handlers {
		ctx = h.Write(ctx, n)
	}

	return err
}

// marshalInterface marshal obj to RawMessage.
func marshalInterface(obj interface{}) (*json.RawMessage, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(data)

	return &raw, nil
}
