// Copyright 2019 The Go Language Server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"
	"strconv"
)

// Version represents a JSON-RPC version.
const Version = "2.0"

// ID is a Request identifier.
// Only one of either the Name or Number members will be set, using the
// number form if the Name is the empty string.
type ID struct {
	Name   string
	Number int64
}

// compile time check whether the ID implements a json.Marshaler and json.Unmarshaler interfaces.
var (
	_ json.Marshaler   = (*ID)(nil)
	_ json.Unmarshaler = (*ID)(nil)
)

// String returns a string representation of the ID.
// The representation is non ambiguous, string forms are quoted, number forms
// are preceded by a #.
func (id *ID) String() string {
	if id == nil {
		return ""
	}
	if id.Name != "" {
		return strconv.Quote(id.Name)
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

// WireRequest is sent to a server to represent a Call or Notify operaton.
type WireRequest struct {
	// JSONRPC is always encoded as the string "2.0"
	JSONRPC string `json:"jsonrpc"`

	// Method is a string containing the method name to invoke.
	//
	// Method names that begin with the word rpc followed by a period character (U+002E or ASCII 46) are reserved
	// for rpc-internal methods and extensions and MUST NOT be used for anything else.
	Method string `json:"method"`

	// Params is either a struct or an array with the parameters of the method.
	Params *json.RawMessage `json:"params,omitempty"`

	// ID is the id of this request, used to tie the Response back to the request.
	// Will be either a string or a number.
	//
	// If not set, the Request is a notify, and no response is possible.
	ID *ID `json:"id,omitempty"`
}

// WireResponse is a reply to a Request.
// It will always have the ID field set to tie it back to a request, and will
// have either the Result or Error fields set depending on whether it is a
// success or failure response.
type WireResponse struct {
	// JSONRPC is always encoded as the string "2.0"
	JSONRPC string `json:"jsonrpc"`

	// Result is the response value, and is required on success.
	// This member MUST NOT exist if there was an error invoking the method.
	//
	// The value of this member is determined by the method invoked on the Server.
	Result *json.RawMessage `json:"result,omitempty"`

	// Error is the object in case a request fails., and is required on error.
	// This member MUST NOT exist if there was no error triggered during invocation.
	//
	// The value for this member MUST be an Object.
	Error *Error `json:"error,omitempty"`

	// ID is the request id.
	//
	// ID must be set and is the identifier of the Request this is a response to.
	//
	// It MUST be the same as the value of the id member in the Request Object.
	// If there was an error in detecting the id in the Request object (e.g. Parse error/Invalid Request), it MUST be Null.
	ID *ID `json:"id,omitempty"`
}

// Combined represents a all the fields of both Request and Response.
// We can decode this and then work out which it is.
type Combined struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *ID              `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  *json.RawMessage `json:"params,omitempty"`
	Result  *json.RawMessage `json:"result,omitempty"`
	Error   *Error           `json:"error,omitempty"`
}
