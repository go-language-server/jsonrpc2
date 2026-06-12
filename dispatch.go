// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

// setupRequest registers req as in-flight and returns the per-request context.
// It returns done=true when the request has already been fully answered (a
// malformed batch member, a preempted request, or a call rejected because the
// connection is shutting down), in which case the caller must not invoke the
// handler.
func (c *conn) setupRequest(ctx context.Context, req Request, bc *batchCollector) (ir *incomingRequest, done bool) {
	if inv, ok := req.(*invalidRequest); ok {
		// A malformed batch member never reaches the handler; answer it directly
		// with a null-id error response so the valid members are still served.
		c.writeInvalid(ctx, bc, inv.err)
		return nil, true
	}

	id, isCall := callID(req)
	ir = &incomingRequest{req: req, parent: ctx, id: id, isCall: isCall}

	// A Preempter, if configured, runs inline on the read goroutine before the
	// request is queued for the handler. ErrNotHandled or a nil handled value
	// defers to the handler; every other result or error is answered here.
	if c.preempter != nil {
		result, perr := c.preempter.Preempt(ir, req)
		if !errors.Is(perr, ErrNotHandled) && (result != nil || perr != nil) {
			c.updateInFlight(func(s *inFlightState) { s.incoming++ })
			c.completeRequest(ir, ir, bc, result, perr)
			return nil, true
		}
	}

	var shutErr error
	c.updateInFlight(func(s *inFlightState) {
		s.incoming++
		if isCall {
			if s.incomingByID == nil {
				s.incomingByID = make(map[ID]*incomingRequest)
			}
			s.incomingByID[id] = ir
			shutErr = s.shuttingDown(ErrServerClosing)
		}
	})

	if shutErr != nil {
		// Reject a call that arrived while shutting down with an immediate error
		// response, and account for it so the connection can reach idle.
		c.completeRequest(ir, ir, bc, nil, shutErr)
		return nil, true
	}

	return ir, false
}

// runHandler invokes handler for the request, sending its reply and performing
// the request-scoped cleanup through defers so that a panic cannot leak the
// in-flight counter or the incomingByID entry and deadlock a later Close.
//
// Defers run last-in-first-out: the recover defer (registered last) runs first,
// then the release (the dispatch path's idempotent soft release), then
// afterHandle (registered first) runs last so the in-flight counter is
// decremented only after the response is sent and the read loop is released. It
// is shared by the inline single-message path and the batch-member goroutine.
func (c *conn) runHandler(handler Handler, ir *incomingRequest, rel *releaser, reply Replier, replied *repliedFlag) {
	req := ir.req
	defer c.afterHandle(ir)
	// release(true) is the soft release that frees the read loop in the
	// synchronous case (when the handler did not call Async) and after a panic.
	// It is idempotent, so a request that already released early via Async is
	// unaffected.
	defer rel.release(true)
	defer func() {
		if r := recover(); r != nil {
			// A handler panic is a request-scoped failure, not a process-wide one:
			// answer an unanswered call with an internal error so the caller (and
			// any batch flush awaiting this member) is not left hanging, then fail
			// the connection so the panic is surfaced rather than silently swallowed.
			if !replied.value() {
				_ = reply(ir, nil, Errorf(InternalError, "jsonrpc2: handler panicked: %v", r))
			}
			c.fail(fmt.Errorf("jsonrpc2: handler for %q panicked: %v", req.Method(), r))
		}
	}()

	err := handler(ir, reply, req)
	if err != nil {
		// A handler-returned error is a connection-level failure: fail the
		// connection so it tears down rather than silently dropping the error.
		c.fail(err)
	}
	// A handler that returns for a call without replying would otherwise leave the
	// caller (and any batch flush awaiting this member) hanging forever. Guarantee
	// a deterministic outcome by answering with an internal error.
	if !replied.value() {
		_ = reply(ir, nil, Errorf(InternalError, "jsonrpc2: handler for %q returned without replying", req.Method()))
	}
}

// handleRequest dispatches a single (non-batch) incoming request inline on the
// read goroutine. The handler runs to completion on this goroutine in the
// common synchronous case, so it spawns no goroutine and handlers observe
// requests in wire order.
//
// When the handler releases itself with [Async], the release effect spawns a
// fresh read goroutine to take over the reader role and records the handoff on
// the releaser; handleRequest then reports it so the current read loop returns
// while this goroutine finishes the handler concurrently. The successor reader
// owns loop termination, so the reader role is always held by exactly one
// goroutine.
func (c *conn) handleRequest(ctx context.Context, handler Handler, req Request) (handedOff bool) {
	ir, done := c.setupRequest(ctx, req, nil)
	if done {
		return false
	}

	// The inline releaser carries the state for the Async handoff in typed fields
	// (ch left nil). It is a value field of ir, so initializing it in place adds no
	// allocation beyond ir itself.
	ir.rel = releaser{active: true, conn: c, ctx: ctx, handler: handler, ir: ir}

	reply, replied := c.replier(ir, nil)
	c.runHandler(handler, ir, &ir.rel, reply, replied)
	// handedOff is set by a hard release (Async) on this same read goroutine
	// before runHandler returns, so the read carries no data race.
	return ir.rel.handedOff
}

// setupRequestV2 registers a scanned concrete request as in-flight. It mirrors
// setupRequest for the direct-return path: the request value is embedded in
// the incomingRequest rather than boxed behind the v1 interface, and the
// incomingRequest itself comes from the request pool. The optional Preempter
// is not consulted on this path; direct mode is the v2 surface.
func (c *conn) setupRequestV2(ctx context.Context, rv RequestV2) (ir *incomingRequest, done bool) {
	ir = getIncomingRequest()
	ir.parent = ctx
	ir.id = rv.id
	ir.isCall = rv.isCall
	ir.reqV2 = rv

	var shutErr error
	c.updateInFlight(func(s *inFlightState) {
		s.incoming++
		if rv.isCall {
			if s.incomingByID == nil {
				s.incomingByID = make(map[ID]*incomingRequest)
			}
			s.incomingByID[rv.id] = ir
			shutErr = s.shuttingDown(ErrServerClosing)
		}
	})

	if shutErr != nil {
		if ir.isCall && ir.replied.done.CompareAndSwap(false, true) {
			_ = c.sendResponse(ir, ir.id, nil, nil, shutErr)
		}
		c.afterHandle(ir)
		return nil, true
	}

	return ir, false
}

// handleRequestDirect dispatches a single (non-batch) incoming request through
// the direct-return path: the handler's return values are sent as the response
// directly, so no reply closure is allocated and no message box exists. The
// duplicate-reply guard lives on ir.replied exactly as in the closure path,
// shared with the panic fallback.
func (c *conn) handleRequestDirect(ctx context.Context, rv RequestV2) (handedOff bool) {
	ir, done := c.setupRequestV2(ctx, rv)
	if done {
		return false
	}

	// The successor handler is nil: a hard release (Async) hands the reader role
	// to a fresh readIncoming with a nil handler, which keeps direct dispatch.
	ir.rel = releaser{active: true, conn: c, ctx: ctx, ir: ir}

	c.runHandlerDirect(ir)
	return ir.rel.handedOff
}

// runHandlerDirect invokes the direct-return handler and answers the request
// from its return values. The deferred recover answers an unanswered call and
// fails the connection on panic, mirroring runHandler.
//
// The pool put is registered first so it runs last, strictly after afterHandle
// has finished the request's bookkeeping. A hard-released (Async) request is
// never pooled: its lifetime escaped the dispatch path, and a handler that
// detached may legally hold the request until it returns on its own schedule.
func (c *conn) runHandlerDirect(ir *incomingRequest) {
	defer putIncomingRequestUnlessDetached(ir)
	defer c.afterHandle(ir)
	defer ir.rel.release(true)
	defer func() {
		if r := recover(); r != nil {
			if ir.isCall && ir.replied.done.CompareAndSwap(false, true) {
				_ = c.sendResponse(ir, ir.id, nil, nil, Errorf(InternalError, "jsonrpc2: handler panicked: %v", r))
			}
			c.fail(fmt.Errorf("jsonrpc2: handler for %q panicked: %v", ir.reqV2.method, r))
		}
	}()

	result, err := c.directHandler(ir, &ir.reqV2)
	if !ir.isCall {
		// A notification has no response; a handler error is a connection-level
		// failure, matching the closure path's contract.
		if err != nil {
			c.fail(err)
		}
		return
	}
	if ir.replied.done.CompareAndSwap(false, true) {
		_ = c.sendResponse(ir, ir.id, nil, result, err)
	}
}

// handleBatchMember dispatches one batch member. Unlike the inline
// single-message path, a batch member runs in its own goroutine gated by a
// [releaser] channel so that an async member can overlap the remaining members
// of the same batch: the dispatch loop blocks until the member either calls
// [Async] (releasing it to run concurrently) or returns.
func (c *conn) handleBatchMember(ctx context.Context, handler Handler, req Request, bc *batchCollector) {
	ir, done := c.setupRequest(ctx, req, bc)
	if done {
		return
	}

	ir.rel = releaser{active: true, ch: make(chan struct{}), ir: ir}

	reply, replied := c.replier(ir, bc)

	go c.runHandler(handler, ir, &ir.rel, reply, replied)

	// Block until the member releases the dispatch loop: immediately for an async
	// member (via Async), or when the handler returns for a synchronous one.
	<-ir.rel.ch
}

// repliedFlag reports whether a request's [Replier] has been invoked. It is
// shared between the handler goroutine (which may reply from any goroutine once
// the request is released with [Async]) and the dispatch goroutine's deferred
// fallback, so it is backed by an atomic to make the read/write race-free.
type repliedFlag struct {
	done atomic.Bool
}

// value reports whether the reply has been sent.
func (f *repliedFlag) value() bool { return f.done.Load() }

// notificationReplied is a shared flag that always reads "replied". A
// notification expects no response, so the dispatch fallback must never fire for
// it; returning this flag avoids allocating a per-notification flag.
var notificationReplied = newRepliedFlag(true)

// newRepliedFlag returns a [repliedFlag] preset to done.
func newRepliedFlag(done bool) *repliedFlag {
	f := &repliedFlag{}
	f.done.Store(done)
	return f
}

// replier builds the [Replier] for an incoming request together with a flag
// reporting whether the reply has been sent. For a notification the replier is a
// no-op and the flag always reads true (no response is expected). For a call the
// replier writes the response, or, in a batch, collects the encoded response for
// the array flush, and marks the flag the first time it is invoked.
func (c *conn) replier(ir *incomingRequest, bc *batchCollector) (Replier, *repliedFlag) {
	id, isCall := callID(ir.req)
	if !isCall {
		// Notifications carry no id and receive no response.
		return func(context.Context, any, error) error { return nil }, notificationReplied
	}

	// flag is &ir.replied: the replied flag is a value field of the request, so it
	// shares ir's single allocation rather than a separate &repliedFlag{}.
	flag := &ir.replied
	return func(ctx context.Context, result any, err error) error {
		if !flag.done.CompareAndSwap(false, true) {
			// A duplicate reply is a programming error in the handler; ignore it
			// rather than writing a second, unmatched response.
			return nil
		}
		return c.sendResponse(ctx, id, bc, result, err)
	}, flag
}

// sendResponse marshals result (or err) into a response wire envelope and
// either writes it immediately or, for a batch member, hands it to the
// collector.
func (c *conn) sendResponse(ctx context.Context, id ID, bc *batchCollector, result any, err error) error {
	resp := responseWire{id: id, err: err}
	if err == nil {
		raw, merr := marshalParams(c.codec, result)
		if merr != nil {
			resp.err = Errorf(InternalError, "jsonrpc2: marshaling response result: %v", merr)
		} else {
			resp.result = raw
		}
	}
	// The id may be reused by the peer as soon as the response is sent, so drop it
	// from the incoming map before writing.
	c.updateInFlight(func(s *inFlightState) {
		delete(s.incomingByID, id)
	})

	if bc != nil {
		bc.add(ctx, resp)
		return nil
	}
	return c.writeResponse(ctx, resp.id, resp.result, resp.err)
}

// completeRequest answers req with an error without invoking the handler, used
// to reject calls that arrive while the connection is shutting down.
func (c *conn) completeRequest(ctx context.Context, ir *incomingRequest, bc *batchCollector, result any, err error) {
	reply, _ := c.replier(ir, bc)
	_ = reply(ctx, result, err)
	c.afterHandle(ir)
}

// afterHandle releases the per-request resources and decrements the in-flight
// counter so the connection can progress toward idle.
func (c *conn) afterHandle(ir *incomingRequest) {
	ir.cancel()
	c.updateInFlight(func(s *inFlightState) {
		if ir.isCall {
			delete(s.incomingByID, ir.id)
		}
		s.incoming--
	})
}

// writeInvalid answers a malformed batch member with a null-id error response,
// collecting it into the batch array. It does not touch the in-flight counters
// because a malformed member is never registered as in-flight work.
func (c *conn) writeInvalid(ctx context.Context, bc *batchCollector, err *Error) {
	resp := responseWire{id: ID{}, err: err}
	if bc != nil {
		bc.add(ctx, resp)
		return
	}
	_ = c.writeResponse(ctx, resp.id, resp.result, resp.err)
}

// fail records err as the connection's terminating error and closes the stream
// so the read goroutine unwinds. It is used for handler-returned connection
// errors.
func (c *conn) fail(err error) {
	c.updateInFlight(func(s *inFlightState) {
		if s.writeErr == nil {
			s.writeErr = err
		}
		if s.closer != nil {
			s.closeErr = s.closer.Close()
			s.closer = nil
		}
	})
}

// callID reports the id of req and whether req is a call (and therefore expects
// a response).
func callID(req Request) (id ID, isCall bool) {
	if c, ok := req.(*Call); ok {
		return c.id, true
	}
	return ID{}, false
}
