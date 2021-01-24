// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright 2021 The Go Language Server Authors

// +build !gojay

package jsonrpc2

import (
	"fmt"

	json "github.com/goccy/go-json"
)

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

// marshalInterface marshal obj to RawMessage.
func marshalInterface(obj interface{}) (RawMessage, error) {
	data, err := json.MarshalNoEscape(obj)
	if err != nil {
		return RawMessage{}, fmt.Errorf("failed to marshal json: %w", err)
	}
	return RawMessage(data), nil
}
