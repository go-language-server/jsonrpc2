// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

type ErrorCode int64

const (
	ParseError           ErrorCode = -32700
	InvalidRequest       ErrorCode = -32600
	MethodNotFound       ErrorCode = -32601
	InvalidParams        ErrorCode = -32602
	InternalError        ErrorCode = -32603
	ServerErrorStart     ErrorCode = -32099
	ServerErrorEnd       ErrorCode = -32000
	ServerNotInitialized ErrorCode = -32002
	UnknownErrorCode     ErrorCode = -32001

	// Defined by the protocol.
	RequestCancelled ErrorCode = -32800
	ContentModified  ErrorCode = -32801
)

// Error ...
type Error struct {

	// Code a number indicating the error type that occurred.
	Code ErrorCode `json:"code"`

	// Data a Primitive or Structured value that contains additional
	// information about the error. Can be omitted.
	Data []byte `json:"data"`

	// Message a string providing a short description of the error.
	Message string `json:"message"`
}

func (err *Error) Error() string {
	if err == nil {
		return ""
	}
	return err.Message
}
