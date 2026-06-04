// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

// Package sonic provides an opt-in [jsonrpc2.Codec] backed by
// github.com/bytedance/sonic, a high-performance JSON library.
//
// It is intentionally isolated in its own module so that sonic and its
// assembly/JIT transitive dependencies never enter the dependency-light core
// go.lsp.dev/jsonrpc2 module graph. Install it explicitly and assign it to a
// connection (or to [jsonrpc2.DefaultCodec]) when sonic's performance is desired.
//
// sonic's package-level Marshal/Unmarshal use ConfigDefault, which does not
// escape HTML-sensitive characters, matching the codec contract. On platforms
// where sonic's optimized path is unavailable (for example certain non-amd64
// builds, or when the purego build tag is set) sonic transparently falls back to
// a compatible pure-Go implementation, so this package builds and runs correctly
// on darwin/arm64 without any additional build tags.
package sonic

import (
	"github.com/bytedance/sonic"

	"go.lsp.dev/jsonrpc2"
)

// Codec is a [jsonrpc2.Codec] backed by github.com/bytedance/sonic.
//
// A [jsonrpc2.RawMessage] (or encoding/json.RawMessage) value is passed through
// verbatim without re-encoding; every other value is routed through sonic.
type Codec struct{}

// compile-time check that Codec satisfies jsonrpc2.Codec.
var _ jsonrpc2.Codec = Codec{}

// Marshal implements [jsonrpc2.Codec] using sonic, passing a raw, already-encoded
// JSON value through verbatim.
func (Codec) Marshal(v any) ([]byte, error) {
	if raw, ok := rawBytes(v); ok {
		return rawMarshal(raw), nil
	}
	return sonic.Marshal(v)
}

// Unmarshal implements [jsonrpc2.Codec] using sonic, copying data verbatim when
// the destination is a pointer to a raw JSON value.
func (Codec) Unmarshal(data []byte, v any) error {
	if rawUnmarshal(data, v) {
		return nil
	}
	return sonic.Unmarshal(data, v)
}
