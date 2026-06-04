// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

// Package goccy provides an opt-in [jsonrpc2.Codec] backed by
// github.com/goccy/go-json, a fast, portable JSON library that needs no JIT or
// assembly.
//
// It is isolated in its own module so that goccy never enters the
// dependency-light core go.lsp.dev/jsonrpc2 module graph. Install it explicitly
// and assign it to a connection (or to [jsonrpc2.DefaultCodec]) when goccy's
// performance is desired. Because goccy is pure Go, this package builds and runs
// on every platform without build tags.
package goccy

import (
	gojson "github.com/goccy/go-json"

	"go.lsp.dev/jsonrpc2"
)

// marshalOptions disables HTML escaping so that output matches a json.Encoder
// configured with SetEscapeHTML(false), per the [jsonrpc2.Codec] contract.
var marshalOptions = []gojson.EncodeOptionFunc{gojson.DisableHTMLEscape()}

// Codec is a [jsonrpc2.Codec] backed by github.com/goccy/go-json.
//
// A [jsonrpc2.RawMessage] (or encoding/json.RawMessage) value is passed through
// verbatim without re-encoding; every other value is routed through goccy.
type Codec struct{}

// compile-time check that Codec satisfies jsonrpc2.Codec.
var _ jsonrpc2.Codec = Codec{}

// Marshal implements [jsonrpc2.Codec] using goccy, passing a raw, already-encoded
// JSON value through verbatim.
func (Codec) Marshal(v any) ([]byte, error) {
	if raw, ok := rawBytes(v); ok {
		return rawMarshal(raw), nil
	}
	return gojson.MarshalWithOption(v, marshalOptions...)
}

// Unmarshal implements [jsonrpc2.Codec] using goccy, copying data verbatim when
// the destination is a pointer to a raw JSON value.
func (Codec) Unmarshal(data []byte, v any) error {
	if rawUnmarshal(data, v) {
		return nil
	}
	return gojson.Unmarshal(data, v)
}
