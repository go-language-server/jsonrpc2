// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"errors"
	"fmt"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

func TestError_Error(t *testing.T) {
	t.Parallel()

	if got := NewError(InvalidParams, "bad").Error(); got != "bad" {
		t.Errorf("Error() = %q, want %q", got, "bad")
	}
	var nilErr *Error
	if got := nilErr.Error(); got != "" {
		t.Errorf("nil Error() = %q, want empty", got)
	}
}

func TestErrorf(t *testing.T) {
	t.Parallel()

	e := Errorf(InternalError, "failed %d times", 3)
	if e.Code != InternalError {
		t.Errorf("Code = %d, want %d", e.Code, InternalError)
	}
	if e.Message != "failed 3 times" {
		t.Errorf("Message = %q, want %q", e.Message, "failed 3 times")
	}
}

func TestError_Is(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err    error
		target error
		want   bool
	}{
		"success: same code matches sentinel": {
			err:    NewError(ParseError, "custom message"),
			target: ErrParse,
			want:   true,
		},
		"success: different code does not match": {
			err:    NewError(ParseError, "x"),
			target: ErrInvalidRequest,
			want:   false,
		},
		"success: wrapped error matches via errors.Is": {
			err:    fmt.Errorf("context: %w", NewError(MethodNotFound, "x")),
			target: ErrMethodNotFound,
			want:   true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := errors.Is(tt.err, tt.target); got != tt.want {
				t.Errorf("errors.Is = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToWireError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err      error
		wantCode Code
		wantMsg  string
		wantNil  bool
	}{
		"success: nil maps to nil": {
			err:     nil,
			wantNil: true,
		},
		"success: wire error preserved verbatim": {
			err:      NewError(InvalidParams, "invalid params"),
			wantCode: InvalidParams,
			wantMsg:  "invalid params",
		},
		"success: plain error maps to message with zero code": {
			err:      errors.New("boom"),
			wantCode: 0,
			wantMsg:  "boom",
		},
		"success: wrapped wire error preserves code with outer message": {
			err:      fmt.Errorf("handler failed: %w", NewError(MethodNotFound, "method not found")),
			wantCode: MethodNotFound,
			wantMsg:  "handler failed: method not found",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := toWireError(tt.err)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("toWireError = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("toWireError = nil, want non-nil")
			}
			if got.Code != tt.wantCode {
				t.Errorf("Code = %d, want %d", got.Code, tt.wantCode)
			}
			if got.Message != tt.wantMsg {
				t.Errorf("Message = %q, want %q", got.Message, tt.wantMsg)
			}
		})
	}
}

func TestError_WireRoundTrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err *Error
	}{
		"success: code and message": {
			err: NewError(InvalidParams, "invalid params"),
		},
		"success: code message and data": {
			err: &Error{Code: InternalError, Message: "boom", Data: RawMessage(`{"k":"v"}`)},
		},
		"success: message needing escape": {
			err: NewError(ParseError, "line\t1 \"quoted\""),
		},
		"success: negative custom code": {
			err: NewError(Code(-32050), "custom"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			encoded := appendError(nil, tt.err)
			got, ok := decodeError(encoded)
			if !ok {
				t.Fatalf("decodeError failed for %q", encoded)
			}
			if diff := gocmp.Diff(tt.err, got); diff != "" {
				t.Errorf("error round-trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestError_CodePreservedThroughResponse(t *testing.T) {
	t.Parallel()

	// Code must survive encode -> decode of a full error response.
	resp := NewResponse(NewNumberID(1), nil, NewError(MethodNotFound, "method not found"))
	encoded, err := EncodeMessage(resp)
	if err != nil {
		t.Fatalf("EncodeMessage error: %v", err)
	}
	decoded, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatalf("DecodeMessage error: %v", err)
	}
	got, ok := decoded.(*Response)
	if !ok {
		t.Fatalf("decoded type = %T, want *Response", decoded)
	}
	var werr *Error
	if !errors.As(got.Err(), &werr) {
		t.Fatalf("response error is not *Error: %v", got.Err())
	}
	if werr.Code != MethodNotFound {
		t.Errorf("decoded code = %d, want %d", werr.Code, MethodNotFound)
	}
}

func TestToWireError_PreservesData(t *testing.T) {
	t.Parallel()

	inner := &Error{
		Code:    InvalidParams,
		Message: "invalid params",
		Data:    RawMessage(`{"field":"name"}`),
	}
	wrapped := fmt.Errorf("bad request: %w", inner)
	got := toWireError(wrapped)
	if got.Code != InvalidParams {
		t.Errorf("got code %v, want %v", got.Code, InvalidParams)
	}
	if string(got.Data) != `{"field":"name"}` {
		t.Errorf("got data %q, want %q", got.Data, `{"field":"name"}`)
	}
}
