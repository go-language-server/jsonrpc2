// SPDX-FileCopyrightText: 2021 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Listener is implemented by protocols to accept new inbound connections.
type Listener interface {
	// Accept an inbound connection to a server.
	// It must block until an inbound connection is made, or the listener is
	// shut down.
	Accept(ctx context.Context) (rwc io.ReadWriteCloser, err error)

	// Close is used to ask a listener to stop accepting new connections.
	Close() error

	// Dialer returns a dialer that can be used to connect to this listener
	// locally.
	// If a listener does not implement this it will return a nil.
	Dialer() Dialer
}

// Dialer is used by clients to dial a server.
type Dialer interface {
	// Dial returns a new communication byte stream to a listening server.
	Dial(ctx context.Context) (rwc io.ReadWriteCloser, err error)
}

// Dial uses the dialer to make a new connection, wraps the returned
// reader and writer using the framer to make a stream, and then builds
// a connection on top of that stream using the binder.
func Dial(ctx context.Context, dialer Dialer, binder Binder) (*Connection, error) {
	// dial a server
	rwc, err := dialer.Dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	return newConnection(ctx, rwc, binder)
}

// async is a small helper for things with an asynchronous result that you can
// wait for.
type async struct {
	ready chan struct{}
	errs  chan error
}

func (a *async) init() {
	a.ready = make(chan struct{})
	a.errs = make(chan error, 1)
	a.errs <- nil
}

func (a *async) done() {
	close(a.ready)
}

func (a *async) isDone() bool {
	select {
	case <-a.ready:
		return true
	default:
		return false
	}
}

func (a *async) wait() error {
	<-a.ready
	err := <-a.errs
	a.errs <- err

	return err
}

func (a *async) setError(err error) {
	storedErr := <-a.errs
	if storedErr == nil {
		storedErr = err
	}
	a.errs <- storedErr
}

// Server is a running server that is accepting incoming connections.
type Server struct {
	listener Listener
	binder   Binder
	async    async
}

// Serve starts a new server listening for incoming connections and returns
// it.
//
// This returns a fully running and connected server, it does not block on
// the listener.
// It can call Wait to block on the server, or Shutdown to get the sever to
// terminate gracefully.
//
// To notice incoming connections, use an intercepting Binder.
//nolint:unparam // reason: feature use
func Serve(ctx context.Context, listener Listener, binder Binder) (*Server, error) {
	server := &Server{
		listener: listener,
		binder:   binder,
	}
	server.async.init()
	go server.run(ctx)

	return server, nil
}

// Wait returns only when the server has shut down.
func (s *Server) Wait() error {
	return s.async.wait()
}

// run accepts incoming connections from the listener,
//
// If IdleTimeout is non-zero, run exits after there are no clients for this
// duration, otherwise it exits only on error.
func (s *Server) run(ctx context.Context) {
	defer s.async.done()

	//nolint:prealloc // reason false positive
	var activeConns []*Connection
	for {
		// never close the accepted connection, rely on the other end
		// closing or the socket closing itself naturally
		rwc, err := s.listener.Accept(ctx)
		if err != nil {
			if !IsClosingError(err) {
				s.async.setError(err)
			}
			// done generating new connections for good
			break
		}

		// see if any connections were closed while waiting
		activeConns = onlyActive(activeConns)

		// a new inbound connection,
		conn, err := newConnection(ctx, rwc, s.binder)
		if err != nil {
			if !IsClosingError(err) {
				s.async.setError(err)
			}

			continue
		}
		activeConns = append(activeConns, conn)
	}

	// wait for all active conns to finish
	for _, c := range activeConns {
		//nolint:errcheck
		c.Wait()
	}
}

func onlyActive(conns []*Connection) []*Connection {
	i := 0
	for _, c := range conns {
		if !c.async.isDone() {
			conns[i] = c
			i++
		}
	}

	// trim the slice down
	return conns[:i]
}

// IsClosingError reports if the error occurs normally during the process of
// closing a network connection.
//
// It uses imperfect heuristics that err on the side of false negatives,
// and should not be used for anything critical.
func IsClosingError(err error) bool {
	if err == nil {
		return false
	}

	// fully unwrap the error, so the following tests work.
	for wrapped := err; wrapped != nil; wrapped = errors.Unwrap(err) {
		err = wrapped
	}

	//nolint:errorlint
	switch err {
	case io.EOF:
		// was it based on an EOF error?
		return true

	case io.ErrClosedPipe:
		// was it based on a closed pipe?
		return true

	default:
		// per https://github.com/golang/go/issues/4373, this error string should not
		// change. This is not ideal, but since the worst that could happen here is
		// some superfluous logging, it is acceptable.
		if err.Error() == "use of closed network connection" {
			return true
		}
	}

	return false
}

type idleCloser struct {
	closeOnce sync.Once
	listen    io.ReadWriteCloser
	closed    chan struct{}
}

// make sure idleCloser implements the io.ReadWriteCloser interface.
var _ io.ReadWriteCloser = (*idleCloser)(nil)

// Read implements io.ReadWriteCloser.Read.
//nolint:wrapcheck
func (c *idleCloser) Read(p []byte) (int, error) {
	n, err := c.listen.Read(p)
	if err != nil && IsClosingError(err) {
		c.closeOnce.Do(func() {
			close(c.closed)
		})
	}

	return n, err
}

// Write implements io.ReadWriteCloser.Write.
//nolint:wrapcheck
func (c *idleCloser) Write(p []byte) (int, error) {
	// do not close on write failure. rely on the wrapped writer to do that
	// if it is appropriate, which will detect in the next read.
	return c.listen.Write(p)
}

// Close implements io.ReadWriteCloser.Close.
//nolint:wrapcheck
func (c *idleCloser) Close() error {
	// rely on closing the wrapped stream to signal to the next read that closed,
	// rather than triggering the closed signal directly
	return c.listen.Close()
}

// NewIdleListener wraps a listener with an idle timeout.
//
// When there are no active connections for at least the timeout duration a
// call to accept will fail with ErrIdleTimeout.
func NewIdleListener(timeout time.Duration, wrap Listener) Listener {
	l := &idleListener{
		timeout:    timeout,
		listen:     wrap,
		newConns:   make(chan *idleCloser),
		closed:     make(chan struct{}),
		wasTimeout: make(chan struct{}),
	}
	go l.run()

	return l
}

type idleListener struct {
	closeOnce  sync.Once
	listen     Listener
	newConns   chan *idleCloser
	closed     chan struct{}
	wasTimeout chan struct{}
	timeout    time.Duration
}

// make sure idleListener implements the Listener interface.
var _ Listener = (*idleListener)(nil)

// Accept implements Listener.Accept.
func (l *idleListener) Accept(ctx context.Context) (io.ReadWriteCloser, error) {
	rwc, err := l.listen.Accept(ctx)
	if err != nil {
		if IsClosingError(err) {
			// underlying listener was closed
			l.closeOnce.Do(func() {
				close(l.closed)
			})

			select {
			case <-l.wasTimeout:
				// was it closed because of the idle timeout?
				err = ErrIdleTimeout
			default:
			}
		}

		return nil, err
	}

	conn := &idleCloser{
		listen: rwc,
		closed: make(chan struct{}),
	}
	l.newConns <- conn

	return conn, err
}

// Close implements Listener.Close.
//nolint:wrapcheck
func (l *idleListener) Close() error {
	defer l.closeOnce.Do(func() {
		close(l.closed)
	})

	return l.listen.Close()
}

// Dialer implements Listener.Dialer.
func (l *idleListener) Dialer() Dialer {
	return l.listen.Dialer()
}

func (l *idleListener) run() {
	var conns []*idleCloser
	for {
		var firstClosed chan struct{} // left at nil if there are no active conns
		var timeout <-chan time.Time  // left at nil if there are  active conns

		if len(conns) > 0 {
			firstClosed = conns[0].closed
		} else {
			timeout = time.After(l.timeout)
		}

		select {
		case <-l.closed:
			// the main listener closed, no need to keep going
			return

		case conn := <-l.newConns:
			// a new conn arrived, add it to the list
			conns = append(conns, conn)

		case <-timeout:
			// timed out, only happens when there are no active conns
			// close the underlying listener, and allow the normal closing process to happen
			close(l.wasTimeout)
			l.listen.Close()

		case <-firstClosed:
			// a conn closed, remove it from the active list
			conns = conns[:copy(conns, conns[1:])]
		}
	}
}
