// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

// Command serve demonstrates serving JSON-RPC requests over a loopback listener.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"go.lsp.dev/jsonrpc2"
)

type symbolParams struct {
	Query string `json:"query"`
}

type symbolResult struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	err := run(ctx, os.Stdout)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve example: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	server := jsonrpc2.HandlerServer(symbolHandler)
	var (
		serveErr error
		wg       sync.WaitGroup
	)
	wg.Go(func() {
		serveErr = jsonrpc2.Serve(ctx, ln, server, 0)
	})

	dialer := net.Dialer{}
	nc, err := dialer.DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		cancel()
		wg.Wait()
		return fmt.Errorf("dial: %w", err)
	}

	client := jsonrpc2.NewConn(jsonrpc2.NewStream(nc))
	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)

	var result symbolResult
	_, err = client.Call(ctx, "workspace/symbol", symbolParams{Query: "jsonrpc2.Conn"}, &result)
	if err != nil {
		_ = client.Close()
		_ = nc.Close()
		cancel()
		wg.Wait()
		return fmt.Errorf("call workspace/symbol: %w", err)
	}

	fmt.Fprintf(out, "symbol: %s (%s)\n", result.Name, result.Kind)

	if err := client.Close(); err != nil {
		_ = nc.Close()
		cancel()
		wg.Wait()
		return fmt.Errorf("close client: %w", err)
	}
	_ = nc.Close()

	cancel()
	wg.Wait()
	if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
		return fmt.Errorf("serve: %w", serveErr)
	}
	return nil
}

func symbolHandler(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	if req.Method() != "workspace/symbol" {
		return jsonrpc2.MethodNotFoundHandler(ctx, req)
	}

	var params symbolParams
	if err := jsonrpc2.DefaultCodec.Unmarshal(req.Params(), &params); err != nil {
		return nil, err
	}

	return symbolResult{
		Name: params.Query,
		Kind: "interface",
	}, nil
}
