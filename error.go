// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"
	"fmt"

	"golang.org/x/xerrors"
)

// Code represents a error's category.
type Code int64

const (
	// ParseError is the invalid JSON was received by the server. An error occurred on the server while parsing the JSON text.
	ParseError = Code(-32700)
	// InvalidRequest is the JSON sent is not a valid Request object.
	InvalidRequest = Code(-32600)
	// MethodNotFound is the method does not exist / is not available.
	MethodNotFound = Code(-32601)
	// InvalidParams is the invalid method parameter(s).
	InvalidParams = Code(-32602)
	// InternalError is the internal JSON-RPC error.
	InternalError = Code(-32603)

	// ServerNotInitialized is the error of server not initialized.
	ServerNotInitialized = Code(-32002)
	// UnknownError should be used for all non coded errors.
	UnknownError = Code(-32001)
	// RequestCancelled is the cancellation error.
	RequestCancelled = Code(-32800)
	// ContentModified is the state change that invalidates the result of a request in execution.
	ContentModified = Code(-32801)

	// ServerOverloaded is returned when a message was refused due to a
	// server being temporarily unable to accept any new messages.
	ServerOverloaded = Code(-32000)

	codeServerErrorStart = Code(-32099) //nolint:deadcode,varcheck
	codeServerErrorEnd   = Code(-32000) //nolint:deadcode,varcheck
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

	frame xerrors.Frame
	err   error
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Format implements fmt.Formatter.
func (e *Error) Format(s fmt.State, c rune) {
	xerrors.FormatError(e, s, c)
}

// FormatError implements xerrors.Formatter.
func (e *Error) FormatError(p xerrors.Printer) (next error) {
	if e.Message == "" {
		p.Printf("code=%v", e.Code)
	} else {
		p.Printf("%s (code=%v)", e.Message, e.Code)
	}
	e.frame.Format(p)

	return e.err
}

// Unwrap implements xerrors.Wrapper.
//
// The returns the error underlying the receiver, which may be nil.
func (e *Error) Unwrap() error {
	return e.err
}

// NewError builds a Error struct for the suppied message and code.
func NewError(c Code, args ...interface{}) *Error {
	e := &Error{
		Code:    c,
		Message: fmt.Sprint(args...),
		frame:   xerrors.Caller(1),
	}
	e.err = xerrors.New(e.Message)

	return e
}

// Errorf builds a Error struct for the suppied message and code.
func Errorf(c Code, format string, args ...interface{}) *Error {
	e := &Error{
		Code:    c,
		Message: fmt.Sprintf(format, args...),
		frame:   xerrors.Caller(1),
	}
	e.err = xerrors.New(e.Message)

	return e
}
