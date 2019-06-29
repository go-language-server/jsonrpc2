// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"
	"strconv"
)

// Version represents a JSONRPC version.
const Version = "2.0"

// ID is a Request identifier.
// Only one of either the Name or Number members will be set, using the
// number form if the Name is the empty string.
type ID struct {
	Name   string
	Number int64
}

// String implements fmt.Stringer.
//
// String returns a string representation of the ID.
// The representation is non ambiguous, string forms are quoted, number forms
// are preceded by a #.
func (id *ID) String() string {
	if id == nil {
		return ""
	}
	if id.Name != "" {
		return id.Name
	}

	return "#" + strconv.FormatInt(id.Number, 10)
}

// MarshalJSON implements json.Marshaler.
func (id *ID) MarshalJSON() ([]byte, error) {
	if id.Name != "" {
		return json.Marshal(id.Name)
	}

	return json.Marshal(id.Number)
}

// UnmarshalJSON implements json.Unmarshaler.
func (id *ID) UnmarshalJSON(data []byte) error {
	*id = ID{}
	if err := json.Unmarshal(data, &id.Number); err == nil {
		return nil
	}

	return json.Unmarshal(data, &id.Name)
}

var _ json.Marshaler = (*ID)(nil)
var _ json.Unmarshaler = (*ID)(nil)

// Request is a request message to describe a request between the client and the server.
//
// Every processed request must send a response back to the sender of the request.
type Request struct {
	// JSONRPC is a general message as defined by JSON-RPC.
	JSONRPC string `json:"jsonrpc"`

	// The request id.
	ID *ID `json:"id"`

	// The method to be invoked.
	Method string `json:"method"`

	// The method's params.
	Params *json.RawMessage `json:"params,omitempty"`
}

// IsNotify returns true if this request is a notification.
func (r *Request) IsNotify() bool {
	return r.ID == nil
}

// Response is a response ressage sent as a result of a request.
//
// If a request doesn't provide a result value the receiver of a request still needs to return a response message to
// conform to the JSON RPC specification.
// The result property of the ResponseMessage should be set to null in this case to signal a successful request.
type Response struct {
	// JSONRPC is a general message as defined by JSON-RPC.
	JSONRPC string `json:"jsonrpc"`

	// The request id.
	ID *ID `json:"id"`

	// The error object in case a request fails.
	Error *Error `json:"error,omitempty"`

	// The result of a request. This member is REQUIRED on success.
	// This member MUST NOT exist if there was an error invoking the method.
	Result *json.RawMessage `json:"result,omitempty"`
}

// Combined represents a all the fields of both Request and Response.
type Combined struct {
	// JSONRPC is a general message as defined by JSON-RPC.
	JSONRPC string `json:"jsonrpc"`

	// The request id.
	ID *ID `json:"id,omitempty"`

	// The method to be invoked.
	Method string `json:"method"`

	// The method's params.
	Params *json.RawMessage `json:"params,omitempty"`

	// The error object in case a request fails.
	Error *Error `json:"error,omitempty"`

	// The result of a request. This member is REQUIRED on success.
	// This member MUST NOT exist if there was an error invoking the method.
	Result *json.RawMessage `json:"result,omitempty"`
}

// NotificationMessage is a notification message.
//
// A processed notification message must not send a response back. They work like events.
type NotificationMessage struct {
	// JSONRPC is a general message as defined by JSON-RPC.
	JSONRPC string `json:"jsonrpc"`

	// Method is the method to be invoked.
	Method string `json:"method"`

	// Params is the notification's params.
	Params *json.RawMessage `json:"params,omitempty"`
}
