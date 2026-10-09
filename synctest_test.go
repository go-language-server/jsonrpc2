// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2_test

import (
	"context"
	"net"
	"testing"
	"testing/synctest"

	"go.lsp.dev/jsonrpc2"
)

// TestCallWaitersStayInBubble checks that calls made outside a synctest bubble
// never receive a waiter, and so a channel, created by calls inside one.
func TestCallWaitersStayInBubble(t *testing.T) {
	for _, tt := range []struct {
		name      string
		newClient func(jsonrpc2.Stream) jsonrpc2.Conn
	}{
		{"Conn", func(s jsonrpc2.Stream) jsonrpc2.Conn { return jsonrpc2.NewConn(s) }},
		{"PipelineClient", func(s jsonrpc2.Stream) jsonrpc2.Conn { return jsonrpc2.NewPipelineClient(s) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				echoCalls(t, tt.newClient, "bubbled")
			})
			// The pool is per-P, so call enough times to draw any waiter the
			// bubble left behind.
			echoCalls(t, tt.newClient, "unbubbled")
		})
	}
}

// echoCalls makes calls from a client built by newClient to a server that
// echoes their params.
func echoCalls(t *testing.T, newClient func(jsonrpc2.Stream) jsonrpc2.Conn, name string) {
	t.Helper()
	ctx := t.Context()

	ca, cb := net.Pipe()
	client := newClient(jsonrpc2.NewNDJSONStream(ca))
	server := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(cb))
	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	server.Go(ctx, func(_ context.Context, req *jsonrpc2.Request) (any, error) {
		return jsonrpc2.RawMessage(string(req.Params())), nil
	})
	t.Cleanup(func() {
		_ = client.Close()
		<-client.Done()
		_ = server.Close()
		<-server.Done()
	})

	type payload struct {
		Name string `json:"name"`
	}
	for range 32 {
		var got payload
		if _, err := client.Call(ctx, "echo", payload{Name: name}, &got); err != nil {
			t.Fatalf("Call: %v", err)
		}
		if got.Name != name {
			t.Fatalf("Call result name = %q, want %q", got.Name, name)
		}
	}
}
