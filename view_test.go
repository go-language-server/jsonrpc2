// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestScanMessageView_CallBorrowed(t *testing.T) {
	frame := []byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"line":1},"id":7}`)

	view, err := ScanMessageView(frame)
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	if view.Kind != MessageViewCall {
		t.Fatalf("Kind = %v, want %v", view.Kind, MessageViewCall)
	}
	if got := string(view.Method); got != "textDocument/hover" {
		t.Fatalf("Method = %q, want textDocument/hover", got)
	}
	if view.MethodEscaped {
		t.Fatal("MethodEscaped = true, want false")
	}
	if got := string(view.Params); got != `{"line":1}` {
		t.Fatalf("Params = %q, want object", got)
	}
	id, ok := view.ID.Number()
	if !ok || id != 7 {
		t.Fatalf("ID.Number = %d, %v; want 7, true", id, ok)
	}

	// The view is intentionally borrowed: mutating the source frame mutates the
	// observed spans. This documents the fastest-path lifetime contract.
	frame[bytes.IndexByte(frame, '1')] = '9'
	if got := string(view.Params); got != `{"line":9}` {
		t.Fatalf("borrowed Params after frame mutation = %q, want mutated view", got)
	}
}

func TestScanMessageView_CallAllocatesZero(t *testing.T) {
	frame := []byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"line":1},"id":7}`)

	allocs := testing.AllocsPerRun(1000, func() {
		view, err := ScanMessageView(frame)
		if err != nil {
			t.Fatalf("ScanMessageView error: %v", err)
		}
		if view.Kind != MessageViewCall {
			t.Fatalf("Kind = %v, want %v", view.Kind, MessageViewCall)
		}
	})
	if allocs != 0 {
		t.Fatalf("ScanMessageView allocations = %v, want 0", allocs)
	}
}

func TestScanMessageView_NotificationNullParams(t *testing.T) {
	view, err := ScanMessageView([]byte(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":null}`))
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	if view.Kind != MessageViewNotification {
		t.Fatalf("Kind = %v, want %v", view.Kind, MessageViewNotification)
	}
	if view.ID.IsValid() {
		t.Fatal("notification ID unexpectedly valid")
	}
	if view.Params != nil {
		t.Fatalf("Params = %q, want nil for null params", view.Params)
	}
}

func TestScanMessageView_ResponseBorrowed(t *testing.T) {
	view, err := ScanMessageView([]byte(`{"jsonrpc":"2.0","id":"abc","result":{"ok":true}}`))
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	if view.Kind != MessageViewResponseResult {
		t.Fatalf("Kind = %v, want %v", view.Kind, MessageViewResponseResult)
	}
	id, ok := view.ID.StringBytes()
	if !ok || string(id) != "abc" {
		t.Fatalf("ID.StringBytes = %q, %v; want abc, true", id, ok)
	}
	if got := string(view.Result); got != `{"ok":true}` {
		t.Fatalf("Result = %q, want object", got)
	}
}

func TestScanMessageView_EscapedStringViews(t *testing.T) {
	view, err := ScanMessageView([]byte(`{"jsonrpc":"2.0","method":"line\nbreak","id":"a\u002fb"}`))
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	if view.Kind != MessageViewCall {
		t.Fatalf("Kind = %v, want %v", view.Kind, MessageViewCall)
	}
	if got := string(view.Method); got != `line\nbreak` {
		t.Fatalf("Method = %q, want raw escaped method", got)
	}
	if !view.MethodEscaped {
		t.Fatal("MethodEscaped = false, want true")
	}
	method, ok := view.MethodString()
	if !ok || method != "line\nbreak" {
		t.Fatalf("MethodString = %q, %v; want decoded line break", method, ok)
	}
	idBytes, ok := view.ID.StringBytes()
	if !ok || string(idBytes) != `a\u002fb` {
		t.Fatalf("ID.StringBytes = %q, %v; want raw escaped string", idBytes, ok)
	}
	if !view.ID.StringEscaped() {
		t.Fatal("ID.StringEscaped = false, want true")
	}
	id, ok := view.ID.StringValue()
	if !ok || id != "a/b" {
		t.Fatalf("ID.StringValue = %q, %v; want decoded a/b", id, ok)
	}
}

func TestScanMessageView_IDBoundaries(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want int64
	}{
		{name: "max", id: "9223372036854775807", want: math.MaxInt64},
		{name: "min", id: "-9223372036854775808", want: math.MinInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view, err := ScanMessageView([]byte(`{"jsonrpc":"2.0","method":"m","id":` + tt.id + `}`))
			if err != nil {
				t.Fatalf("ScanMessageView error: %v", err)
			}
			got, ok := view.ID.Number()
			if !ok || got != tt.want {
				t.Fatalf("ID.Number = %d, %v; want %d, true", got, ok, tt.want)
			}
		})
	}
}

func TestScanMessageView_Invalid(t *testing.T) {
	tests := []struct {
		name string
		wire string
		want error
	}{
		{name: "batch rejected", wire: `[]`, want: ErrInvalidRequest},
		{name: "trailing", wire: `{"jsonrpc":"2.0","id":1,"result":null}{}`, want: ErrParse},
		{name: "bad version", wire: `{"jsonrpc":"1.0","method":"m"}`, want: ErrInvalidRequest},
		{name: "mixed request response", wire: `{"jsonrpc":"2.0","method":"m","result":null}`, want: ErrInvalidRequest},
		{name: "fractional id", wire: `{"jsonrpc":"2.0","method":"m","id":1.5}`, want: ErrInvalidRequest},
		{name: "overflow id", wire: `{"jsonrpc":"2.0","method":"m","id":9223372036854775808}`, want: ErrInvalidRequest},
		{name: "bad escape", wire: `{"jsonrpc":"2.0","method":"bad\q"}`, want: ErrInvalidRequest},
		{name: "error null", wire: `{"jsonrpc":"2.0","id":1,"error":null}`, want: ErrInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ScanMessageView([]byte(tt.wire))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ScanMessageView = %#v, %v; want error %v", got, err, tt.want)
			}
		})
	}
}
