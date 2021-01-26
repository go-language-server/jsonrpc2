// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright 2021 The Go Language Server Authors

package jsonrpc2

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestIdleTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	connect := func() net.Conn {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
		if err != nil {
			panic(err)
		}
		return conn
	}

	server := HandlerServer(MethodNotFoundHandler)
	// connTimer := &fakeTimer{c: make(chan time.Time, 1)}
	var (
		runErr error
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr = Serve(ctx, ln, server, 100*time.Millisecond)
	}()

	// Exercise some connection/disconnection patterns, and then assert that when
	// our timer fires, the server exits.
	conn1 := connect()
	conn2 := connect()
	conn1.Close()
	conn2.Close()
	conn3 := connect()
	conn3.Close()

	wg.Wait()

	if !errors.Is(runErr, ErrIdleTimeout) {
		t.Errorf("run() returned error %v, want %v", runErr, ErrIdleTimeout)
	}
}