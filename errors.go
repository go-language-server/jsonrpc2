// Copyright 2019 The Go Language Server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
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

type constErr string

func (e constErr) Error() string { return string(e) }
