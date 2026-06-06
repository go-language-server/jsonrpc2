// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
)

// PipelineClient is the pipelined client mode.
//
// It supports concurrent client-driven calls and notifications, but it does not
// dispatch server-initiated requests. The response reader is client-only: for
// frame-capable streams it scans borrowed response views and unmarshals the
// result directly into the waiting call's destination before the next frame can
// invalidate the borrowed bytes.
type PipelineClient struct {
	stream Stream
	frames frameStream
	fw     frameWriter
	codec  Codec

	seq atomic.Int64 // last allocated outgoing call id

	mu         sync.Mutex
	calls      densePipelineCallSlots
	reading    bool
	closing    bool
	doneClosed bool
	readErr    error
	writeErr   error
	closeErr   error
	done       chan struct{}
}

// NewPipelineClient creates a pipelined client over stream.
func NewPipelineClient(stream Stream, opts ...Option) *PipelineClient {
	probe := &conn{codec: DefaultCodec}
	for _, opt := range opts {
		opt(probe)
	}
	frames, _ := stream.(frameStream)
	fw, _ := stream.(frameWriter)
	return &PipelineClient{
		stream: stream,
		frames: frames,
		fw:     fw,
		codec:  probe.codec,
		done:   make(chan struct{}),
	}
}

var _ Conn = (*PipelineClient)(nil)

// Call implements [Conn].
func (c *PipelineClient) Call(ctx context.Context, method string, params, result any) (ID, error) {
	raw, err := marshalParams(c.codec, params)
	if err != nil {
		return ID{}, fmt.Errorf("jsonrpc2: marshaling call parameters: %w", err)
	}

	id := NewNumberID(c.seq.Add(1))
	w := getPipelineWaiter(result)

	c.mu.Lock()
	if shutErr := c.shuttingDownLocked(); shutErr != nil {
		c.mu.Unlock()
		putPipelineWaiter(w)
		return id, shutErr
	}
	c.calls.Add(id, w)
	c.mu.Unlock()

	if err := c.writeCall(ctx, id, method, raw); err != nil {
		c.afterWrite(ctx, err)
		if c.retireCall(id) {
			putPipelineWaiter(w)
			return id, err
		}
		<-w.ready
		putPipelineWaiter(w)
		return id, err
	}

	select {
	case err := <-w.ready:
		putPipelineWaiter(w)
		if err != nil {
			return id, err
		}
		return id, nil
	case <-ctx.Done():
		if c.retireCall(id) {
			putPipelineWaiter(w)
			return id, ctx.Err()
		}
		<-w.ready
		putPipelineWaiter(w)
		return id, ctx.Err()
	}
}

// Notify implements [Conn].
func (c *PipelineClient) Notify(ctx context.Context, method string, params any) error {
	raw, err := marshalParams(c.codec, params)
	if err != nil {
		return fmt.Errorf("jsonrpc2: marshaling notify parameters: %w", err)
	}

	c.mu.Lock()
	shutErr := c.shuttingDownLocked()
	c.mu.Unlock()
	if shutErr != nil {
		return shutErr
	}

	err = c.writeNotification(ctx, method, raw)
	c.afterWrite(ctx, err)
	return err
}

// Go implements [Conn].
func (c *PipelineClient) Go(ctx context.Context, _ Handler) {
	c.mu.Lock()
	switch {
	case c.reading:
		c.mu.Unlock()
		panic("jsonrpc2: PipelineClient.Go called more than once")
	case c.doneClosed:
		c.mu.Unlock()
		return
	case c.closing:
		c.closeDoneLocked()
		c.mu.Unlock()
		return
	default:
		c.reading = true
		c.mu.Unlock()
	}
	go c.readResponses(ctx)
}

// Close implements [Conn].
func (c *PipelineClient) Close() error {
	c.mu.Lock()
	if c.doneClosed {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	shouldClose := !c.closing
	c.closing = true
	c.drainCallsLocked(ErrClientClosing)
	if !c.reading {
		c.closeDoneLocked()
	}
	done := c.done
	c.mu.Unlock()

	if shouldClose {
		c.recordCloseError(c.stream.Close())
	}
	<-done
	return c.closeReturnError()
}

// Done implements [Conn].
func (c *PipelineClient) Done() <-chan struct{} { return c.done }

// Err implements [Conn].
func (c *PipelineClient) Err() error {
	err := c.terminationError()
	switch {
	case err == nil,
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrClosedPipe),
		errors.Is(err, os.ErrClosed),
		errors.Is(err, net.ErrClosed),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, ErrServerClosing),
		errors.Is(err, ErrClientClosing):
		return nil
	default:
		return err
	}
}

func (c *PipelineClient) writeCall(ctx context.Context, id ID, method string, params RawMessage) error {
	if c.fw != nil {
		_, err := c.fw.writeCall(ctx, id, method, params)
		return err
	}
	if c.frames != nil {
		_, err := c.frames.WriteFrame(ctx, appendCallFields(nil, id, method, params))
		return err
	}
	_, err := c.stream.Write(ctx, callWire{id: id, method: method, params: params})
	return err
}

func (c *PipelineClient) writeNotification(ctx context.Context, method string, params RawMessage) error {
	if c.fw != nil {
		_, err := c.fw.writeNotification(ctx, method, params)
		return err
	}
	if c.frames != nil {
		_, err := c.frames.WriteFrame(ctx, appendNotificationFields(nil, method, params))
		return err
	}
	_, err := c.stream.Write(ctx, notificationWire{method: method, params: params})
	return err
}

func (c *PipelineClient) readResponses(ctx context.Context) {
	var err error
	for {
		if err = c.readResponse(ctx); err != nil {
			break
		}
	}
	c.finishRead(err)
}

func (c *PipelineClient) readResponse(ctx context.Context) error {
	if c.frames == nil {
		msg, _, err := c.stream.Read(ctx)
		if err != nil {
			return err
		}
		resp, ok := msg.(*Response)
		if !ok {
			return fmt.Errorf("jsonrpc2: pipeline client received non-response %T", msg)
		}
		c.deliverResponse(resp.id, resp.result, resp.err)
		return nil
	}

	frame, _, err := c.frames.ReadFrame(ctx)
	if err != nil {
		return err
	}
	if id, result, ok, err := scanPipelineResultResponseNumber(frame); ok || err != nil {
		if err != nil {
			return err
		}
		c.deliverNumberResponse(id, result, nil)
		return nil
	}
	view, err := ScanMessageView(frame)
	if err != nil {
		return fmt.Errorf("jsonrpc2: decoding response: %w", err)
	}
	switch view.Kind {
	case MessageViewResponseResult:
		id, ok := view.ID.ID()
		if !ok {
			return ErrInvalidRequest
		}
		c.deliverResponse(id, RawMessage(view.Result), nil)
		return nil
	case MessageViewResponseError:
		id, ok := view.ID.ID()
		if !ok {
			return ErrInvalidRequest
		}
		respErr, ok := view.Error.Owned()
		if !ok {
			return ErrInvalidRequest
		}
		c.deliverResponse(id, nil, respErr)
		return nil
	default:
		return fmt.Errorf("jsonrpc2: pipeline client received non-response %s", view.Kind)
	}
}

const pipelineResultResponsePrefix = `{"jsonrpc":"2.0","id":`

func scanPipelineResultResponse(frame []byte) (id ID, result RawMessage, ok bool, err error) {
	n, result, ok, err := scanPipelineResultResponseNumber(frame)
	if !ok || err != nil {
		return ID{}, result, ok, err
	}
	return NewNumberID(n), result, true, nil
}

func scanPipelineResultResponseNumber(frame []byte) (id int64, result RawMessage, ok bool, err error) {
	i := skipSpace(frame, 0)
	if !hasLiteralAt(frame, i, pipelineResultResponsePrefix) {
		return 0, nil, false, nil
	}
	i += len(pipelineResultResponsePrefix)

	n, next, ok := parsePositiveInt64(frame, i)
	if !ok {
		return 0, nil, false, nil
	}
	i = next
	if !hasLiteralAt(frame, i, `,"result":`) {
		return 0, nil, false, nil
	}
	i += len(`,"result":`)

	valStart := i
	if hasLiteralAt(frame, i, "null") {
		i += len("null")
		i = skipSpace(frame, i)
		if i >= len(frame) || frame[i] != '}' {
			return 0, nil, false, ErrInvalidRequest
		}
		i = skipSpace(frame, i+1)
		if i != len(frame) {
			return 0, nil, false, ErrInvalidRequest
		}
		return n, RawMessage(frame[valStart : valStart+len("null")]), true, nil
	}

	valEnd, ok := scanValue(frame, i)
	if !ok {
		return 0, nil, false, ErrParse
	}
	i = skipSpace(frame, valEnd)
	if i >= len(frame) || frame[i] != '}' {
		return 0, nil, false, ErrInvalidRequest
	}
	i = skipSpace(frame, i+1)
	if i != len(frame) {
		return 0, nil, false, ErrInvalidRequest
	}
	return n, RawMessage(frame[valStart:valEnd]), true, nil
}

func hasLiteralAt(data []byte, i int, lit string) bool {
	if i < 0 || len(data)-i < len(lit) {
		return false
	}
	for j := range lit {
		if data[i+j] != lit[j] {
			return false
		}
	}
	return true
}

func parsePositiveInt64(data []byte, i int) (n int64, next int, ok bool) {
	if i >= len(data) || data[i] < '1' || data[i] > '9' {
		return 0, i, false
	}
	const maxInt64 = int64(1<<63 - 1)
	for i < len(data) {
		c := data[i]
		if c < '0' || c > '9' {
			break
		}
		digit := int64(c - '0')
		if n > (maxInt64-digit)/10 {
			return 0, i, false
		}
		n = n*10 + digit
		i++
	}
	return n, i, true
}

func (c *PipelineClient) deliverResponse(id ID, result RawMessage, respErr error) {
	w := c.takeCall(id)
	if w == nil {
		return
	}
	c.deliverWaiter(w, result, respErr)
}

func (c *PipelineClient) deliverNumberResponse(id int64, result RawMessage, respErr error) {
	w := c.takeNumberCall(id)
	if w == nil {
		return
	}
	c.deliverWaiter(w, result, respErr)
}

func (c *PipelineClient) deliverWaiter(w *pipelineWaiter, result RawMessage, respErr error) {
	if respErr != nil {
		w.deliver(respErr)
		return
	}
	if err := unmarshalResult(c.codec, result, w.result); err != nil {
		w.deliver(fmt.Errorf("jsonrpc2: unmarshaling result: %w", err))
		return
	}
	w.deliver(nil)
}

func (c *PipelineClient) retireCall(id ID) bool {
	c.mu.Lock()
	_, ok := c.calls.Take(id)
	c.mu.Unlock()
	return ok
}

func (c *PipelineClient) takeCall(id ID) *pipelineWaiter {
	c.mu.Lock()
	w, _ := c.calls.Take(id)
	c.mu.Unlock()
	return w
}

func (c *PipelineClient) takeNumberCall(id int64) *pipelineWaiter {
	c.mu.Lock()
	w, _ := c.calls.TakeNumber(id)
	c.mu.Unlock()
	return w
}

func (c *PipelineClient) afterWrite(ctx context.Context, err error) {
	if err == nil || ctx.Err() != nil {
		return
	}
	c.fail(err)
}

func (c *PipelineClient) fail(err error) {
	c.mu.Lock()
	if c.writeErr == nil {
		c.writeErr = err
	}
	shouldClose := !c.closing
	c.closing = true
	c.drainCallsLocked(err)
	closeDone := !c.reading
	c.mu.Unlock()

	if shouldClose {
		c.recordCloseError(c.stream.Close())
	}
	if closeDone {
		c.mu.Lock()
		c.closeDoneLocked()
		c.mu.Unlock()
	}
}

func (c *PipelineClient) finishRead(err error) {
	c.mu.Lock()
	c.reading = false
	if c.readErr == nil {
		c.readErr = err
	}
	shouldClose := !c.closing
	c.closing = true
	c.drainCallsLocked(err)
	c.mu.Unlock()

	if shouldClose {
		c.recordCloseError(c.stream.Close())
	}

	c.mu.Lock()
	c.closeDoneLocked()
	c.mu.Unlock()
}

func (c *PipelineClient) shuttingDownLocked() error {
	switch {
	case c.closing:
		return ErrClientClosing
	case c.readErr != nil:
		return fmt.Errorf("%w: %w", ErrClientClosing, c.readErr)
	case c.writeErr != nil:
		return fmt.Errorf("%w: %w", ErrClientClosing, c.writeErr)
	case c.doneClosed:
		return ErrClientClosing
	default:
		return nil
	}
}

func (c *PipelineClient) drainCallsLocked(err error) {
	c.calls.Drain(func(_ ID, w *pipelineWaiter) {
		w.deliver(err)
	})
}

func (c *PipelineClient) closeDoneLocked() {
	if !c.doneClosed {
		close(c.done)
		c.doneClosed = true
	}
}

func (c *PipelineClient) recordCloseError(err error) {
	c.mu.Lock()
	if c.closeErr == nil {
		c.closeErr = err
	}
	c.mu.Unlock()
}

func (c *PipelineClient) closeReturnError() error {
	c.mu.Lock()
	err := c.closeErr
	c.mu.Unlock()
	return err
}

func (c *PipelineClient) terminationError() error {
	c.mu.Lock()
	err := c.terminationErrorLocked()
	c.mu.Unlock()
	return err
}

func (c *PipelineClient) terminationErrorLocked() error {
	switch {
	case c.writeErr != nil:
		return c.writeErr
	case c.readErr != nil && !errors.Is(c.readErr, io.EOF):
		return c.readErr
	default:
		return c.closeErr
	}
}

type pipelineWaiter struct {
	ready  chan error
	result any
}

func (w *pipelineWaiter) deliver(err error) { w.ready <- err }

var pipelineWaiterPool = sync.Pool{
	New: func() any {
		return &pipelineWaiter{ready: make(chan error, 1)}
	},
}

func getPipelineWaiter(result any) *pipelineWaiter {
	w := pipelineWaiterPool.Get().(*pipelineWaiter)
	w.result = result
	return w
}

func putPipelineWaiter(w *pipelineWaiter) {
	select {
	case <-w.ready:
	default:
	}
	w.result = nil
	pipelineWaiterPool.Put(w)
}

type densePipelineCallSlots struct {
	slots []densePipelineCallSlot
	base  int64
	live  int
}

type densePipelineCallSlot struct {
	id     int64
	waiter *pipelineWaiter
}

func (s *densePipelineCallSlots) Len() int { return s.live }

func (s *densePipelineCallSlots) Add(id ID, w *pipelineWaiter) {
	n, ok := id.Number()
	if !ok {
		panic("jsonrpc2: dense pipeline id is not numeric")
	}
	if n <= 0 {
		panic("jsonrpc2: dense pipeline id must be positive")
	}
	if len(s.slots) == 0 {
		s.base = n
		s.slots = make([]densePipelineCallSlot, initialOutgoingCallSlots)
	} else if s.live == 0 {
		s.base = n
	}
	if n < s.base {
		s.rebase(n)
	}
	if need := int(n - s.base + 1); need > len(s.slots) {
		s.grow(need)
	}
	idx := s.index(n)
	if s.slots[idx].waiter != nil {
		panic("jsonrpc2: duplicate dense pipeline id")
	}
	s.slots[idx] = densePipelineCallSlot{id: n, waiter: w}
	s.live++
}

func (s *densePipelineCallSlots) Take(id ID) (*pipelineWaiter, bool) {
	n, ok := id.Number()
	if !ok {
		return nil, false
	}
	return s.TakeNumber(n)
}

func (s *densePipelineCallSlots) TakeNumber(n int64) (*pipelineWaiter, bool) {
	if s.live == 0 || len(s.slots) == 0 || n < s.base || int(n-s.base) >= len(s.slots) {
		return nil, false
	}
	idx := s.index(n)
	slot := &s.slots[idx]
	if slot.waiter == nil || slot.id != n {
		return nil, false
	}
	w := slot.waiter
	*slot = densePipelineCallSlot{}
	s.live--
	if s.live == 0 {
		clear(s.slots)
		s.base = 0
		return w, true
	}
	if n == s.base {
		s.advanceBase()
	}
	return w, true
}

func (s *densePipelineCallSlots) Drain(f func(ID, *pipelineWaiter)) {
	if s.live == 0 {
		return
	}
	for i := range s.slots {
		if w := s.slots[i].waiter; w != nil {
			f(NewNumberID(s.slots[i].id), w)
			s.slots[i] = densePipelineCallSlot{}
		}
	}
	s.live = 0
	s.base = 0
}

func (s *densePipelineCallSlots) index(id int64) int {
	return int(uint64(id) & uint64(len(s.slots)-1))
}

func (s *densePipelineCallSlots) advanceBase() {
	for s.live > 0 {
		idx := s.index(s.base)
		if s.slots[idx].waiter != nil && s.slots[idx].id == s.base {
			return
		}
		s.base++
	}
}

func (s *densePipelineCallSlots) rebase(base int64) {
	if s.live == 0 {
		s.base = base
		return
	}
	maxID := base
	for i := range s.slots {
		if s.slots[i].waiter != nil && s.slots[i].id > maxID {
			maxID = s.slots[i].id
		}
	}
	if need := int(maxID - base + 1); need > len(s.slots) {
		s.grow(need)
	}
	s.base = base
}

func (s *densePipelineCallSlots) grow(need int) {
	size := len(s.slots)
	for size < need {
		size *= 2
	}
	old := s.slots
	s.slots = make([]densePipelineCallSlot, size)
	for i := range old {
		if old[i].waiter != nil {
			s.slots[s.index(old[i].id)] = old[i]
		}
	}
}
