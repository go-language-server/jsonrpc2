// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"context"
	"fmt"
	"net"

	"go.lsp.dev/jsonrpc2"
)

// jsonrpc2SyncAdapter drives the jsonrpc2 SyncClient — the synchronous-client
// mode — against an ordinary jsonrpc2 Conn server over a net.Pipe with NDJSON
// framing.
//
// MODE DISCLOSURE (do not quote the number without this qualifier): the
// SyncClient removes the CLIENT's background read goroutine; the calling
// goroutine owns the read loop and reads its own response directly, collapsing
// the dedicated-reader-to-Call hand-off (the third goroutine hop) that the
// concurrent Conn pays. The server is a normal Conn with a background reader, so
// this is a real RPC over a real net.Pipe transport — but it is NOT the same
// execution model as the concurrent Conn (jsonrpc2/native) or as jrpc2's
// channel.Direct, which keep a client-side reader. The tradeoff is that a
// SyncClient cannot receive server-initiated requests and serializes its calls
// (one outstanding at a time). This row is therefore reported as a distinct
// mode, analogous to how the batch rows are excluded from the strict
// apples-to-apples claim; it is "jsonrpc2's fastest in-process request path," not a
// statement that the concurrent Conn got faster.
type jsonrpc2SyncAdapter struct {
	client *jsonrpc2.SyncClient
	server jsonrpc2.Conn
}

func newJSONRPC2SyncAdapter(ctx context.Context) (*jsonrpc2SyncAdapter, error) {
	ca, cb := net.Pipe()
	client, err := jsonrpc2.NewSyncClient(jsonrpc2.NewNDJSONStream(ca))
	if err != nil {
		return nil, fmt.Errorf("jsonrpc2 sync adapter: %w", err)
	}
	server := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(cb))
	server.Go(ctx, func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
		return nil, nil
	})

	a := &jsonrpc2SyncAdapter{client: client, server: server}
	if err := sanityCheck(ctx, a); err != nil {
		_ = a.Close()
		return nil, fmt.Errorf("jsonrpc2 sync adapter: %w", err)
	}
	return a, nil
}

func (a *jsonrpc2SyncAdapter) Call(ctx context.Context, method string, params []byte) ([]byte, error) {
	var result jsonrpc2.RawMessage
	if _, err := a.client.Call(ctx, method, rawParams(params), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *jsonrpc2SyncAdapter) Notify(ctx context.Context, method string, params []byte) error {
	return a.client.Notify(ctx, method, rawParams(params))
}

// Batch is unsupported by the synchronous-client mode (it serializes single
// calls and has no batch-send API); the sync row is not part of the batch
// comparison.
func (a *jsonrpc2SyncAdapter) Batch(ctx context.Context, n int) error {
	return fmt.Errorf("jsonrpc2 sync adapter: Batch is not supported by SyncClient")
}

func (a *jsonrpc2SyncAdapter) Close() error {
	errc := a.client.Close()
	errs := a.server.Close()
	<-a.server.Done()
	if errc != nil {
		return errc
	}
	return errs
}
