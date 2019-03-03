// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"
)

// ID is a Request identifier.
// Only one of either the Name or Number members will be set, using the
// number form if the Name is the empty string.
type ID struct {
	Name   string
	Number int64
}

// Message is a general message as defined by JSON-RPC. The language server protocol always uses "2.0" as the jsonrpc version.
type Message struct {
	JSONRPC string `json:"jsonrpc"`
}

// Request is a request message to describe a request between the client and the server.
// Every processed request must send a response back to the sender of the request.
type Request struct {
	Message

	// The request id.
	ID *ID `json:"id"`

	// The method to be invoked.
	Method string `json:"method"`

	// The method's params.
	Params *json.RawMessage `json:"params,omitempty"`
}

// Response is a response ressage sent as a result of a request.
// If a request doesn't provide a result value the receiver of a request still needs to return a response message to
// conform to the JSON RPC specification.
// The result property of the ResponseMessage should be set to null in this case to signal a successful request.
type Response struct {
	Message

	// The error object in case a request fails.
	Error *ResponseError `json:"error,omitempty"`

	// The request id.
	ID *ID `json:"id"`

	// The result of a request. This member is REQUIRED on success.
	// This member MUST NOT exist if there was an error invoking the method.
	Result *json.RawMessage `json:"result,omitempty"`
}

// ResponseError ...
type ResponseError struct {

	// Code a number indicating the error type that occurred.
	Code ErrorCode `json:"code"`

	// Data a Primitive or Structured value that contains additional
	// information about the error. Can be omitted.
	Data *json.RawMessage `json:"data"`

	// Message a string providing a short description of the error.
	Message string `json:"message"`
}

type NotificationMessage struct {
	Message

	// Method is the method to be invoked.
	Method string `json:"method"`

	// Params is the notification's params.
	Params interface{} `json:"params,omitempty"`
}
