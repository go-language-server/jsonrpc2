// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import "testing"

func TestScanPipelineResultResponse(t *testing.T) {
	t.Parallel()

	id, result, ok, err := scanPipelineResultResponse([]byte(`{"jsonrpc":"2.0","id":42,"result":{"ok":true}}`))
	if err != nil {
		t.Fatalf("scanPipelineResultResponse error: %v", err)
	}
	if !ok {
		t.Fatal("scanPipelineResultResponse ok = false, want true")
	}
	if got, ok := id.Number(); !ok || got != 42 {
		t.Fatalf("id = %v, %v; want 42, true", got, ok)
	}
	if string(result) != `{"ok":true}` {
		t.Fatalf("result = %q, want object", result)
	}
}

func TestScanPipelineResultResponseFallbacks(t *testing.T) {
	t.Parallel()

	for _, frame := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"missing"}}`),
		[]byte(`{"jsonrpc":"2.0","id":0,"result":null}`),
		[]byte(`{"jsonrpc":"2.0","id":"1","result":null}`),
		[]byte(`{"jsonrpc":"2.0","id":9223372036854775808,"result":null}`),
		[]byte(`{"id":1,"jsonrpc":"2.0","result":null}`),
		[]byte(`[{"jsonrpc":"2.0","id":1,"result":null}]`),
	} {
		if _, _, ok, err := scanPipelineResultResponse(frame); ok || err != nil {
			t.Fatalf("scanPipelineResultResponse(%q) = ok %v, err %v; want fallback", frame, ok, err)
		}
	}
}

func TestScanPipelineResultResponseInvalid(t *testing.T) {
	t.Parallel()

	for _, frame := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"result":`),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":null} trailing`),
	} {
		if _, _, ok, err := scanPipelineResultResponse(frame); ok || err == nil {
			t.Fatalf("scanPipelineResultResponse(%q) = ok %v, err %v; want invalid/parse", frame, ok, err)
		}
	}
}
