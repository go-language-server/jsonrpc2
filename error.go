// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"fmt"
)

type ErrorCode int64

const (
	CodeParseError           ErrorCode = -32700
	CodeInvalidRequest       ErrorCode = -32600
	CodeMethodNotFound       ErrorCode = -32601
	CodeInvalidParams        ErrorCode = -32602
	CodeInternalError        ErrorCode = -32603
	CodeServerErrorStart     ErrorCode = -32099
	CodeServerErrorEnd       ErrorCode = -32000
	CodeServerNotInitialized ErrorCode = -32002
	CodeUnknownErrorCode     ErrorCode = -32001

	// Defined by the protocol.
	CodeRequestCancelled ErrorCode = -32800
	CodeContentModified  ErrorCode = -32801
)

// Error
type Error struct {

	// Code a number indicating the error type that occurred.
	Code ErrorCode `json:"code"`

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
func Errorf(code ErrorCode, format string, args ...interface{}) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}
