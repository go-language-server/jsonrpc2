// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

// Command batch demonstrates sending one raw JSON-RPC batch frame.
package main

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"time"

	"go.lsp.dev/jsonrpc2"
)

type sumParams struct {
	Values []int `json:"values"`
}

type sumResult struct {
	Sum int `json:"sum"`
}

type logParams struct {
	Message string `json:"message"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	err := run(ctx, os.Stdout)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "batch example: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out io.Writer) error {
	clientConn, serverConn := net.Pipe()
	clientStream := jsonrpc2.NewNDJSONStream(clientConn)
	serverStream := jsonrpc2.NewNDJSONStream(serverConn)

	client, err := jsonrpc2.NewBatchClient(clientStream)
	if err != nil {
		_ = clientStream.Close()
		_ = serverStream.Close()
		return fmt.Errorf("new batch client: %w", err)
	}

	logged := make(chan string, 1)
	server := jsonrpc2.NewBatchServer(serverStream)
	server.Go(ctx, batchHandler(logged))

	defer func() {
		_ = client.Close()
		_ = server.Close()
		<-server.Done()
	}()

	batch := jsonrpc2.AppendBatch(nil, []jsonrpc2.Message{
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), "math/sum", jsonrpc2.RawMessage(`{"values":[2,3]}`)),
		jsonrpc2.NewNotification("telemetry/log", jsonrpc2.RawMessage(`{"message":"queued"}`)),
		jsonrpc2.NewCall(jsonrpc2.NewNumberID(2), "math/sum", jsonrpc2.RawMessage(`{"values":[5,8]}`)),
	})

	if _, err := client.WriteFrame(ctx, batch); err != nil {
		return fmt.Errorf("write batch: %w", err)
	}

	frame, _, err := client.ReadFrame(ctx)
	if err != nil {
		return fmt.Errorf("read batch response: %w", err)
	}
	responses, err := decodeBatchResults(frame)
	if err != nil {
		return err
	}

	var loggedMessage string
	select {
	case loggedMessage = <-logged:
	case <-ctx.Done():
		return fmt.Errorf("wait for telemetry notification: %w", ctx.Err())
	}

	ids := make([]int, 0, len(responses))
	for id := range responses {
		ids = append(ids, int(id))
	}
	slices.Sort(ids)

	fmt.Fprintf(out, "batch responses: %d\n", len(responses))
	for _, id := range ids {
		fmt.Fprintf(out, "sum[%d]: %d\n", id, responses[int64(id)].Sum)
	}
	fmt.Fprintf(out, "notification: %s\n", loggedMessage)
	return nil
}

func batchHandler(logged chan<- string) jsonrpc2.Handler {
	return func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
		switch req.Method() {
		case "math/sum":
			var params sumParams
			if err := jsonrpc2.DefaultCodec.Unmarshal(req.Params(), &params); err != nil {
				return nil, err
			}
			sum := 0
			for _, value := range params.Values {
				sum += value
			}
			return sumResult{Sum: sum}, nil

		case "telemetry/log":
			var params logParams
			if err := jsonrpc2.DefaultCodec.Unmarshal(req.Params(), &params); err != nil {
				return nil, err
			}
			select {
			case logged <- params.Message:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return nil, nil

		default:
			return jsonrpc2.MethodNotFoundHandler(ctx, req)
		}
	}
}

func decodeBatchResults(frame []byte) (map[int64]sumResult, error) {
	var members []stdjson.RawMessage
	if err := stdjson.Unmarshal(frame, &members); err != nil {
		return nil, fmt.Errorf("decode batch response array: %w", err)
	}

	out := make(map[int64]sumResult, len(members))
	for _, member := range members {
		msg, err := jsonrpc2.DecodeMessage(member)
		if err != nil {
			return nil, fmt.Errorf("decode batch response member: %w", err)
		}
		resp, ok := msg.(*jsonrpc2.Response)
		if !ok {
			return nil, fmt.Errorf("batch member is %T, want *jsonrpc2.Response", msg)
		}
		if err := resp.Err(); err != nil {
			return nil, fmt.Errorf("batch response %v: %w", resp.ID(), err)
		}
		id, ok := resp.ID().Number()
		if !ok {
			return nil, fmt.Errorf("batch response id %v is not numeric", resp.ID())
		}
		var result sumResult
		if err := jsonrpc2.DefaultCodec.Unmarshal(resp.Result(), &result); err != nil {
			return nil, fmt.Errorf("decode result for response %d: %w", id, err)
		}
		out[id] = result
	}
	return out, nil
}
