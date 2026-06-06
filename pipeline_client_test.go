// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import "testing"

func TestScanPipelineResultResponse(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		frame  string
		id     int64
		result string
	}{
		{
			name:   "object",
			frame:  `{"jsonrpc":"2.0","id":42,"result":{"ok":true}}`,
			id:     42,
			result: `{"ok":true}`,
		},
		{
			name:   "null",
			frame:  `{"jsonrpc":"2.0","id":43,"result":null}`,
			id:     43,
			result: `null`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id, result, ok, err := scanPipelineResultResponse([]byte(tt.frame))
			if err != nil {
				t.Fatalf("scanPipelineResultResponse error: %v", err)
			}
			if !ok {
				t.Fatal("scanPipelineResultResponse ok = false, want true")
			}
			if got, ok := id.Number(); !ok || got != tt.id {
				t.Fatalf("id = %v, %v; want %d, true", got, ok, tt.id)
			}
			if string(result) != tt.result {
				t.Fatalf("result = %q, want %q", result, tt.result)
			}
		})
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
