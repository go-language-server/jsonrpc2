// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

// Command peer demonstrates a bidirectional in-process JSON-RPC connection.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"go.lsp.dev/jsonrpc2"
)

type hoverParams struct {
	URI  string `json:"uri"`
	Line int    `json:"line"`
}

type hoverResult struct {
	Contents string `json:"contents"`
}

type saveParams struct {
	URI string `json:"uri"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	err := run(ctx, os.Stdout)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "peer example: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out io.Writer) error {
	clientStream, serverStream := jsonrpc2.NewChannelStreamPair(4)
	client := jsonrpc2.NewPeer(clientStream)
	server := jsonrpc2.NewPeer(serverStream)

	saved := make(chan string, 1)
	server.Go(ctx, peerHandler(saved))
	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)

	defer func() {
		_ = client.Close()
		<-client.Done()
		_ = server.Close()
		<-server.Done()
	}()

	var hover hoverResult
	_, err := client.Call(ctx, "textDocument/hover", hoverParams{
		URI:  "file:///hello.go",
		Line: 7,
	}, &hover)
	if err != nil {
		return fmt.Errorf("call hover: %w", err)
	}

	if err := client.Notify(ctx, "textDocument/didSave", saveParams{URI: "file:///hello.go"}); err != nil {
		return fmt.Errorf("notify didSave: %w", err)
	}

	var savedURI string
	select {
	case savedURI = <-saved:
	case <-ctx.Done():
		return fmt.Errorf("wait for didSave notification: %w", ctx.Err())
	}

	fmt.Fprintf(out, "hover: %s\n", hover.Contents)
	fmt.Fprintf(out, "saved: %s\n", savedURI)
	return nil
}

func peerHandler(saved chan<- string) jsonrpc2.Handler {
	return func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
		switch req.Method() {
		case "textDocument/hover":
			var params hoverParams
			if err := jsonrpc2.DefaultCodec.Unmarshal(req.Params(), &params); err != nil {
				return nil, err
			}
			return hoverResult{
				Contents: fmt.Sprintf("hover for %s:%d", params.URI, params.Line),
			}, nil

		case "textDocument/didSave":
			var params saveParams
			if err := jsonrpc2.DefaultCodec.Unmarshal(req.Params(), &params); err != nil {
				return nil, err
			}
			select {
			case saved <- params.URI:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return nil, nil

		default:
			return jsonrpc2.MethodNotFoundHandler(ctx, req)
		}
	}
}
