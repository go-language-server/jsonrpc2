// SPDX-FileCopyrightText: Copyright 2021 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"fmt"

	"github.com/segmentio/encoding/json"
)

// Code is an int64 error code as defined in the JSON-RPC spec.
type Code int64

// list of JSON-RPC error codes.
const (
	// ParseError is the invalid JSON was received by the server.
	// An error occurred on the server while parsing the JSON text.
	ParseError Code = -32700

	// InvalidRequest is the JSON sent is not a valid Request object.
	InvalidRequest Code = -32600

	// MethodNotFound is the method does not exist / is not available.
	MethodNotFound Code = -32601

	// InvalidParams is the invalid method parameter(s).
	InvalidParams Code = -32602

	// InternalError is the internal JSON-RPC error.
	InternalError Code = -32603

	// ServerNotInitialized is the error of server not initialized.
	ServerNotInitialized Code = -32002

	// UnknownError should be used for all non coded errors.
	UnknownError Code = -32001

	// RequestCancelled is the cancellation error.
	//
	// Defined by the Language Server Protocol.
	RequestCancelled Code = -32800

	// ContentModified is the state change that invalidates the result of a request in execution.
	//
	// Defined by the Language Server Protocol.
	ContentModified Code = -32801

	// ServerOverloaded is returned when a message was refused due to a
	// server being temporarily unable to accept any new messages.
	ServerOverloaded Code = -32000

	codeServerErrorStart Code = -32099
	codeServerErrorEnd   Code = -32000
)

// Version represents a JSON-RPC version.
const Version = "2.0"

// version is a special 0 sized struct that encodes as the jsonrpc version tag.
//
// It will fail during decode if it is not the correct version tag in the stream.
type version struct{}

// compile time check whether the version implements a json.Marshaler and json.Unmarshaler interfaces.
var (
	_ json.Marshaler   = (*version)(nil)
	_ json.Unmarshaler = (*version)(nil)
)

// MarshalJSON implements json.Marshaler.
func (version) MarshalJSON() ([]byte, error) {
	return json.Marshal(Version)
}

// UnmarshalJSON implements json.Unmarshaler.
func (version) UnmarshalJSON(data []byte) error {
	version := ""
	if err := json.Unmarshal(data, &version); err != nil {
		return fmt.Errorf("failed to Unmarshal: %w", err)
	}
	if version != Version {
		return fmt.Errorf("invalid RPC version %v", version)
	}
	return nil
}

// ID is a Request identifier.
//
// Only one of either the Name or Number members will be set, using the
// number form if the Name is the empty string.
type ID struct {
	name   string
	number int64
}

// compile time check whether the ID implements a fmt.Formatter, json.Marshaler and json.Unmarshaler interfaces.
var (
	_ fmt.Formatter    = (*ID)(nil)
	_ json.Marshaler   = (*ID)(nil)
	_ json.Unmarshaler = (*ID)(nil)
)

// NewNumberID returns a new number request ID.
func NewNumberID(v int64) ID { return ID{number: v} }

// NewStringID returns a new string request ID.
func NewStringID(v string) ID { return ID{name: v} }

// Format writes the ID to the formatter.
//
// If the rune is q the representation is non ambiguous,
// string forms are quoted, number forms are preceded by a #.
func (id ID) Format(f fmt.State, r rune) {
	numF, strF := `%d`, `%s`
	if r == 'q' {
		numF, strF = `#%d`, `%q`
	}

	switch {
	case id.name != "":
		fmt.Fprintf(f, strF, id.name)
	default:
		fmt.Fprintf(f, numF, id.number)
	}
}

// MarshalJSON implements json.Marshaler.
func (id *ID) MarshalJSON() ([]byte, error) {
	if id.name != "" {
		return json.Marshal(id.name)
	}
	return json.Marshal(id.number)
}

// UnmarshalJSON implements json.Unmarshaler.
func (id *ID) UnmarshalJSON(data []byte) error {
	*id = ID{}
	if err := json.Unmarshal(data, &id.number); err == nil {
		return nil
	}
	return json.Unmarshal(data, &id.name)
}

// wireRequest is sent to a server to represent a Call or Notify operaton.
type wireRequest struct {
	// VersionTag is always encoded as the string "2.0"
	VersionTag version `json:"jsonrpc"`
	// Method is a string containing the method name to invoke.
	Method string `json:"method"`
	// Params is either a struct or an array with the parameters of the method.
	Params *json.RawMessage `json:"params,omitempty"`
	// The id of this request, used to tie the Response back to the request.
	// Will be either a string or a number. If not set, the Request is a notify,
	// and no response is possible.
	ID *ID `json:"id,omitempty"`
}

// wireResponse is a reply to a Request.
//
// It will always have the ID field set to tie it back to a request, and will
// have either the Result or Error fields set depending on whether it is a
// success or failure wireResponse.
type wireResponse struct {
	// VersionTag is always encoded as the string "2.0"
	VersionTag version `json:"jsonrpc"`
	// Result is the response value, and is required on success.
	Result *json.RawMessage `json:"result,omitempty"`
	// Error is a structured error response if the call fails.
	Error *Error `json:"error,omitempty"`
	// ID must be set and is the identifier of the Request this is a response to.
	ID *ID `json:"id,omitempty"`
}

// combined has all the fields of both Request and Response.
//
// We can decode this and then work out which it is.
type combined struct {
	VersionTag version          `json:"jsonrpc"`
	ID         *ID              `json:"id,omitempty"`
	Method     string           `json:"method"`
	Params     *json.RawMessage `json:"params,omitempty"`
	Result     *json.RawMessage `json:"result,omitempty"`
	Error      *Error           `json:"error,omitempty"`
}
