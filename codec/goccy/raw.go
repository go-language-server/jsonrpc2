// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package goccy

import (
	stdjson "encoding/json"
	"slices"

	"go.lsp.dev/jsonrpc2"
)

// rawBytes reports whether v is a raw, already-encoded JSON value
// ([jsonrpc2.RawMessage] or encoding/json.RawMessage) and, if so, returns its
// bytes so it can be passed through verbatim.
func rawBytes(v any) (raw []byte, ok bool) {
	switch m := v.(type) {
	case jsonrpc2.RawMessage:
		return m, true
	case stdjson.RawMessage:
		return m, true
	default:
		return nil, false
	}
}

// rawMarshal returns the verbatim encoding of a raw JSON value, mapping a nil
// value to the JSON null literal.
func rawMarshal(raw []byte) []byte {
	if raw == nil {
		return []byte("null")
	}
	return raw
}

// rawUnmarshal copies data verbatim into v when v points to a raw JSON value, and
// reports whether it handled v. A nil data slice yields a nil destination so that
// "absent" stays distinguishable from "present but empty".
func rawUnmarshal(data []byte, v any) (handled bool) {
	switch p := v.(type) {
	case *jsonrpc2.RawMessage:
		*p = slices.Clone(data)
		return true
	case *stdjson.RawMessage:
		*p = slices.Clone(data)
		return true
	default:
		return false
	}
}
