// Copyright 2020 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Message is the interface to all JSON-RPC message types.
//
// They share no common functionality, but are a closed set of concrete types
// that are allowed to implement this interface.
//
// The message types are *Request, *Response and *Notification.
type Message interface {
	// isJSONRPC2Message is used to make the set of message implementations a
	// closed set.
	isJSONRPC2Message()
}

// Requester is the shared interface to jsonrpc2 messages that request
// a method be invoked.
//
// The request types are a closed set of *Request and *Notification.
type Requester interface {
	Message

	// Method is a string containing the method name to invoke.
	Method() string
	// Params is either a struct or an array with the parameters of the method.
	Params() RawMessage

	// isJSONRPC2Request is used to make the set of request implementations closed.
	isJSONRPC2Request()
}

// Request is a request that expects a response.
//
// The response will have a matching ID.
type Request struct {
	// Method is a string containing the method name to invoke.
	method string
	// Params is either a struct or an array with the parameters of the method.
	params RawMessage
	// id of this request, used to tie the Response back to the request.
	id ID
}

// compile time check whether the Request implements a json.Marshaler and json.Unmarshaler interfaces.
var (
	_ json.Marshaler   = (*Request)(nil)
	_ json.Unmarshaler = (*Request)(nil)
)

// NewRequest constructs a new Call message for the supplied ID, method and
// parameters.
func NewRequest(id ID, method string, params interface{}) (*Request, error) {
	p, merr := marshalInterface(params)
	req := &Request{
		id:     id,
		method: method,
		params: p,
	}
	return req, merr
}

func (r *Request) Method() string     { return r.method }
func (r *Request) Params() RawMessage { return r.params }
func (r *Request) ID() ID             { return r.id }
func (r *Request) isJSONRPC2Message() {}
func (r *Request) isJSONRPC2Request() {}

// MarshalJSON implements json.Marshaler.
func (r *Request) MarshalJSON() ([]byte, error) {
	req := request{
		Method: r.method,
		Params: &r.params,
		ID:     &r.id,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return data, fmt.Errorf("marshaling call: %w", err)
	}

	return data, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *Request) UnmarshalJSON(data []byte) error {
	req := request{}
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshaling call: %w", err)
	}
	r.method = req.Method
	if req.Params != nil {
		r.params = *req.Params
	}
	if req.ID != nil {
		r.id = *req.ID
	}
	return nil
}

// Response is a reply to a Request.
//
// It will have the same ID as the call it is a response to.
type Response struct {
	// result is the content of the response.
	result RawMessage
	// err is set only if the call failed.
	err error
	// ID of the request this is a response to.
	id ID
}

// compile time check whether the Response implements a json.Marshaler and json.Unmarshaler interfaces.
var (
	_ json.Marshaler   = (*Response)(nil)
	_ json.Unmarshaler = (*Response)(nil)
)

// NewResponse constructs a new Response message that is a reply to the
// supplied. If err is set result may be ignored.
func NewResponse(id ID, result interface{}, err error) (*Response, error) {
	r, merr := marshalInterface(result)
	resp := &Response{
		id:     id,
		result: r,
		err:    err,
	}
	return resp, merr
}

func (r *Response) Result() RawMessage { return r.result }
func (r *Response) Err() error         { return r.err }
func (r *Response) ID() ID             { return r.id }
func (r *Response) isJSONRPC2Message() {}

// MarshalJSON implements json.Marshaler.
func (r *Response) MarshalJSON() ([]byte, error) {
	resp := &response{
		Error: toError(r.err),
		ID:    &r.id,
	}
	if resp.Error == nil {
		resp.Result = &r.result
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return data, fmt.Errorf("marshaling notification: %w", err)
	}
	return data, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *Response) UnmarshalJSON(data []byte) error {
	resp := response{}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("unmarshaling jsonrpc response: %w", err)
	}
	if resp.Result != nil {
		r.result = *resp.Result
	}
	if resp.Error != nil {
		r.err = resp.Error
	}
	if resp.ID != nil {
		r.id = *resp.ID
	}
	return nil
}

func toError(err error) *Error {
	if err == nil {
		// no error, the response is complete
		return nil
	}
	var wrapped *Error
	if errors.As(err, &wrapped) {
		// already a wire error, just use it
		return wrapped
	}
	result := &Error{Message: err.Error()}
	if errors.As(err, &wrapped) {
		// if we wrapped a wire error, keep the code from the wrapped error
		// but the message from the outer error
		result.Code = wrapped.Code
	}
	return result
}

// Notification is a request for which a response cannot occur, and as such
// it has not ID.
type Notification struct {
	// Method is a string containing the method name to invoke.
	method string

	params RawMessage
}

// compile time check whether the Response implements a json.Marshaler and json.Unmarshaler interfaces.
var (
	_ json.Marshaler   = (*Notification)(nil)
	_ json.Unmarshaler = (*Notification)(nil)
)

// NewNotification constructs a new Notification message for the supplied
// method and parameters.
func NewNotification(method string, params interface{}) (*Notification, error) {
	p, merr := marshalInterface(params)
	notify := &Notification{
		method: method,
		params: p,
	}
	return notify, merr
}

func (r *Notification) Method() string     { return r.method }
func (r *Notification) Params() RawMessage { return r.params }
func (r *Notification) isJSONRPC2Message() {}
func (r *Notification) isJSONRPC2Request() {}

// MarshalJSON implements json.Marshaler.
func (r *Notification) MarshalJSON() ([]byte, error) {
	req := request{
		Method: r.method,
		Params: &r.params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return data, fmt.Errorf("marshaling notification: %w", err)
	}
	return data, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *Notification) UnmarshalJSON(data []byte) error {
	req := request{}
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshaling notification: %w", err)
	}
	r.method = req.Method
	if req.Params != nil {
		r.params = *req.Params
	}
	return nil
}
