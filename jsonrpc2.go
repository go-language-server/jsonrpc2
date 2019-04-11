// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"io"
	"time"

	"github.com/francoispqt/gojay"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"golang.org/x/xerrors"
)

// Interface represents an interface for issuing requests that speak the JSON-RPC 2 protocol.
type Interface interface {
	io.ReadWriter

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
	ctx        context.Context // for Read and Write only
	seq        atomic.Int64    // must only be accessed using atomic operations
	pending    atomic.Value    // map[ID]chan *Response
	handling   atomic.Value    // map[ID]handling
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

type handling struct {
	request *Request
	cancel  context.CancelFunc
	start   time.Time
}

type pendingMap map[ID]chan *Response
type handlingMap map[ID]handling

var (
	errLoadPendingMap  = xerrors.New("failed to Load pendingMap")
	errLoadhandlingMap = xerrors.New("failed to Load handlingMap")
)

// NewConn creates a new connection object that reads and writes messages from
// the supplied stream and dispatches incoming messages to the supplied handler.
func NewConn(ctx context.Context, s Stream, options ...Options) *Conn {
	conn := &Conn{
		stream: s,
		done:   make(chan struct{}),
	}
	conn.pending.Store(make(pendingMap))
	conn.handling.Store(make(handlingMap))

	for _, opt := range options {
		opt(conn)
	}

	if conn.Handler == nil {
		// the default handler reports a method error
		conn.Handler = defaultHandler
	}
	if conn.Canceler == nil {
		// the default canceller does nothing
		conn.Canceler = defaultCanceler
	}
	if conn.logger == nil {
		// the default logger does nothing
		conn.logger = zap.NewNop()
	}

	return conn
}

// Read implements io.Reader.
func (c *Conn) Read(p []byte) (n int, err error) {
	return c.stream.Read(c.ctx, p)
}

// Write implements io.Write.
func (c *Conn) Write(p []byte) (n int, err error) {
	return c.stream.Write(c.ctx, p)
}

// Call sends a request over the connection and then waits for a response.
func (c *Conn) Call(ctx context.Context, method string, params, result interface{}) error {
	jsonParams, err := marshalToEmbedded(params)
	if err != nil {
		return xerrors.Errorf("failed to marshaling call parameters: %v", err)
	}

	id := ID{Number: c.seq.Add(1)}
	req := &Request{
		JSONRPC: Version,
		ID:      &id,
		Method:  method,
		Params:  jsonParams,
	}

	// marshal the request now it is complete
	data, err := gojay.MarshalJSONObject(req)
	if err != nil {
		return xerrors.Errorf("failed to marshaling call request: %v", err)
	}

	rchan := make(chan *Response)
	m, ok := c.pending.Load().(pendingMap)
	if !ok {
		return errLoadPendingMap
	}
	m[id] = rchan
	c.pending.Store(m)
	defer func() {
		m, ok := c.pending.Load().(pendingMap)
		if !ok {
			panic(errLoadPendingMap)
		}
		delete(m, id)
		c.pending.Store(m)
	}()

	start := time.Now()
	c.logger.Debug(Send,
		zap.String("req.JSONRPC", req.JSONRPC),
		zap.Any("id", id),
		zap.String("req.method", req.Method),
		zap.Any("req.params", req.Params),
	)
	if _, err := c.stream.Write(ctx, data); err != nil {
		return err
	}

	select {
	case resp := <-rchan:
		c.logger.Debug(Receive,
			zap.String("req.JSONRPC", req.JSONRPC),
			zap.Any("id", id),
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
			return xerrors.Errorf("failed to unmarshalling result: %v", err)
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
		return xerrors.New("reply not invoked with a valid call")
	}

	m, ok := c.handling.Load().(handlingMap)
	if !ok {
		return errLoadhandlingMap
	}
	handling, found := m[*req.ID]
	if !found {
		return xerrors.Errorf("not a call in progress: %v", req.ID)
	}

	elapsed := time.Since(handling.start)

	resp := &Response{
		JSONRPC: Version,
		ID:      req.ID,
	}
	if err == nil {
		if resp.Result, err = marshalToEmbedded(result); err != nil {
			return err
		}
	} else {
		resp.Error = New(CodeParseError, err)
	}

	data, err := gojay.Marshal(resp)
	if err != nil {
		c.logger.Error(Send,
			zap.Any("resp.ID", resp.ID.Number),
			zap.Duration("elapsed", elapsed),
			zap.String("req.Method", req.Method),
			zap.Any("resp.Result", resp.Result),
			zap.Error(err),
		)
		return err
	}

	c.logger.Debug(Send,
		zap.Any("resp.ID", resp.ID),
		zap.Duration("elapsed", elapsed),
		zap.String("req.Method", req.Method),
		zap.Any("resp.Result", resp.Result),
		zap.Error(resp.Error),
	)

	if _, err := c.stream.Write(ctx, data); err != nil {
		return err
	}

	return nil
}

// Notify is called to send a notification request over the connection.
func (c *Conn) Notify(ctx context.Context, method string, params interface{}) error {
	prms, err := marshalToEmbedded(params)
	if err != nil {
		return Errorf(CodeParseError, "failed to marshaling notify parameters: %v", err)
	}

	req := &NotificationMessage{
		JSONRPC: Version,
		Method:  method,
		Params:  prms,
	}
	data, err := gojay.MarshalJSONObject(req)
	if err != nil {
		return Errorf(CodeParseError, "failed to marshaling notify request: %v", err)
	}

	c.logger.Debug(Send,
		zap.String("req.Method", req.Method),
		zap.Any("req.Params", req.Params),
	)

	_, err = c.stream.Write(ctx, data)

	return err
}

// Cancel cancels a pending Call on the server side.
func (c *Conn) Cancel(id ID) {
	m, ok := c.handling.Load().(handlingMap)
	if !ok {
		panic(errLoadhandlingMap)
	}
	handling, found := m[id]
	if found {
		handling.cancel()
	}
}

type queue struct {
	ctx context.Context
	c   *Conn
	r   *Request
}

func (c *Conn) deliver(ctx context.Context, q chan queue, request *Request) bool {
	e := queue{ctx: ctx, c: c, r: request}
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
	q := make(chan queue, c.capacity)
	defer close(q)

	// start the queue processor
	go func() {
		for e := range q {
			if e.ctx.Err() != nil {
				continue
			}
			c.Handler(e.ctx, e.c, e.r)
		}
	}()

	for {
		data := make([]byte, 0, 512)
		_, err = c.stream.Read(ctx, data) // get the data for a message
		if err != nil {
			return err // the stream failed, we cannot continue
		}

		// read a combined message
		msg := new(Combined)
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
		case msg.Method != "":
			// if method is set it must be a request
			req := &Request{
				JSONRPC: Version,
				Method:  msg.Method,
				Params:  msg.Params,
				ID:      msg.ID,
			}
			if req.IsNotify() {
				c.logger.Debug(Receive,
					zap.Any("req.ID", req.ID),
					zap.String("req.Method", req.Method),
					zap.Any("req.Params", req.Params),
				)
				c.deliver(ctx, q, req)

				return
			}

			// we have a Call, add to the processor queue
			reqCtx, reqCancel := context.WithCancel(ctx)
			m, ok := c.handling.Load().(handlingMap)
			if !ok {
				return errLoadhandlingMap
			}
			m[*req.ID] = handling{
				request: req,
				cancel:  reqCancel,
				start:   time.Now(),
			}
			c.handling.Store(m)

			c.logger.Debug(Receive,
				zap.Any("req.ID", req.ID),
				zap.String("req.Method", req.Method),
				zap.Any("req.Params", req.Params),
			)
			if !c.deliver(reqCtx, q, req) {
				// queue is full, reject the message by directly replying
				c.Reply(ctx, req, nil, Errorf(CodeServerOverloaded, "no room in queue"))
			}

		case msg.ID != nil:
			// we have a response, get the pending entry from the map
			m, ok := c.handling.Load().(pendingMap)
			if !ok {
				return errLoadPendingMap
			}
			rchan := m[*msg.ID]
			if rchan != nil {
				delete(m, *msg.ID)
			}
			c.pending.Store(m)

			// and send the reply to the channel
			resp := &Response{
				JSONRPC: Version,
				Result:  msg.Result,
				Error:   msg.Error,
				ID:      msg.ID,
			}
			rchan <- resp
			close(rchan)

		default:
			c.logger.Error(Receive, zap.Error(Errorf(CodeInvalidParams, "message not a call, notify or response, ignoring")))
		}
	}
}

const (
	// Send indicates the message is outgoing.
	Send = "send"
	// Receive indicates the message is incoming.
	Receive = "receive"
)

func marshalToEmbedded(obj interface{}) (*RawMessage, error) {
	data, err := gojay.MarshalAny(obj)
	if err != nil {
		return nil, err
	}
	raw := RawMessage(gojay.EmbeddedJSON(data))

	return &raw, nil
}
