// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"context"
	"fmt"
	"net"

	"go.lsp.dev/jsonrpc2"
)

// oursSyncAdapter drives the ours SyncClient — the A1c synchronous-client mode —
// against an ordinary ours Conn server over a net.Pipe with NDJSON framing.
//
// MODE DISCLOSURE (do not quote the number without this qualifier): the
// SyncClient removes the CLIENT's background read goroutine; the calling
// goroutine owns the read loop and reads its own response directly, collapsing
// the dedicated-reader-to-Call hand-off (the third goroutine hop) that the
// concurrent Conn pays. The server is a normal Conn with a background reader, so
// this is a real RPC over a real net.Pipe transport — but it is NOT the same
// execution model as the concurrent Conn (ours/native) or as jrpc2's
// channel.Direct, which keep a client-side reader. The tradeoff is that a
// SyncClient cannot receive server-initiated requests and serializes its calls
// (one outstanding at a time). This row is therefore reported as a distinct
// mode, analogous to how the batch rows are excluded from the strict
// apples-to-apples claim; it is "ours' fastest in-process request path," not a
// statement that the concurrent Conn got faster.
type oursSyncAdapter struct {
	client *jsonrpc2.SyncClient
	server jsonrpc2.Conn
}

func newOursSyncAdapter(ctx context.Context) (*oursSyncAdapter, error) {
	ca, cb := net.Pipe()
	client, err := jsonrpc2.NewSyncClient(jsonrpc2.NewNDJSONStream(ca))
	if err != nil {
		return nil, fmt.Errorf("ours sync adapter: %w", err)
	}
	server := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(cb))
	server.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		return reply(ctx, nil, nil)
	})

	a := &oursSyncAdapter{client: client, server: server}
	if err := sanityCheck(ctx, a); err != nil {
		_ = a.Close()
		return nil, fmt.Errorf("ours sync adapter: %w", err)
	}
	return a, nil
}

func (a *oursSyncAdapter) Call(ctx context.Context, method string, params []byte) ([]byte, error) {
	var result jsonrpc2.RawMessage
	if _, err := a.client.Call(ctx, method, rawParams(params), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *oursSyncAdapter) Notify(ctx context.Context, method string, params []byte) error {
	return a.client.Notify(ctx, method, rawParams(params))
}

// Batch is unsupported by the synchronous-client mode (it serializes single
// calls and has no batch-send API); the sync row is not part of the batch
// comparison.
func (a *oursSyncAdapter) Batch(ctx context.Context, n int) error {
	return fmt.Errorf("ours sync adapter: Batch is not supported by SyncClient")
}

func (a *oursSyncAdapter) Close() error {
	errc := a.client.Close()
	errs := a.server.Close()
	<-a.server.Done()
	if errc != nil {
		return errc
	}
	return errs
}
