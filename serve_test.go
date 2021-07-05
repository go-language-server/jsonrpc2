// SPDX-FileCopyrightText: 2021 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.lsp.dev/pkg/stack/stacktest"

	"go.lsp.dev/jsonrpc2"
)

func TestIdleTimeout(t *testing.T) {
	t.Parallel()
	stacktest.NoLeak(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	listener, err := jsonrpc2.NetListener(ctx, "tcp", "localhost:0", &jsonrpc2.ListenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	listener = jsonrpc2.NewIdleListener(100*time.Millisecond, listener)
	defer listener.Close()

	server, err := jsonrpc2.Serve(ctx, listener, jsonrpc2.Conn{})
	if err != nil {
		t.Fatal(err)
	}

	connect := func() *jsonrpc2.Connection {
		client, err := jsonrpc2.Dial(ctx,
			listener.Dialer(),
			jsonrpc2.Conn{})
		if err != nil {
			t.Fatal(err)
		}

		return client
	}

	// Exercise some connection/disconnection patterns, and then assert that when
	// our timer fires, the server exits.
	conn1 := connect()
	conn2 := connect()

	if err := conn1.Close(); err != nil {
		t.Fatalf("conn1.Close failed with error: %v", err)
	}
	if err := conn2.Close(); err != nil {
		t.Fatalf("conn2.Close failed with error: %v", err)
	}
	conn3 := connect()
	if err := conn3.Close(); err != nil {
		t.Fatalf("conn3.Close failed with error: %v", err)
	}

	if serverError := server.Wait(); !errors.Is(serverError, jsonrpc2.ErrIdleTimeout) {
		t.Fatalf("run() returned error %v, want %v", serverError, jsonrpc2.ErrIdleTimeout)
	}
}

type msg struct {
	Msg string
}

type fakeHandler struct{}

func (fakeHandler) Handle(ctx context.Context, req *jsonrpc2.Request) (interface{}, error) {
	switch req.Method {
	case "ping":
		return &msg{"pong"}, nil
	default:
		return nil, jsonrpc2.ErrNotHandled
	}
}

func TestServe(t *testing.T) {
	t.Parallel()
	stacktest.NoLeak(t)

	tests := map[string]struct {
		factory func(context.Context) (jsonrpc2.Listener, error)
	}{
		"tcp": {
			factory: func(ctx context.Context) (jsonrpc2.Listener, error) {
				return jsonrpc2.NetListener(ctx, "tcp", "localhost:0", &jsonrpc2.ListenOptions{})
			},
		},
		"pipe": {
			factory: jsonrpc2.NetPipe,
		},
	}
	for name, tt := range tests {
		tt := tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			fake, err := tt.factory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			conn, shutdown, err := newFake(t, ctx, fake)
			if err != nil {
				t.Fatal(err)
			}
			defer shutdown()

			var got msg
			if err := conn.Request(ctx, "ping", &msg{"ting"}).Await(ctx, &got); err != nil {
				t.Fatal(err)
			}
			if want := "pong"; got.Msg != want {
				t.Errorf("conn.Call(...): returned %q, want %q", got, want)
			}
		})
	}
}

func newFake(t *testing.T, ctx context.Context, l jsonrpc2.Listener) (*jsonrpc2.Connection, func(), error) {
	t.Helper()

	l = jsonrpc2.NewIdleListener(100*time.Millisecond, l)
	server, err := jsonrpc2.Serve(ctx, l, jsonrpc2.Conn{
		Handler: fakeHandler{},
	})
	if err != nil {
		return nil, nil, err
	}

	client, err := jsonrpc2.Dial(ctx,
		l.Dialer(),
		jsonrpc2.Conn{
			Handler: fakeHandler{},
		})
	if err != nil {
		return nil, nil, err
	}

	return client, func() {
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		if err := server.Wait(); err != nil {
			t.Fatal(err)
		}
	}, nil
}
