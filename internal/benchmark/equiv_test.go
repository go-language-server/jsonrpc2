// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"testing"

	jrpc2 "github.com/creachadair/jrpc2"
	gocmp "github.com/google/go-cmp/cmp"

	"go.lsp.dev/jsonrpc2"
	mcp "go.lsp.dev/jsonrpc2/internal/benchmark/mcpref"
)

// decoded captures the library-independent view of a parsed single request, so
// the three decoders can be compared on identical input. It is the anti-gaming
// guard for the pure-decode benchmarks: it proves jsonrpc2, jrpc2, and mcp all do
// the same parsing work (same method, same params bytes) rather than one library
// cheating by skipping fields.
type decoded struct {
	method string
	params string
}

// TestDecodeEquivalence verifies that jsonrpc2.DecodeMessage, jrpc2.ParseRequests,
// and mcp.DecodeMessage extract the same method name and params bytes from
// identical single-message inputs. It uses go-cmp to compare the normalized
// views.
func TestDecodeEquivalence(t *testing.T) {
	tests := map[string]struct {
		input  string
		method string
		params string
	}{
		"success: minimal call": {
			input:  `{"jsonrpc":"2.0","id":1,"method":"Foo.Bar","params":[1,2,3]}`,
			method: "Foo.Bar",
			params: "[1,2,3]",
		},
		"success: object params": {
			input:  `{"jsonrpc":"2.0","id":7,"method":"do.it","params":{"x":99}}`,
			method: "do.it",
			params: `{"x":99}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			msg := []byte(tt.input)
			want := decoded{method: tt.method, params: tt.params}

			// jsonrpc2
			jsonrpc2Msg, err := jsonrpc2.DecodeMessage(msg)
			if err != nil {
				t.Fatalf("jsonrpc2.DecodeMessage: %v", err)
			}
			jsonrpc2Req, ok := jsonrpc2Msg.(jsonrpc2.Request)
			if !ok {
				t.Fatalf("jsonrpc2.DecodeMessage: not a request: %T", jsonrpc2Msg)
			}
			gotJSONRPC2 := decoded{method: jsonrpc2Req.Method(), params: string(jsonrpc2Req.Params())}
			if diff := gocmp.Diff(want, gotJSONRPC2, gocmp.AllowUnexported(decoded{})); diff != "" {
				t.Errorf("jsonrpc2 decode mismatch (-want +got):\n%s", diff)
			}

			// jrpc2
			jreqs, err := jrpc2.ParseRequests(msg)
			if err != nil {
				t.Fatalf("jrpc2.ParseRequests: %v", err)
			}
			if len(jreqs) != 1 {
				t.Fatalf("jrpc2.ParseRequests: got %d requests, want 1", len(jreqs))
			}
			gotJRPC2 := decoded{method: jreqs[0].Method, params: string(jreqs[0].Params)}
			if diff := gocmp.Diff(want, gotJRPC2, gocmp.AllowUnexported(decoded{})); diff != "" {
				t.Errorf("jrpc2 decode mismatch (-want +got):\n%s", diff)
			}

			// mcp
			mcpMsg, err := mcp.DecodeMessage(msg)
			if err != nil {
				t.Fatalf("mcp.DecodeMessage: %v", err)
			}
			mcpReq, ok := mcpMsg.(*mcp.Request)
			if !ok {
				t.Fatalf("mcp.DecodeMessage: not a request: %T", mcpMsg)
			}
			gotMCP := decoded{method: mcpReq.Method, params: string(mcpReq.Params)}
			if diff := gocmp.Diff(want, gotMCP, gocmp.AllowUnexported(decoded{})); diff != "" {
				t.Errorf("mcp decode mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
