// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"errors"
	"testing"

	"github.com/buger/jsonparser"
	"github.com/tidwall/gjson"
	"github.com/valyala/fastjson"
	"go.lsp.dev/jsonrpc2"
)

var envelopeSelectorSink int

// envelopeSelectorInputs are single-message JSON-RPC frames used to compare
// benchmark-only envelope selector dependencies against the package's borrowed
// scanner. They intentionally avoid batch arrays; batch behavior is covered by
// ParseRequests/ScanRequestViews because a selector that only extracts top-level
// object fields is not a batch parser.
var envelopeSelectorInputs = []struct {
	name  string
	frame []byte
}{
	{"CallMinimal", []byte(`{"jsonrpc":"2.0","id":1,"method":"Foo.Bar","params":null}`)},
	{"CallStringID", []byte(`{"jsonrpc":"2.0","id":"abc-123","method":"textDocument/hover","params":{"line":12,"character":4}}`)},
	{"Notification", []byte(`{"jsonrpc":"2.0","method":"$/progress","params":{"token":"x","value":{"kind":"begin"}}}`)},
	{"ResponseResult", []byte(`{"jsonrpc":"2.0","id":7,"result":{"ok":true,"items":[1,2,3]}}`)},
	{"ResponseError", []byte(`{"jsonrpc":"2.0","id":7,"error":{"code":-32602,"message":"invalid params","data":{"field":"uri"}}}`)},
	{"EscapedMethod", []byte(`{"jsonrpc":"2.0","id":8,"method":"workspace/execute\\nCommand","params":{"command":"x"}}`)},
	{"LargeParams", []byte(`{"jsonrpc":"2.0","id":9,"method":"textDocument/didChange","params":{"text":"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua."}}`)},
}

func BenchmarkEnvelopeSelectorProbe(b *testing.B) {
	for _, in := range envelopeSelectorInputs {
		frame := in.frame

		b.Run(in.name+"/scan-view", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				view, err := jsonrpc2.ScanMessageView(frame)
				if err != nil {
					b.Fatalf("ScanMessageView: %v", err)
				}
				envelopeSelectorSink += int(view.Kind) + len(view.MethodBytes) + len(view.IDRaw) + len(view.Result) + len(view.ErrorRaw)
			}
		})

		b.Run(in.name+"/fastjson", func(b *testing.B) {
			var parser fastjson.Parser
			b.ReportAllocs()
			for b.Loop() {
				v, err := parser.ParseBytes(frame)
				if err != nil {
					b.Fatalf("fastjson.ParseBytes: %v", err)
				}
				method := v.GetStringBytes("method")
				id := v.Get("id")
				result := v.Get("result")
				errValue := v.Get("error")
				envelopeSelectorSink += len(method) + fastjsonValueScore(id) + fastjsonValueScore(result) + fastjsonValueScore(errValue)
			}
		})

		b.Run(in.name+"/jsonparser", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				method, methodType, err := jsonparserOptional(frame, "method")
				if err != nil {
					b.Fatalf("jsonparser method: %v", err)
				}
				id, idType, err := jsonparserOptional(frame, "id")
				if err != nil {
					b.Fatalf("jsonparser id: %v", err)
				}
				result, resultType, err := jsonparserOptional(frame, "result")
				if err != nil {
					b.Fatalf("jsonparser result: %v", err)
				}
				errRaw, errType, err := jsonparserOptional(frame, "error")
				if err != nil {
					b.Fatalf("jsonparser error: %v", err)
				}
				envelopeSelectorSink += len(method) + int(methodType) + len(id) + int(idType) + len(result) + int(resultType) + len(errRaw) + int(errType)
			}
		})

		b.Run(in.name+"/gjson", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				results := gjson.GetManyBytes(frame, "method", "id", "result", "error")
				score := 0
				for _, r := range results {
					if r.Exists() {
						score += int(r.Type) + len(r.Raw) + len(r.Str)
					}
				}
				envelopeSelectorSink += score
			}
		})
	}
}

func fastjsonValueScore(v *fastjson.Value) int {
	if v == nil {
		return 0
	}
	return int(v.Type())
}

func jsonparserOptional(data []byte, key string) ([]byte, jsonparser.ValueType, error) {
	value, typ, _, err := jsonparser.Get(data, key)
	if err == nil {
		return value, typ, nil
	}
	if errors.Is(err, jsonparser.KeyPathNotFoundError) {
		return nil, jsonparser.NotExist, nil
	}
	return nil, typ, err
}
