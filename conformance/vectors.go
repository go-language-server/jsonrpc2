// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

// Package conformance holds JSON-RPC 2.0 wire vectors shared across the
// jsonrpc2 test suite. Keeping them in one place lets the encode, decode, and
// fuzz tests agree on a single corpus of canonical and adversarial inputs.
package conformance

// Kind classifies the expected role of a vector after decoding.
type Kind uint8

// The recognized vector kinds.
const (
	// KindInvalid marks an input that a strict single-message decode rejects.
	KindInvalid Kind = iota
	// KindCall marks a request that expects a response (has method and id).
	KindCall
	// KindNotification marks a request with no id.
	KindNotification
	// KindResponseResult marks a successful response (has result).
	KindResponseResult
	// KindResponseError marks an error response (has error).
	KindResponseError
)

// Vector is a single JSON-RPC 2.0 conformance case.
type Vector struct {
	// Name identifies the case using the "success:"/"error:" convention shared
	// by the test suite.
	Name string
	// Wire is the raw JSON bytes of the message.
	Wire string
	// Kind is the expected classification of Wire on a strict decode.
	Kind Kind
	// Method is the expected method name for request kinds.
	Method string
	// Params is the expected raw params for request kinds ("" means absent).
	Params string
	// Result is the expected raw result for result responses ("" means absent).
	Result string
	// ErrCode is the expected error code for error responses.
	ErrCode int32
}

// Valid returns the vectors that a strict single-message decode must accept and
// classify, including the two pinned specification traps:
//
//   - a Response with "id":null is distinct from a notification that omits "id";
//   - "result":null is a valid success result, distinct from result absent.
func Valid() []Vector {
	return []Vector{
		{
			Name:   "success: call with object params",
			Wire:   `{"jsonrpc":"2.0","method":"sum","params":{"a":1,"b":2},"id":1}`,
			Kind:   KindCall,
			Method: "sum",
			Params: `{"a":1,"b":2}`,
		},
		{
			Name:   "success: call with array params",
			Wire:   `{"jsonrpc":"2.0","method":"subtract","params":[42,23],"id":2}`,
			Kind:   KindCall,
			Method: "subtract",
			Params: `[42,23]`,
		},
		{
			Name:   "success: call with string id",
			Wire:   `{"jsonrpc":"2.0","method":"ping","id":"abc"}`,
			Kind:   KindCall,
			Method: "ping",
		},
		{
			Name:   "success: notification without params",
			Wire:   `{"jsonrpc":"2.0","method":"update"}`,
			Kind:   KindNotification,
			Method: "update",
		},
		{
			Name:   "success: notification with params",
			Wire:   `{"jsonrpc":"2.0","method":"notify","params":[1,2,3]}`,
			Kind:   KindNotification,
			Method: "notify",
			Params: `[1,2,3]`,
		},
		{
			Name:   "success: notification with explicit null id",
			Wire:   `{"jsonrpc":"2.0","method":"notify","id":null}`,
			Kind:   KindNotification,
			Method: "notify",
		},
		{
			Name:   "success: success response with result",
			Wire:   `{"jsonrpc":"2.0","result":19,"id":3}`,
			Kind:   KindResponseResult,
			Result: `19`,
		},
		{
			Name:   "success: success response with result null (trap b present-null)",
			Wire:   `{"jsonrpc":"2.0","result":null,"id":4}`,
			Kind:   KindResponseResult,
			Result: `null`,
		},
		{
			Name:    "success: error response with null id (trap a)",
			Wire:    `{"jsonrpc":"2.0","error":{"code":-32700,"message":"parse error"},"id":null}`,
			Kind:    KindResponseError,
			ErrCode: -32700,
		},
		{
			Name:    "success: error response with data",
			Wire:    `{"jsonrpc":"2.0","error":{"code":-32602,"message":"invalid params","data":{"detail":"x"}},"id":5}`,
			Kind:    KindResponseError,
			ErrCode: -32602,
		},
		{
			Name:   "success: call with escaped method name",
			Wire:   `{"jsonrpc":"2.0","method":"a\/b\n","id":6}`,
			Kind:   KindCall,
			Method: "a/b\n",
		},
		{
			Name:   "success: call with whitespace around tokens",
			Wire:   "{ \"jsonrpc\" : \"2.0\" , \"method\" : \"m\" , \"id\" : 7 }",
			Kind:   KindCall,
			Method: "m",
		},
		{
			Name:   "success: call with nested params and strings containing braces",
			Wire:   `{"jsonrpc":"2.0","method":"m","params":{"s":"}{[]","n":[{"k":"v"}]},"id":8}`,
			Kind:   KindCall,
			Method: "m",
			Params: `{"s":"}{[]","n":[{"k":"v"}]}`,
		},
		{
			Name:   "success: call with params null treated as absent",
			Wire:   `{"jsonrpc":"2.0","method":"m","params":null,"id":9}`,
			Kind:   KindCall,
			Method: "m",
		},
		{
			Name:   "success: call with negative numeric id",
			Wire:   `{"jsonrpc":"2.0","method":"m","id":-15}`,
			Kind:   KindCall,
			Method: "m",
		},
	}
}

// Invalid returns inputs that a strict single-message decode must reject.
func Invalid() []Vector {
	return []Vector{
		{Name: "error: empty input", Wire: ``},
		{Name: "error: not an object", Wire: `42`},
		{Name: "error: truncated object", Wire: `{"jsonrpc":"2.0","method":"m"`},
		{Name: "error: response without result or error", Wire: `{"jsonrpc":"2.0","id":1}`},
		{Name: "error: method with result is mixed", Wire: `{"jsonrpc":"2.0","method":"m","result":1,"id":1}`},
		{Name: "error: trailing garbage", Wire: `{"jsonrpc":"2.0","method":"m","id":1}x`},
		{Name: "error: unterminated string", Wire: `{"jsonrpc":"2.0","method":"m`},
		{Name: "error: missing jsonrpc version", Wire: `{"method":"m","id":1}`},
		{Name: "error: wrong jsonrpc version", Wire: `{"jsonrpc":"1.0","method":"m","id":1}`},
		{Name: "error: jsonrpc version as number", Wire: `{"jsonrpc":2.0,"method":"m","id":1}`},
		{Name: "error: jsonrpc version null", Wire: `{"jsonrpc":null,"method":"m","id":1}`},
		{Name: "error: missing version on response", Wire: `{"result":1,"id":1}`},
		{Name: "error: response with both result and error", Wire: `{"jsonrpc":"2.0","result":1,"error":{"code":-32000,"message":"boom"},"id":1}`},
	}
}
