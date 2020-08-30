// Copyright 2020 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

// Code is an int64 error code as defined in the JSON-RPC spec.
type Code int64

// list of JSON-RPC error codes.
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

// list of JSON-RPC errors.
var (
	// ErrUnknown should be used for all non coded errors.
	ErrUnknown = Error{Code: UnknownError, Message: "JSON-RPC unknown error"}

	// ErrParse is used when invalid JSON was received by the server.
	ErrParse = Error{Code: ParseError, Message: "JSON-RPC parse error"}

	// ErrInvalidRequest is used when the JSON sent is not a valid Request object.
	ErrInvalidRequest = Error{Code: InvalidRequest, Message: "JSON-RPC invalid request"}

	// ErrMethodNotFound should be returned by the handler when the method does
	// not exist / is not available.
	ErrMethodNotFound = Error{Code: MethodNotFound, Message: "JSON-RPC method not found"}

	// ErrInvalidParams should be returned by the handler when method
	// parameter(s) were invalid.
	ErrInvalidParams = Error{Code: InvalidParams, Message: "JSON-RPC invalid params"}

	// ErrInternal is not currently returned but defined for completeness.
	ErrInternal = Error{Code: InternalError, Message: "JSON-RPC internal error"}

	// ErrServerOverloaded is returned when a message was refused due to a
	// server being temporarily unable to accept any new messages.
	ErrServerOverloaded = Error{Code: ServerOverloaded, Message: "JSON-RPC overloaded"}

	// ErrIdleTimeout is returned when serving timed out waiting for new connections.
	ErrIdleTimeout = constErr("timed out waiting for new connections")
)
