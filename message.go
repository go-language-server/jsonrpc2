// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright 2021 The Go Language Server Authors

package jsonrpc2

import (
	stdjson "encoding/json"
	"errors"
	"fmt"

	json "github.com/goccy/go-json"
)

// Message is the interface to all JSON-RPC message types.
//
// They share no common functionality, but are a closed set of concrete types
// that are allowed to implement this interface.
//
// The message types are *Call, *Response and *Notification.
type Message interface {
	// jsonrpc2Message is used to make the set of message implementations a
	// closed set.
	jsonrpc2Message()
}

// Request is the shared interface to jsonrpc2 messages that request
// a method be invoked.
//
// The request types are a closed set of *Call and *Notification.
type Request interface {
	Message

	// Method is a string containing the method name to invoke.
	Method() string
	// Params is either a struct or an array with the parameters of the method.
	Params() json.RawMessage

	// jsonrpc2Request is used to make the set of request implementations closed.
	jsonrpc2Request()
}

// Call is a request that expects a response.
//
// The response will have a matching ID.
type Call struct {
	// Method is a string containing the method name to invoke.
	method string
	// Params is either a struct or an array with the parameters of the method.
	params json.RawMessage
	// id of this request, used to tie the Response back to the request.
	id ID
}

// compile time check whether the Request implements a json.Marshaler and json.Unmarshaler interfaces.
var (
	_ json.Marshaler   = (*Call)(nil)
	_ json.Unmarshaler = (*Call)(nil)
)

// NewCall constructs a new Call message for the supplied ID, method and
// parameters.
func NewCall(id ID, method string, params interface{}) (*Call, error) {
	p, merr := marshalInterface(params)
	req := &Call{
		id:     id,
		method: method,
		params: p,
	}
	return req, merr
}

// Method implements Request.
func (r *Call) Method() string { return r.method }

// Params implements Request.
func (r *Call) Params() json.RawMessage { return r.params }

// ID implements Request.
func (r *Call) ID() ID { return r.id }

// jsonrpc2Message implements Message.
func (r *Call) jsonrpc2Message() {}

// jsonrpc2Request implements Request.
func (r *Call) jsonrpc2Request() {}

// MarshalJSON implements json.Marshaler.
func (r *Call) MarshalJSON() ([]byte, error) {
	req := wireRequest{
		Method: r.method,
		Params: &r.params,
		ID:     &r.id,
	}
	data, err := stdjson.Marshal(req)
	if err != nil {
		return data, fmt.Errorf("marshaling call: %w", err)
	}

	return data, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *Call) UnmarshalJSON(data []byte) error {
	req := wireRequest{}
	if err := stdjson.Unmarshal(data, &req); err != nil {
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
	result json.RawMessage
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

// Result returns the Response result.
func (r *Response) Result() json.RawMessage { return r.result }

// Err returns the Response error.
func (r *Response) Err() error { return r.err }

// ID implements Request.
func (r *Response) ID() ID { return r.id }

// jsonrpc2Message implements Message.
func (r *Response) jsonrpc2Message() {}

// MarshalJSON implements json.Marshaler.
func (r *Response) MarshalJSON() ([]byte, error) {
	resp := &wireResponse{
		Error: toError(r.err),
		ID:    &r.id,
	}
	if resp.Error == nil {
		resp.Result = &r.result
	}

	data, err := stdjson.Marshal(resp)
	if err != nil {
		return data, fmt.Errorf("marshaling notification: %w", err)
	}
	return data, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *Response) UnmarshalJSON(data []byte) error {
	resp := wireResponse{}
	if err := stdjson.Unmarshal(data, &resp); err != nil {
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

	params json.RawMessage
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

// Method implements Request.
func (r *Notification) Method() string { return r.method }

// Params implements Request.
func (r *Notification) Params() json.RawMessage { return r.params }

// jsonrpc2Message implements Message.
func (r *Notification) jsonrpc2Message() {}

// jsonrpc2Request implements Request.
func (r *Notification) jsonrpc2Request() {}

// MarshalJSON implements json.Marshaler.
func (r *Notification) MarshalJSON() ([]byte, error) {
	req := wireRequest{
		Method: r.method,
		Params: &r.params,
	}
	data, err := stdjson.Marshal(req)
	if err != nil {
		return data, fmt.Errorf("marshaling notification: %w", err)
	}
	return data, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (r *Notification) UnmarshalJSON(data []byte) error {
	req := wireRequest{}
	if err := stdjson.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshaling notification: %w", err)
	}
	r.method = req.Method
	if req.Params != nil {
		r.params = *req.Params
	}
	return nil
}

// DecodeMessage decodes data to Message.
func DecodeMessage(data []byte) (Message, error) {
	msg := combined{}
	if err := json.UnmarshalNoEscape(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshaling jsonrpc message: %w", err)
	}

	if msg.Method == "" {
		// no method, should be a response
		if msg.ID == nil {
			return nil, ErrInvalidRequest
		}
		resp := &Response{
			id: *msg.ID,
		}
		if msg.Error != nil {
			resp.err = msg.Error
		}
		if msg.Result != nil {
			resp.result = *msg.Result
		}
		return resp, nil
	}

	// has a method, must be a request
	if msg.ID == nil {
		// request with no ID is a notify
		notify := &Notification{
			method: msg.Method,
		}
		if msg.Params != nil {
			notify.params = *msg.Params
		}
		return notify, nil
	}

	// request with an ID, must be a call
	req := &Call{
		method: msg.Method,
		id:     *msg.ID,
	}
	if msg.Params != nil {
		req.params = *msg.Params
	}
	return req, nil
}

// marshalInterface marshal obj to json.RawMessage.
func marshalInterface(obj interface{}) (json.RawMessage, error) {
	data, err := json.MarshalNoEscape(obj)
	if err != nil {
		return json.RawMessage{}, fmt.Errorf("failed to marshal json: %w", err)
	}
	return json.RawMessage(data), nil
}
