// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"fmt"
)

type Code int64

const (
	CodeParseError           Code = -32700
	CodeInvalidRequest       Code = -32600
	CodeMethodNotFound       Code = -32601
	CodeInvalidParams        Code = -32602
	CodeInternalError        Code = -32603
	CodeServerErrorStart     Code = -32099
	CodeServerErrorEnd       Code = -32000
	CodeServerNotInitialized Code = -32002
	CodeUnknownErrorCode     Code = -32001

	// Defined by the protocol.
	CodeRequestCancelled Code = -32800
	CodeContentModified  Code = -32801
)

// Error
type Error struct {

	// Code a number indicating the error type that occurred.
	Code Code `json:"code"`

	// Data a Primitive or Structured value that contains additional
	// information about the error. Can be omitted.
	Data []byte `json:"data"`

	// Message a string providing a short description of the error.
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Errorf builds a Error struct for the suppied message and code.
// If args is not empty, message and args will be passed to Sprintf.
func Errorf(code Code, format string, args ...interface{}) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}
