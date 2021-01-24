// Copyright 2020 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

// +build !gojay

package jsonrpc2

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"

	json "github.com/goccy/go-json"
)

// Call implemens Conn.
func (c *conn) Call(ctx context.Context, method string, params, result interface{}) (ID, error) {
	// generate a new request identifier
	id := ID{number: atomic.AddInt64(&c.seq, 1)}
	call, err := NewRequest(id, method, params)
	if err != nil {
		return id, fmt.Errorf("marshaling call parameters: %v", err)
	}

	// We have to add ourselves to the pending map before we send, otherwise we
	// are racing the response. Also add a buffer to rchan, so that if we get a
	// wire response between the time this call is cancelled and id is deleted
	// from c.pending, the send to rchan will not block.
	rchan := make(chan *Response, 1)

	c.pendingMu.Lock()
	c.pending[id] = rchan
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	// now we are ready to send
	_, err = c.write(ctx, call)
	if err != nil {
		// sending failed, we will never get a response, so don't leave it pending
		return id, err
	}

	// now wait for the response
	select {
	case response := <-rchan:
		switch {
		case response.err != nil:
			return id, response.err

		case result == nil || len(response.result) == 0:
			return id, nil

		default:
			dec := json.NewDecoder(bytes.NewReader(response.result))
			if err := dec.Decode(result); err != nil {
				return id, fmt.Errorf("unmarshaling result: %v", err)
			}
			return id, nil
		}

	case <-ctx.Done():
		return id, ctx.Err()
	}
}
