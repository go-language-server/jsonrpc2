// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import "testing"

var (
	benchmarkDecodeMinimal = []byte(`{"jsonrpc":"2.0","method":"void","id":1}`)
	benchmarkDecodeMedium  = []byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"textDocument":{"uri":"file:///tmp/example.go"},"position":{"line":123,"character":45}},"id":99}`)

	benchmarkMessageView MessageView
	benchmarkMessage     Message
)

func BenchmarkDecodeMinimalLegacy(b *testing.B) {
	msg := benchmarkDecodeMinimal
	b.ReportAllocs()
	for b.Loop() {
		got, err := DecodeMessage(msg)
		if err != nil {
			b.Fatalf("DecodeMessage: %v", err)
		}
		benchmarkMessage = got
	}
}

func BenchmarkDecodeMinimalFastView(b *testing.B) {
	msg := benchmarkDecodeMinimal
	b.ReportAllocs()
	for b.Loop() {
		got, err := ScanMessageView(msg)
		if err != nil {
			b.Fatalf("ScanMessageView: %v", err)
		}
		benchmarkMessageView = got
	}
}

func BenchmarkDecodeMediumLegacy(b *testing.B) {
	msg := benchmarkDecodeMedium
	b.ReportAllocs()
	for b.Loop() {
		got, err := DecodeMessage(msg)
		if err != nil {
			b.Fatalf("DecodeMessage: %v", err)
		}
		benchmarkMessage = got
	}
}

func BenchmarkDecodeMediumFastView(b *testing.B) {
	msg := benchmarkDecodeMedium
	b.ReportAllocs()
	for b.Loop() {
		got, err := ScanMessageView(msg)
		if err != nil {
			b.Fatalf("ScanMessageView: %v", err)
		}
		benchmarkMessageView = got
	}
}
