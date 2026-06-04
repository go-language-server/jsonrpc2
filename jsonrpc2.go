// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

// Version is the JSON-RPC protocol version that every envelope advertises in
// its "jsonrpc" member.
const Version = "2.0"

// Message is the interface implemented by all JSON-RPC message types.
//
// The set of implementations is closed: only [*Call], [*Notification], and
// [*Response] satisfy it. The unexported method makes the set impossible to
// extend from outside the package.
type Message interface {
	// jsonrpc2Message keeps the set of Message implementations closed.
	jsonrpc2Message()
}

// Request is the shared interface for messages that ask a peer to invoke a
// method. The set of implementations is closed to [*Call] and [*Notification].
type Request interface {
	Message

	// Method reports the name of the method to invoke.
	Method() string

	// Params reports the raw, already-encoded parameters of the method, or nil
	// when the request carries no parameters.
	Params() RawMessage

	// jsonrpc2Request keeps the set of Request implementations closed.
	jsonrpc2Request()
}

// RawMessage is a raw, already-encoded JSON value.
//
// It mirrors the semantics of encoding/json's RawMessage: the bytes are stored
// verbatim on encode and returned verbatim on decode. A RawMessage exposed by a
// decoded [Message] owns its backing array and never aliases the buffer passed
// to the decoder.
type RawMessage []byte
