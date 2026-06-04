// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

// envelopePrefix is the shared, constant head of every JSON-RPC envelope.
const envelopePrefix = `{"jsonrpc":"2.0"`

// EncodeMessage encodes a [Message] into a freshly allocated JSON-RPC envelope.
//
// The envelope is built by appending directly into a pooled buffer with no
// reflection: method names and string identifiers are written through a
// fast-escape routine, integer identifiers through strconv, and params/result
// raw values verbatim. The returned slice is a right-sized copy that the caller
// owns; the pooled buffer is recycled before returning.
func EncodeMessage(msg Message) ([]byte, error) {
	bp := getEncodeBuf()
	buf := appendMessage(*bp, msg)
	out := make([]byte, len(buf))
	copy(out, buf)
	*bp = buf
	putEncodeBuf(bp)
	return out, nil
}

// appendMessage appends the wire envelope of msg to dst and returns the extended
// slice. It dispatches on the concrete message type; the [Message] set is closed
// so the default case is unreachable for well-formed values.
func appendMessage(dst []byte, msg Message) []byte {
	switch m := msg.(type) {
	case *Call:
		return appendCall(dst, m)
	case *Notification:
		return appendNotification(dst, m)
	case *Response:
		return appendResponse(dst, m)
	default:
		return dst
	}
}

// appendCall appends a call envelope:
//
//	{"jsonrpc":"2.0","method":<esc>,"params":<raw?>,"id":<id>}
func appendCall(dst []byte, c *Call) []byte {
	dst = append(dst, envelopePrefix...)
	dst = append(dst, `,"method":`...)
	dst = appendQuotedString(dst, c.method)
	if len(c.params) > 0 {
		dst = append(dst, `,"params":`...)
		dst = append(dst, c.params...)
	}
	dst = append(dst, `,"id":`...)
	dst = c.id.appendID(dst)
	return append(dst, '}')
}

// appendNotification appends a notification envelope (no id member):
//
//	{"jsonrpc":"2.0","method":<esc>,"params":<raw?>}
func appendNotification(dst []byte, n *Notification) []byte {
	dst = append(dst, envelopePrefix...)
	dst = append(dst, `,"method":`...)
	dst = appendQuotedString(dst, n.method)
	if len(n.params) > 0 {
		dst = append(dst, `,"params":`...)
		dst = append(dst, n.params...)
	}
	return append(dst, '}')
}

// appendResponse appends a response envelope. Per the specification a response
// always carries an id (null when unknown) and exactly one of result or error;
// a successful response always emits a result member, defaulting to null when no
// result bytes are present.
//
//	{"jsonrpc":"2.0","id":<id>,"result":<raw|null>}
//	{"jsonrpc":"2.0","id":<id>,"error":{...}}
func appendResponse(dst []byte, r *Response) []byte {
	dst = append(dst, envelopePrefix...)
	dst = append(dst, `,"id":`...)
	dst = r.id.appendID(dst)
	if r.err != nil {
		dst = append(dst, `,"error":`...)
		dst = appendError(dst, toWireError(r.err))
		return append(dst, '}')
	}
	dst = append(dst, `,"result":`...)
	if len(r.result) > 0 {
		dst = append(dst, r.result...)
	} else {
		dst = append(dst, 'n', 'u', 'l', 'l')
	}
	return append(dst, '}')
}
