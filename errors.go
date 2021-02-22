// SPDX-FileCopyrightText: Copyright 2019 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"errors"
	"fmt"

	"github.com/segmentio/encoding/json"
)

// Error represents a JSON-RPC error.
type Error struct {
	// Code a number indicating the error type that occurred.
	Code Code `json:"code"`

	// Message a string providing a short description of the error.
	Message string `json:"message"`

	// Data a Primitive or Structured value that contains additional
	// information about the error. Can be omitted.
	Data *json.RawMessage `json:"data,omitempty"`
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
	return &Error{
		Code:    c,
		Message: fmt.Sprintf(format, args...),
	}
}

// constErr represents a error constant.
type constErr string

// compile time check whether the constErr implements error interface.
var _ error = (*constErr)(nil)

// Error implements error.Error.
func (e constErr) Error() string { return string(e) }

// This file contains the Go forms of the wire specification.
//
// See http://www.jsonrpc.org/specification for details.
//
// list of JSON-RPC errors.
var (
	// ErrUnknown should be used for all non coded errors.
	ErrUnknown = NewError(UnknownError, "JSON-RPC unknown error")

	// ErrParse is used when invalid JSON was received by the server.
	ErrParse = NewError(ParseError, "JSON-RPC parse error")

	// ErrInvalidRequest is used when the JSON sent is not a valid Request object.
	ErrInvalidRequest = NewError(InvalidRequest, "JSON-RPC invalid request")

	// ErrMethodNotFound should be returned by the handler when the method does
	// not exist / is not available.
	ErrMethodNotFound = NewError(MethodNotFound, "JSON-RPC method not found")

	// ErrInvalidParams should be returned by the handler when method
	// parameter(s) were invalid.
	ErrInvalidParams = NewError(InvalidParams, "JSON-RPC invalid params")

	// ErrInternal is not currently returned but defined for completeness.
	ErrInternal = NewError(InternalError, "JSON-RPC internal error")

	// ErrServerOverloaded is returned when a message was refused due to a
	// server being temporarily unable to accept any new messages.
	ErrServerOverloaded = NewError(ServerOverloaded, "JSON-RPC overloaded")

	// ErrIdleTimeout is returned when serving timed out waiting for new connections.
	ErrIdleTimeout = constErr("timed out waiting for new connections")
)
