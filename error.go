// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"fmt"

	"github.com/francoispqt/gojay"
	"golang.org/x/xerrors"
)

// Code represents a error's category.
type Code int64

const (
	// CodeParseError is the invalid JSON was received by the server. An error occurred on the server while parsing the JSON text.
	CodeParseError Code = -32700
	// CodeInvalidRequest is the JSON sent is not a valid Request object.
	CodeInvalidRequest Code = -32600
	// CodeMethodNotFound is the method does not exist / is not available.
	CodeMethodNotFound Code = -32601
	// CodeInvalidParams is the invalid method parameter(s).
	CodeInvalidParams Code = -32602
	// CodeInternalError is the internal JSON-RPC error.
	CodeInternalError Code = -32603

	// CodeServerNotInitialized is the error of server not initialized.
	CodeServerNotInitialized Code = -32002
	// CodeUnknownError should be used for all non coded errors.
	CodeUnknownError Code = -32001
	// CodeRequestCancelled is the cancellation error.
	CodeRequestCancelled Code = -32800
	// CodeContentModified is the state change that invalidates the result of a request in execution.
	CodeContentModified Code = -32801

	// CodeServerOverloaded is returned when a message was refused due to a
	// server being temporarily unable to accept any new messages.
	CodeServerOverloaded = -32000

	codeServerErrorStart Code = -32099 //nolint:deadcode,varcheck
	codeServerErrorEnd   Code = -32000 //nolint:deadcode,varcheck
)

// Error represents a jsonrpc2 error.
type Error struct {
	// Code a number indicating the error type that occurred.
	Code Code `json:"code"`

	// Message a string providing a short description of the error.
	Message string `json:"message"`

	// Data a Primitive or Structured value that contains additional
	// information about the error. Can be omitted.
	Data *gojay.EmbeddedJSON `json:"data"`

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

// New builds a Error struct for the suppied message and code.
func New(c Code, args ...interface{}) *Error {
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
