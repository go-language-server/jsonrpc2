// SPDX-FileCopyrightText: 2021 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"go.lsp.dev/pkg/fakenet"
)

// This file contains implementations of the transport primitives that use the standard network
// package.

// ListenOptions is the optional arguments to the NetListen function.
type ListenOptions struct {
	NetDialer       net.Dialer
	NetListenConfig net.ListenConfig
}

// netListener is the implementation of Listener for connections made using the net package.
type netListener struct {
	listen net.Listener
}

// make sure netListener implements the Listener interface.
var _ Listener = (*netListener)(nil)

// NetListener returns a new Listener that listens on a socket using the net package.
func NetListener(ctx context.Context, network, address string, opt *ListenOptions) (Listener, error) {
	ln, err := opt.NetListenConfig.Listen(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	return &netListener{listen: ln}, nil
}

// Accept blocks waiting for an incoming connection to the listener.
//
// Accept implements Listener.Accept.
func (l *netListener) Accept(ctx context.Context) (io.ReadWriteCloser, error) {
	conn, err := l.listen.Accept()
	if err != nil {
		return nil, fmt.Errorf("failed to accept: %w", err)
	}

	return conn, nil
}

// Close will cause the listener to stop listening. It will not close any connections that have
// already been accepted.
//
// Close implements Listener.Close.
func (l *netListener) Close() error {
	addr := l.listen.Addr()

	err := l.listen.Close()
	if addr.Network() == "unix" {
		rerr := os.Remove(addr.String())
		if rerr != nil && err == nil {
			err = rerr
		}
	}

	return err
}

// Dialer returns a dialer that can be used to connect to the listener.
//
// Dialer implements Listener.Dialer.
func (l *netListener) Dialer() Dialer {
	return NetDialer(l.listen.Addr().Network(), l.listen.Addr().String(), (&net.Dialer{
		Timeout: 5 * time.Second,
	}))
}

type netDialer struct {
	dialer  *net.Dialer
	network string
	address string
}

// make sure netDialer implements the Dialer interface.
var _ Dialer = (*netDialer)(nil)

// NetDialer returns a Dialer using the supplied standard network dialer.
func NetDialer(network, address string, nd *net.Dialer) Dialer {
	return &netDialer{
		network: network,
		address: address,
		dialer:  nd,
	}
}

// Dial implements Dialer.Dial.
func (n *netDialer) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	conn, err := n.dialer.DialContext(ctx, n.network, n.address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	return conn, nil
}

// netPiper is the implementation of Listener build on top of net.Pipes.
type netPiper struct {
	done   chan struct{}
	dialed chan io.ReadWriteCloser
}

// make sure netPiper implements the Listener and Dialer interfaces.
var (
	_ Listener = (*netPiper)(nil)
	_ Dialer   = (*netPiper)(nil)
)

// NetPipe returns a new Listener that listens using net.Pipe.
//
// It is only possibly to connect to it using the Dialier returned by the
// Dialer method, each call to that method will generate a new pipe the other
// side of which will be returned from the Accept call.
//nolint:unparam // reason: future use
func NetPipe(context.Context) (Listener, error) {
	return &netPiper{
		done:   make(chan struct{}),
		dialed: make(chan io.ReadWriteCloser),
	}, nil
}

// Accept blocks waiting for an incoming connection to the listener.
//
// Accept implements Listener.Accept.
func (l *netPiper) Accept(ctx context.Context) (io.ReadWriteCloser, error) {
	// block until we have a listener, or are closed or cancelled
	select {
	case rwc := <-l.dialed:
		return rwc, nil

	case <-l.done:
		return nil, io.EOF

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close will cause the listener to stop listening. It will not close any connections that have
// already been accepted.
//
// Close implements Listener.Close.
func (l *netPiper) Close() error {
	// unblock any accept calls that are pending
	close(l.done)

	return nil
}

// Dialer implements Listener.Dialer.
func (l *netPiper) Dialer() Dialer {
	return l
}

// Dial implements Dialer.Dial.
func (l *netPiper) Dial(context.Context) (io.ReadWriteCloser, error) {
	c, w := net.Pipe()
	l.dialed <- w

	return c, nil
}

// ioPiper is the implementation of Listener build on top of io.ReadCloser and io.WriteCloser.
type ioPiper struct {
	done   chan struct{}
	dialed chan io.ReadWriteCloser
	conn   net.Conn
}

// make sure ioPiper implements the Listener interface.
var (
	_ Listener = (*ioPiper)(nil)
	_ Dialer   = (*ioPiper)(nil)
)

// IOPipe returns a new Listener that listens using r and w pipe.
//nolint:unparam // reason: future use
func IOPipe(_ context.Context, r io.ReadCloser, w io.WriteCloser) (Listener, error) {
	return &ioPiper{
		done:   make(chan struct{}),
		dialed: make(chan io.ReadWriteCloser),
		conn:   fakenet.NewConn("stdio", r, w),
	}, nil
}

// Accept blocks waiting for an incoming connection to the listener.
//
// Accept implements Listener.Accept.
func (l *ioPiper) Accept(ctx context.Context) (io.ReadWriteCloser, error) {
	// block until we have a listener, or are closed or cancelled
	select {
	case rwc := <-l.dialed:
		return rwc, nil

	case <-l.done:
		return nil, io.EOF

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close will cause the listener to stop listening. It will not close any connections that have
// already been accepted.
//
// Close implements Listener.Close.
func (l *ioPiper) Close() error {
	// unblock any accept calls that are pending
	close(l.done)

	//nolint:wrapcheck // reason: return net.Conn.Close error directly
	return l.conn.Close()
}

// Dialer implements Listener.Dialer.
func (l *ioPiper) Dialer() Dialer {
	return l
}

// Dial implements Dialer.Dial.
func (l *ioPiper) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	l.dialed <- l.conn

	return l.conn, nil
}
