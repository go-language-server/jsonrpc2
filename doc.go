// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

// Package jsonrpc2 is a minimal, allocation-conscious implementation of the
// JSON-RPC 2.0 wire protocol.
//
// The package is built around a reflection-free wire core plus pluggable
// framing, a swappable payload codec, and a bidirectional connection state
// machine. The wire core encodes message envelopes by appending directly into a
// byte buffer ([EncodeMessage], [AppendMessage], [AppendCall],
// [AppendNotification], [AppendResponse], [AppendBatch]) and decodes them with
// a single-pass span scanner ([DecodeMessage], [ParseRequests]), so the hot path
// performs no reflection and copies each payload at most once.
//
// [EncodeMessage], [DecodeMessage], and [ParseRequests] return owned values.
// For callback-scoped fast paths, [ScanMessageView], [ScanFrameView], and
// [AppendRequestViews] expose borrowed views over caller-owned frame bytes; those
// views are valid only while the source frame remains valid and unmodified.
//
// The message types are a closed set of [*Call], [*Notification], and
// [*Response], all of which implement the [Message] interface.
//
// # Framing
//
// A [Stream] adapts a byte transport to message reads and writes. Two framings
// are provided: newline-delimited JSON ([NewNDJSONStream], compatible with the
// Model Context Protocol stdio transport) and LSP "Content-Length" header
// framing ([NewHeaderStream]). [NewStream] selects the header framing as the
// gopls-compatible default.
//
// # Codec
//
// The envelope is never marshaled through a codec; only the user payload
// (params and result) is. The payload [Codec] is swappable via [WithCodec] and
// defaults to encoding/json/v2 ([DefaultCodec]). Faster opt-in codecs live in
// the codec/sonic and codec/goccy subpackages, which carry their own module
// dependencies and never enter this package's module graph.
//
// # Serving
//
// A [Conn] is a symmetric peer that can both issue ([Conn.Call], [Conn.Notify])
// and answer requests via a [Handler] started with [Conn.Go]. For network
// servers, [Serve] and [ListenAndServe] accept connections from a
// [net.Listener] and drive each one with a [StreamServer], typically built from
// a [Handler] with [HandlerServer].
package jsonrpc2
