// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bytes"
	"testing"

	"go.lsp.dev/jsonrpc2/conformance"
)

var messageViewSink MessageView

func TestScanMessageView_Conformance(t *testing.T) {
	t.Parallel()

	methods := NewMethodTable("sum", "subtract", "ping", "update", "notify", "m")
	for _, v := range conformance.Valid() {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()

			got, err := ScanMessageViewWithMethods([]byte(v.Wire), methods)
			if err != nil {
				t.Fatalf("ScanMessageView(%q) error: %v", v.Wire, err)
			}
			if got.Kind != wantViewKind(v.Kind) {
				t.Fatalf("kind = %v, want %v", got.Kind, wantViewKind(v.Kind))
			}
			if v.Method != "" {
				if gotMethod := methodString(t, got); gotMethod != v.Method {
					t.Fatalf("method = %q, want %q", gotMethod, v.Method)
				}
			}
			if v.Params != "" && string(got.Params) != v.Params {
				t.Fatalf("params = %q, want %q", got.Params, v.Params)
			}
			if v.Result != "" && string(got.Result) != v.Result {
				t.Fatalf("result = %q, want %q", got.Result, v.Result)
			}
			if v.ErrCode != 0 && int32(got.Error.Code) != v.ErrCode {
				t.Fatalf("error code = %d, want %d", got.Error.Code, v.ErrCode)
			}
		})
	}

	for _, v := range conformance.Invalid() {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			if got, err := ScanMessageView([]byte(v.Wire)); err == nil {
				t.Fatalf("ScanMessageView(%q) = %#v, want error", v.Wire, got)
			}
		})
	}
}

func TestScanMessageView_BorrowedSpans(t *testing.T) {
	t.Parallel()

	frame := []byte(`{"jsonrpc":"2.0","method":"m","params":{"k":"value"},"id":"abc"}`)
	view, err := ScanMessageView(frame)
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	if view.Kind != MessageViewCall {
		t.Fatalf("kind = %v, want call", view.Kind)
	}
	if !bytes.Equal(view.Method, []byte("m")) {
		t.Fatalf("method = %q, want m", view.Method)
	}
	if idBytes, ok := view.ID.StringBytes(); !ok || !bytes.Equal(idBytes, []byte("abc")) {
		t.Fatalf("id bytes = (%q, %v), want (abc, true)", idBytes, ok)
	}

	before := string(view.Params)
	pos := bytes.Index(frame, []byte("value"))
	if pos < 0 {
		t.Fatal("test frame missing value")
	}
	frame[pos] = 'V'
	if string(view.Params) == before {
		t.Fatalf("params did not reflect frame mutation; view appears copied: %q", view.Params)
	}
	if !bytes.Contains(view.Params, []byte("Value")) {
		t.Fatalf("params = %q, want borrowed mutation visible", view.Params)
	}
}

func TestScanMessageView_MethodTokens(t *testing.T) {
	t.Parallel()

	methods := NewMethodTable("initialize", "textDocument/didOpen")
	initID := methods.Lookup([]byte("initialize"))
	openID := methods.Lookup([]byte("textDocument/didOpen"))
	if initID == MethodUnknown || openID == MethodUnknown || initID == openID {
		t.Fatalf("unexpected method IDs: initialize=%d didOpen=%d", initID, openID)
	}
	if name, ok := methods.Name(openID); !ok || name != "textDocument/didOpen" {
		t.Fatalf("Name(%d) = (%q, %v), want didOpen", openID, name, ok)
	}

	view, err := ScanMessageViewWithMethods(
		[]byte(`{"jsonrpc":"2.0","method":"textDocument/didOpen","id":1}`),
		methods,
	)
	if err != nil {
		t.Fatalf("ScanMessageViewWithMethods error: %v", err)
	}
	if view.MethodID != openID {
		t.Fatalf("MethodID = %d, want %d", view.MethodID, openID)
	}
	if !bytes.Equal(view.Method, []byte("textDocument/didOpen")) {
		t.Fatalf("Method = %q, want textDocument/didOpen", view.Method)
	}

	escaped, err := ScanMessageViewWithMethods(
		[]byte(`{"jsonrpc":"2.0","method":"textDocument\/didOpen","id":1}`),
		methods,
	)
	if err != nil {
		t.Fatalf("escaped ScanMessageViewWithMethods error: %v", err)
	}
	if !escaped.MethodEscaped || escaped.MethodID != MethodUnknown || string(escaped.Method) != `textDocument\/didOpen` {
		t.Fatalf("escaped method view = {escaped:%v id:%d method:%q}, want escaped unknown raw body", escaped.MethodEscaped, escaped.MethodID, escaped.Method)
	}
	if s, ok := escaped.MethodString(); !ok || s != "textDocument/didOpen" {
		t.Fatalf("escaped MethodString = (%q, %v), want textDocument/didOpen", s, ok)
	}
}

func TestScanMessageView_NoAllocMinimal(t *testing.T) {
	methods := NewMethodTable("m")
	frame := []byte(`{"jsonrpc":"2.0","method":"m","id":1}`)

	allocs := testing.AllocsPerRun(1000, func() {
		view, err := ScanMessageViewWithMethods(frame, methods)
		if err != nil {
			t.Fatalf("ScanMessageViewWithMethods error: %v", err)
		}
		messageViewSink = view
	})
	if allocs != 0 {
		t.Fatalf("ScanMessageViewWithMethods allocs = %g, want 0", allocs)
	}
}

func BenchmarkDecodeMinimalView(b *testing.B) {
	methods := NewMethodTable("m")
	frame := []byte(`{"jsonrpc":"2.0","method":"m","id":1}`)

	b.ReportAllocs()
	for b.Loop() {
		view, err := ScanMessageViewWithMethods(frame, methods)
		if err != nil {
			b.Fatalf("ScanMessageViewWithMethods: %v", err)
		}
		messageViewSink = view
	}
}

func BenchmarkDecodeMediumView(b *testing.B) {
	methods := NewMethodTable("workspace/executeCommand")
	frame := []byte(`{"jsonrpc":"2.0","method":"workspace/executeCommand","params":{"command":"go.test","arguments":[{"uri":"file:///tmp/a.go","range":{"start":{"line":1,"character":2},"end":{"line":3,"character":4}}}]},"id":99}`)

	b.ReportAllocs()
	for b.Loop() {
		view, err := ScanMessageViewWithMethods(frame, methods)
		if err != nil {
			b.Fatalf("ScanMessageViewWithMethods: %v", err)
		}
		messageViewSink = view
	}
}

func wantViewKind(kind conformance.Kind) MessageViewKind {
	switch kind {
	case conformance.KindCall:
		return MessageViewCall
	case conformance.KindNotification:
		return MessageViewNotification
	case conformance.KindResponseResult:
		return MessageViewResponseResult
	case conformance.KindResponseError:
		return MessageViewResponseError
	default:
		return MessageViewInvalid
	}
}

func methodString(t *testing.T, view MessageView) string {
	t.Helper()
	if !view.MethodEscaped {
		return string(view.Method)
	}
	method, ok := view.MethodString()
	if !ok {
		t.Fatal("escaped method did not decode")
	}
	return method
}
