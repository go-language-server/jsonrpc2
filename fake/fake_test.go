// Copyright 2020 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

package fake_test

import (
	"context"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/jsonrpc2/fake"
)

type msg struct {
	Msg string
}

func fakeHandler(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Requester) error {
	return reply(ctx, &msg{"pong"}, nil)
}

func TestTestServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server := jsonrpc2.HandlerServer(fakeHandler)
	tcp := fake.NewTCPServer(ctx, server, nil)
	defer tcp.Close()

	tests := []struct {
		name      string
		connector fake.Connector
	}{
		{"tcp", tcp},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := test.connector.Connect(ctx)
			conn.Go(ctx, jsonrpc2.MethodNotFoundHandler)

			var got msg
			if _, err := conn.Call(ctx, "ping", &msg{"ping"}, &got); err != nil {
				t.Fatal(err)
			}

			if want := "pong"; got.Msg != want {
				t.Errorf("conn.Call(...): returned %q, want %q", got, want)
			}
		})
	}
}
