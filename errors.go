// Copyright 2019 The Go Language Server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Code represents a error's category.
type Code int64

const (
	// ParseError is the invalid JSON was received by the server. An error occurred on the server while parsing the JSON text.
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

// Error represents a jsonrpc2 error.
type Error struct {
	// Code a number indicating the error type that occurred.
	Code Code `json:"code"`

	// Message a string providing a short description of the error.
	Message string `json:"message"`

	// Data a Primitive or Structured value that contains additional
	// information about the error. Can be omitted.
	Data *json.RawMessage `json:"data"`
}

// compile time check whether the Error implements error interface.
var _ error = (*Error)(nil)

// Error implements error.Error.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap implements errors.Unwrap.
//
// Returns the error underlying the receiver, which may be nil.
func (e *Error) Unwrap() error { return errors.New(e.Message) }

// NewError builds a Error struct for the suppied code and message.
func NewError(c Code, message string) *Error {
	return &Error{
		Code:    c,
		Message: message,
	}
}

// Errorf builds a Error struct for the suppied code, format and args.
func Errorf(c Code, format string, args ...interface{}) *Error {
	e := &Error{
		Code:    c,
		Message: fmt.Sprintf(format, args...),
	}

	return e
}
