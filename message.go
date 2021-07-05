// SPDX-FileCopyrightText: 2021 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/segmentio/encoding/json"
)

// This file contains the go forms of the wire specification.
// see http://www.jsonrpc.org/specification for details

// Version represents a JSON-RPC version.
const Version = "2.0"

// payload has all the fields of both Request and Response.
//
// It can decode this and then work out which it is.
type payload struct {
	VersionTag string          `json:"jsonrpc"`
	ID         interface{}     `json:"id,omitempty"`
	Error      *Error          `json:"error,omitempty"`
	Method     string          `json:"method,omitempty"`
	Params     json.RawMessage `json:"params,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

// ID is a Request identifier.
type ID struct {
	value interface{}
}

// StringID creates a new string request identifier.
func StringID(s string) ID {
	return ID{value: s}
}

// Int64ID creates a new integer request identifier.
func Int64ID(i int64) ID {
	return ID{value: i}
}

// IsValid reports whether the ID is a valid identifier.
//
// The default value for ID will return false.
func (id ID) IsValid() bool {
	return id.value != nil
}

// Raw returns the underlying value of the ID.
func (id ID) Raw() interface{} {
	return id.value
}

// Message is the interface to all jsonrpc2 message types.
//
// They share no common functionality, but are a closed set of concrete types
// that are allowed to implement this interface.
//
// The message types are Request and Response.
type Message interface {
	// marshal builds the wire form from the API form.
	//
	// It is private, which makes the set of Message implementations closed.
	marshal(msg *payload)
}

// Request is a Message sent to a peer to request behavior.
// If it has an ID it is a call, otherwise it is a notification.
type Request struct {
	// ID of this request, used to tie the Response back to the request.
	// This will be nil for notifications.
	ID ID

	// Method is a string containing the method name to invoke.
	Method string

	// Params is either a struct or an array with the parameters of the method.
	Params json.RawMessage
}

// IsCall reports whether the request is call.
func (req *Request) IsCall() bool {
	return req.ID.IsValid()
}

// marshal implements Message.marshal.
func (req *Request) marshal(msg *payload) {
	msg.ID = req.ID.value
	msg.Method = req.Method
	msg.Params = req.Params
}

// NewNotification constructs a new Notification message for the supplied
// method and parameters.
func NewNotification(method string, params interface{}) (*Request, error) {
	p, merr := marshalInterface(params)

	req := &Request{
		Method: method,
		Params: p,
	}

	return req, merr
}

// NewRequest constructs a new Call message for the supplied ID, method and
// parameters.
func NewRequest(id ID, method string, params interface{}) (*Request, error) {
	p, merr := marshalInterface(params)

	req := &Request{
		ID:     id,
		Method: method,
		Params: p,
	}

	return req, merr
}

// Response is a Message used as a reply to a call Request.
// It will have the same ID as the call it is a response to.
type Response struct {
	// ID of the request this is a response to.
	ID ID

	// Error is set only if the call failed.
	Error error

	// Result is the content of the response.
	Result json.RawMessage
}

// NewResponse constructs a new Response message that is a reply to the
// supplied.
//
// If err is set result may be ignored.
func NewResponse(id ID, result interface{}, err error) (*Response, error) {
	r, merr := marshalInterface(result)

	resp := &Response{
		ID:     id,
		Result: r,
		Error:  err,
	}

	return resp, merr
}

// marshal implements Message.marshal.
func (resp *Response) marshal(msg *payload) {
	msg.ID = resp.ID.value
	msg.Error = toError(resp.Error)
	msg.Result = resp.Result
}

// toError converts err to Error type.
func toError(err error) *Error {
	if err == nil {
		// no error, the response is complete.
		return nil
	}

	var wrapped *Error
	if errors.As(err, &wrapped) {
		// already a wire error, just use it.
		return wrapped
	}

	result := &Error{
		Message: err.Error(),
	}
	if errors.As(err, &wrapped) {
		// if we wrapped a wire error, keep the code from the wrapped error
		// but the message from the outer error
		result.Code = wrapped.Code
	}

	return result
}

// EncodeMessage encodes msg to byts slice.
func EncodeMessage(msg Message) ([]byte, error) {
	m := payload{
		VersionTag: Version,
	}
	msg.marshal(&m)

	data, err := json.Marshal(&m)
	if err != nil {
		return nil, fmt.Errorf("marshaling jsonrpc message: %w", err)
	}

	return data, nil
}

// DecodeMessage deecodse byts slice to Message.
func DecodeMessage(data []byte) (Message, error) {
	var msg payload

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.ZeroCopy()
	if err := dec.Decode(&msg); err != nil {
		return nil, fmt.Errorf("unmarshaling jsonrpc message: %w", err)
	}

	if msg.VersionTag != Version {
		return nil, fmt.Errorf("invalid message version tag %s expected %s", msg.VersionTag, Version)
	}

	id := ID{}
	switch v := msg.ID.(type) {
	case nil:
		// nothing to do
	case float64:
		// coerce the id type to int64 if it is float64, the spec does not allow fractional parts
		id = Int64ID(int64(v))
	case int64:
		id = Int64ID(v)
	case string:
		id = StringID(v)
	default:
		return nil, fmt.Errorf("invalid message id type <%[1]T>%[1]v", v)
	}

	if msg.Method != "" {
		// has a method, must be a call
		return &Request{
			Method: msg.Method,
			ID:     id,
			Params: msg.Params,
		}, nil
	}

	// no method, should be a response
	if !id.IsValid() {
		return nil, ErrInvalidRequest
	}

	resp := &Response{
		ID:     id,
		Result: msg.Result,
	}

	// we have to check if msg.Error is nil to avoid a typed error
	if msg.Error != nil {
		resp.Error = msg.Error
	}

	return resp, nil
}

func marshalInterface(obj interface{}) (json.RawMessage, error) {
	if obj == nil {
		return nil, nil
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal json: %w", err)
	}

	return json.RawMessage(data), nil
}
