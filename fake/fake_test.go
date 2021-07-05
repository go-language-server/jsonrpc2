// SPDX-FileCopyrightText: 2021 The Go Language Server Authors
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

func fakeHandler(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	return reply(ctx, &msg{"pong"}, nil)
}

func TestTestServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	server := jsonrpc2.HandlerServer(fakeHandler)

	tcpTS := fake.NewTCPServer(ctx, server, nil)
	defer tcpTS.Close()

	pipeTS := fake.NewPipeServer(ctx, server, nil)
	defer pipeTS.Close()

	tests := map[string]struct {
		connector fake.Connector
	}{
		"tcp": {
			connector: tcpTS,
		},
		"pipe": {
			connector: pipeTS,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			conn := tt.connector.Connect(ctx)
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
