// Copyright 2019 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

// +build gojay

package jsonrpc2

import (
	"fmt"

	"github.com/francoispqt/gojay"
)

// MarshalJSONObject implements gojay.MarshalerJSONObject.
func (r *Call) MarshalJSONObject(enc *gojay.Encoder) {
	req := wireRequest{
		Method: r.method,
		Params: &r.params,
		ID:     &r.id,
	}
	enc.Object(&req)
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *Call) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *Call) UnmarshalJSONObject(dec *gojay.Decoder, _ string) error {
	req := wireRequest{}
	if err := dec.Decode(&req); err != nil {
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

// NKeys implements gojay.UnmarshalerJSONObject.
func (Call) NKeys() int { return 0 }

// compile time check whether the Call implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*Call)(nil)
	_ gojay.UnmarshalerJSONObject = (*Call)(nil)
)

// MarshalJSONObject implements gojay.MarshalerJSONObject.
func (r *Response) MarshalJSONObject(enc *gojay.Encoder) {
	resp := &wireResponse{
		Error: toError(r.err),
		ID:    &r.id,
	}
	if resp.Error == nil {
		resp.Result = &r.result
	}
	enc.Object(resp)
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *Response) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *Response) UnmarshalJSONObject(dec *gojay.Decoder, _ string) error {
	resp := wireResponse{}
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("unmarshaling call: %w", err)
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

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *Response) NKeys() int { return 0 }

// compile time check whether the Response implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*Response)(nil)
	_ gojay.UnmarshalerJSONObject = (*Response)(nil)
)

// MarshalJSONObject implements gojay.MarshalerJSONObject.
func (r *Notification) MarshalJSONObject(enc *gojay.Encoder) {
	req := wireRequest{
		Method: r.method,
		Params: &r.params,
	}
	enc.Object(&req)
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *Notification) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *Notification) UnmarshalJSONObject(dec *gojay.Decoder, _ string) error {
	req := wireRequest{}
	if err := dec.Decode(&req); err != nil {
		return fmt.Errorf("unmarshaling call: %w", err)
	}
	r.method = req.Method
	if req.Params != nil {
		r.params = *req.Params
	}
	return nil
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *Notification) NKeys() int { return 0 }

// compile time check whether the Notification implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*Notification)(nil)
	_ gojay.UnmarshalerJSONObject = (*Notification)(nil)
)

// marshalInterface marshal obj to RawMessage.
func marshalInterface(obj interface{}) (RawMessage, error) {
	data, err := gojay.MarshalAny(&obj)
	if err != nil {
		return RawMessage{}, err
	}
	return RawMessage(data), nil
}

// DecodeMessage decodes data to Message.
func DecodeMessage(data []byte) (Message, error) {
	msg := combined{}
	if err := gojay.Unsafe.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshaling JSON-RPC message: %w", err)
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
