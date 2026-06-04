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
	// request is queued for the handler. A request it handles (any error other
	// than ErrNotHandled) is answered here and never reaches the handler.
	if c.preempter != nil {
		result, perr := c.preempter.Preempt(ir, req)
		if !errors.Is(perr, ErrNotHandled) {
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
	// (ch left nil), so dispatch allocates the releaser but no per-request closure.
	rel := &releaser{conn: c, ctx: ctx, handler: handler}
	ir.rel = rel

	reply, replied := c.replier(ir, nil)
	c.runHandler(handler, ir, rel, reply, replied)
	// handedOff is set by a hard release (Async) on this same read goroutine
	// before runHandler returns, so the read carries no data race.
	return rel.handedOff
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

	rel := &releaser{ch: make(chan struct{})}
	ir.rel = rel

	reply, replied := c.replier(ir, bc)

	go c.runHandler(handler, ir, rel, reply, replied)

	// Block until the member releases the dispatch loop: immediately for an async
	// member (via Async), or when the handler returns for a synchronous one.
	<-rel.ch
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

	flag := &repliedFlag{}
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
			return merr
		}
		resp.result = raw
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
	return c.write(ctx, resp)
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
	_ = c.write(ctx, resp)
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
