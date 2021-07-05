// SPDX-FileCopyrightText: 2019 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"errors"
	"fmt"

	"github.com/segmentio/encoding/json"
)

var (
	// ErrIdleTimeout is returned when serving timed out waiting for new connections.
	ErrIdleTimeout = errors.New("timed out waiting for new connections")

	// ErrNotHandled is returned from a handler to indicate it did not handle the
	// message.
	ErrNotHandled = errors.New("JSON RPC not handled")

	// ErrAsyncResponse is returned from a handler to indicate it will generate a
	// response asynchronously.
	ErrAsyncResponse = errors.New("JSON RPC asynchronous response")
)

// Error represents a JSON-RPC error.
type Error struct {
	// Code a Number that indicates the error type that occurred.
	Code Code `json:"code"`

	// Message a String providing a short description of the error.
	// The message SHOULD be limited to a concise single sentence.
	Message string `json:"message"`

	// Data a Primitive or Structured value that contains additional information about the error.
	// This may be omitted.
	// The value of this member is defined by the Server (e.g. detailed error information, nested errors etc.).
	Data *json.RawMessage `json:"data,omitempty"`
}

// make sure Error implements the error interface.
var _ error = (*Error)(nil)

// Error returns a string representation of the Error.
func (e *Error) Error() string {
	return e.Message
}

// NewError builds a Error struct for the suppied code and message.
func NewError(code Code, message string) *Error {
	return &Error{
		Code:    code,
		Message: message,
	}
}

// Errorf builds a Error struct for the suppied code, format and args.
func Errorf(code Code, format string, args ...interface{}) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}
