// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

func TestEncodeMessage_Golden(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		msg  Message
		want string
	}{
		"success: call with object params": {
			msg:  NewCall(NewNumberID(1), "sum", RawMessage(`{"a":1,"b":2}`)),
			want: `{"jsonrpc":"2.0","method":"sum","params":{"a":1,"b":2},"id":1}`,
		},
		"success: call with array params and string id": {
			msg:  NewCall(NewStringID("abc"), "subtract", RawMessage(`[42,23]`)),
			want: `{"jsonrpc":"2.0","method":"subtract","params":[42,23],"id":"abc"}`,
		},
		"success: call without params": {
			msg:  NewCall(NewNumberID(2), "ping", nil),
			want: `{"jsonrpc":"2.0","method":"ping","id":2}`,
		},
		"success: call with empty-but-non-nil params omits params": {
			msg:  NewCall(NewNumberID(2), "ping", RawMessage{}),
			want: `{"jsonrpc":"2.0","method":"ping","id":2}`,
		},
		"success: call with method needing escape": {
			msg:  NewCall(NewNumberID(3), "a/b\nc", nil),
			want: `{"jsonrpc":"2.0","method":"a/b\nc","id":3}`,
		},
		"success: notification without params omits id": {
			msg:  NewNotification("update", nil),
			want: `{"jsonrpc":"2.0","method":"update"}`,
		},
		"success: notification with params": {
			msg:  NewNotification("notify", RawMessage(`[1,2,3]`)),
			want: `{"jsonrpc":"2.0","method":"notify","params":[1,2,3]}`,
		},
		"success: notification with empty-but-non-nil params omits params": {
			msg:  NewNotification("update", RawMessage{}),
			want: `{"jsonrpc":"2.0","method":"update"}`,
		},
		"success: success response with result": {
			msg:  NewResponse(NewNumberID(4), RawMessage(`19`), nil),
			want: `{"jsonrpc":"2.0","id":4,"result":19}`,
		},
		"success: success response with null result preserved": {
			msg:  NewResponse(NewNumberID(5), RawMessage(`null`), nil),
			want: `{"jsonrpc":"2.0","id":5,"result":null}`,
		},
		"success: success response with empty result defaults to null": {
			msg:  NewResponse(NewNumberID(6), nil, nil),
			want: `{"jsonrpc":"2.0","id":6,"result":null}`,
		},
		"success: success response with empty-but-non-nil result defaults to null": {
			msg:  NewResponse(NewNumberID(6), RawMessage{}, nil),
			want: `{"jsonrpc":"2.0","id":6,"result":null}`,
		},
		"success: error response with null id": {
			msg:  NewResponse(ID{}, nil, NewError(ParseError, "parse error")),
			want: `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`,
		},
		"success: error response with data": {
			msg: NewResponse(NewNumberID(7), nil, &Error{
				Code:    InvalidParams,
				Message: "invalid params",
				Data:    RawMessage(`{"detail":"x"}`),
			}),
			want: `{"jsonrpc":"2.0","id":7,"error":{"code":-32602,"message":"invalid params","data":{"detail":"x"}}}`,
		},
		"success: error response with empty-but-non-nil data omits data": {
			msg: NewResponse(NewNumberID(7), nil, &Error{
				Code:    InvalidParams,
				Message: "invalid params",
				Data:    RawMessage{},
			}),
			want: `{"jsonrpc":"2.0","id":7,"error":{"code":-32602,"message":"invalid params"}}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := EncodeMessage(tt.msg)
			if err != nil {
				t.Fatalf("EncodeMessage error: %v", err)
			}
			if diff := gocmp.Diff(tt.want, string(got)); diff != "" {
				t.Errorf("EncodeMessage mismatch (-want +got):\n%s", diff)
			}

			appended := AppendMessage([]byte("prefix:"), tt.msg)
			if diff := gocmp.Diff("prefix:"+tt.want, string(appended)); diff != "" {
				t.Errorf("AppendMessage mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAppendFields_Golden(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		got  []byte
		want string
	}{
		"call": {
			got:  AppendCall(nil, NewNumberID(1), "sum", RawMessage(`{"a":1,"b":2}`)),
			want: `{"jsonrpc":"2.0","method":"sum","params":{"a":1,"b":2},"id":1}`,
		},
		"notification": {
			got:  AppendNotification(nil, "notify", RawMessage(`[1,2,3]`)),
			want: `{"jsonrpc":"2.0","method":"notify","params":[1,2,3]}`,
		},
		"response": {
			got:  AppendResponse(nil, NewNumberID(4), RawMessage(`19`), nil),
			want: `{"jsonrpc":"2.0","id":4,"result":19}`,
		},
		"error response": {
			got:  AppendResponse(nil, ID{}, nil, NewError(ParseError, "parse error")),
			want: `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := gocmp.Diff(tt.want, string(tt.got)); diff != "" {
				t.Errorf("append helper mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEncodeMessage_OwnsBuffer(t *testing.T) {
	t.Parallel()

	// Encoding twice must not return aliased buffers, and the pool must not let
	// one result corrupt another.
	a, err := EncodeMessage(NewCall(NewNumberID(1), "a", RawMessage(`[1]`)))
	if err != nil {
		t.Fatalf("EncodeMessage error: %v", err)
	}
	b, err := EncodeMessage(NewCall(NewNumberID(2), "b", RawMessage(`[2]`)))
	if err != nil {
		t.Fatalf("EncodeMessage error: %v", err)
	}
	want := `{"jsonrpc":"2.0","method":"a","params":[1],"id":1}`
	if string(a) != want {
		t.Errorf("first result corrupted: got %q want %q", a, want)
	}
	for i := range a {
		a[i] = 'x'
	}
	if string(b) != `{"jsonrpc":"2.0","method":"b","params":[2],"id":2}` {
		t.Errorf("second result aliases first: %q", b)
	}
}
