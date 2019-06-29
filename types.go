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

// request represents a rpc call by sending a request object to a Server.
// This is a request message to describe a request between the client and the server.
//
// Every processed request must send a response back to the sender of the request.
type request struct {
	// JSONRPC is a string specifying the version of the JSON-RPC protocol.
	//
	// MUST be exactly "2.0".
	JSONRPC string `json:"jsonrpc"`

	// Method is a string containing the name of the method to be invoked.
	//
	// Method names that begin with the word rpc followed by a period character (U+002E or ASCII 46) are reserved
	// for rpc-internal methods and extensions and MUST NOT be used for anything else.
	Method string `json:"method"`

	// Params is a string containing the name of the method to be invoked.
	//
	// Method names that begin with the word rpc followed by a period character (U+002E or ASCII 46) are reserved
	// for rpc-internal methods and extensions and MUST NOT be used for anything else.
	Params *json.RawMessage `json:"params,omitempty"`

	// ID is an identifier established by the Client that MUST contain a String, Number, or NULL value if included.
	//
	// If it is not included it is assumed to be a notification.
	//
	// The value SHOULD normally not be Null and Numbers SHOULD NOT contain fractional parts.
	ID *ID `json:"id"`
}

// IsNotify returns true if this request is a notification.
func (r *request) IsNotify() bool {
	return r.ID == nil
}

// Response is a response ressage sent as a result of a request.
// When a rpc call is made, the Server MUST reply with a Response, except for in the case of Notifications.
//
// If a request doesn't provide a result value the receiver of a request still needs to return a response message to
// conform to the JSON RPC specification.
// The result property of the ResponseMessage should be set to null in this case to signal a successful request.
type Response struct {
	// JSONRPC is a string specifying the version of the JSON-RPC protocol.
	//
	// MUST be exactly "2.0".
	JSONRPC string `json:"jsonrpc"`

	// Result is the result of a request.
	//
	// This member is REQUIRED on success.
	// This member MUST NOT exist if there was an error invoking the method.
	//
	// The value of this member is determined by the method invoked on the Server.
	Result *json.RawMessage `json:"result,omitempty"`

	// Error is the object in case a request fails.
	//
	// This member is REQUIRED on error.
	// This member MUST NOT exist if there was no error triggered during invocation.
	//
	// The value for this member MUST be an Object.
	Error *Error `json:"error,omitempty"`

	// ID is the request id.
	//
	// This member is REQUIRED.
	// It MUST be the same as the value of the id member in the Request Object.
	//
	// If there was an error in detecting the id in the Request object (e.g. Parse error/Invalid Request), it MUST be Null.
	ID *ID `json:"id"`
}

// Combined represents a all the fields of both Request and Response.
type Combined struct {
	JSONRPC string           `json:"jsonrpc"`
	Method  string           `json:"method"`
	Params  *json.RawMessage `json:"params,omitempty"`
	Result  *json.RawMessage `json:"result,omitempty"`
	Error   *Error           `json:"error,omitempty"`
	ID      *ID              `json:"id,omitempty"`
}

// NotificationMessage is a notification message.
//
// A processed notification message must not send a response back. They work like events.
type NotificationMessage struct {
	// JSONRPC is a string specifying the version of the JSON-RPC protocol.
	//
	// MUST be exactly "2.0".
	JSONRPC string `json:"jsonrpc"`

	// Method is a string containing the name of the method to be invoked.
	//
	// Method names that begin with the word rpc followed by a period character (U+002E or ASCII 46) are reserved
	// for rpc-internal methods and extensions and MUST NOT be used for anything else.
	Method string `json:"method"`

	// Params is a string containing the name of the method to be invoked.
	//
	// Method names that begin with the word rpc followed by a period character (U+002E or ASCII 46) are reserved
	// for rpc-internal methods and extensions and MUST NOT be used for anything else.
	Params *json.RawMessage `json:"params,omitempty"`
}
