// Copyright 2020 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"bytes"
	"context"
	"io/ioutil"
	"testing"
)

const payload = `Content-Length: 265

{
	"jsonrpc": "2.0",
	"id": 1,
	"method": "textDocument/didOpen",
	"params": {
		textDocument: {
			"uri": "file:///path/to/basic.go",
			"languageId": "go",
			"version": 10,
			"text": "package main

import \"fmt\"

func main() {
	fmt.Print(\"test\")
}
"
		}
	}
}`

func BenchmarkSteam_Read(b *testing.B) {
	var in bytes.Buffer

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.Write([]byte(payload))
		stream := NewStream(&in, ioutil.Discard)
		_, _, _ = stream.Read(context.Background())
		in.Reset()
	}
	b.SetBytes(int64(len(payload)))
}

func BenchmarkSteam_Write(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stream := NewStream(nil, ioutil.Discard)
		_, _ = stream.Write(context.Background(), []byte(payload))
	}
	b.SetBytes(int64(len(payload)))
}
