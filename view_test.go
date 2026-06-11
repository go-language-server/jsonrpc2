// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bytes"
	"errors"
	"math"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"go.lsp.dev/jsonrpc2/conformance"
)

func TestScanMessageView_Conformance(t *testing.T) {
	t.Parallel()

	for _, v := range conformance.Valid() {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()

			got, err := ScanMessageView([]byte(v.Wire))
			if err != nil {
				t.Fatalf("ScanMessageView(%q) error: %v", v.Wire, err)
			}
			if got.Kind != wantViewKind(v.Kind) {
				t.Fatalf("Kind = %v, want %v", got.Kind, wantViewKind(v.Kind))
			}
			if got.JSONRPC == nil || string(got.JSONRPC) != `"2.0"` {
				t.Fatalf("JSONRPC = %q, want %q", got.JSONRPC, `"2.0"`)
			}
			if got.Kind == MessageViewCall || got.Kind == MessageViewNotification {
				if method := mustMethodString(t, got); method != v.Method {
					t.Fatalf("method = %q, want %q", method, v.Method)
				}
				if diff := gocmp.Diff(v.Params, string(got.Params)); diff != "" {
					t.Fatalf("params mismatch (-want +got):\n%s", diff)
				}
			}
			if got.Kind == MessageViewResponseResult {
				if diff := gocmp.Diff(v.Result, string(got.Result)); diff != "" {
					t.Fatalf("result mismatch (-want +got):\n%s", diff)
				}
			}
			if got.Kind == MessageViewResponseError {
				if int32(got.Error.Code) != v.ErrCode {
					t.Fatalf("error code = %d, want %d", got.Error.Code, v.ErrCode)
				}
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

func TestScanMessageView_DifferentialCorpus(t *testing.T) {
	t.Parallel()

	// FuzzScan in scan_test.go differentially checks DecodeMessage against an
	// encoding/json oracle. This corpus ties the borrowed view scanner to that
	// owned scanner over the shapes the scanner must get right: escaped
	// strings, duplicate fields, malformed inputs, batches, and large payloads.
	type testCase struct {
		name  string
		input []byte
		batch bool
	}
	var cases []testCase
	for _, v := range conformance.Valid() {
		cases = append(cases, testCase{name: "conformance/" + v.Name, input: []byte(v.Wire)})
	}
	for _, v := range conformance.Invalid() {
		cases = append(cases, testCase{name: "invalid/" + v.Name, input: []byte(v.Wire)})
	}
	for _, tc := range benchmarkDecodeEnvelopeCases {
		cases = append(cases, testCase{name: "benchmark/" + tc.name, input: tc.input, batch: tc.batch})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.batch {
				want, wantErr := ParseRequests(tc.input)
				got, gotErr := ScanRequestViews(tc.input)
				if (wantErr != nil) != (gotErr != nil) {
					t.Fatalf("batch error mismatch: ParseRequests=%v ScanRequestViews=%v", wantErr, gotErr)
				}
				if wantErr != nil {
					return
				}
				if len(got) != len(want) {
					t.Fatalf("batch len = %d, want %d", len(got), len(want))
				}
				for i := range want {
					compareParsedMessageView(t, tc.input, i, got[i], want[i])
				}
				return
			}

			want, wantErr := DecodeMessage(tc.input)
			got, gotErr := ScanMessageView(tc.input)
			if (wantErr != nil) != (gotErr != nil) {
				t.Fatalf("message error mismatch: DecodeMessage=%v ScanMessageView=%v", wantErr, gotErr)
			}
			if wantErr != nil {
				return
			}
			compareMessageView(t, tc.input, got, want)
		})
	}
}

func TestScanMessageView_BorrowedLifetimeInvalidatesOnFrameMutation(t *testing.T) {
	t.Parallel()

	frame := []byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"line":1},"id":7}`)
	view, err := ScanMessageView(frame)
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	if view.Kind != MessageViewCall {
		t.Fatalf("Kind = %v, want %v", view.Kind, MessageViewCall)
	}
	if got := string(view.MethodBytes); got != "textDocument/hover" {
		t.Fatalf("MethodBytes = %q, want textDocument/hover", got)
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

	// The retained view is intentionally borrowed. Mutating the source frame
	// mutates the observed spans, documenting the explicit lifetime contract.
	frame[bytes.IndexByte(frame, '1')] = '9'
	if got := string(view.Params); got != `{"line":9}` {
		t.Fatalf("borrowed Params after frame mutation = %q, want mutated view", got)
	}
}

func TestMessageViewCloneRetainsBytesAfterFrameMutation(t *testing.T) {
	t.Parallel()

	frame := []byte(`{"jsonrpc":"2.0","method":"m","params":{"k":"v"},"id":"abc"}`)
	view, err := ScanMessageView(frame)
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	cloned := view.Clone()

	for i := range frame {
		frame[i] = 'Z'
	}

	if got := string(cloned.MethodBytes); got != "m" {
		t.Fatalf("cloned MethodBytes = %q, want m", got)
	}
	if got := string(cloned.Params); got != `{"k":"v"}` {
		t.Fatalf("cloned Params = %q, want original params", got)
	}
	id, ok := cloned.ID.StringValue()
	if !ok || id != "abc" {
		t.Fatalf("cloned ID.StringValue = %q, %v; want abc, true", id, ok)
	}
}

func TestMessageViewOwnedReturnsSafeMessage(t *testing.T) {
	t.Parallel()

	frame := []byte(`{"jsonrpc":"2.0","method":"line\nbreak","params":{"k":"v"},"id":"a\u002fb"}`)
	view, err := ScanMessageView(frame)
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	msg, err := view.Owned()
	if err != nil {
		t.Fatalf("Owned error: %v", err)
	}

	for i := range frame {
		frame[i] = 'Z'
	}

	call, ok := msg.(*Call)
	if !ok {
		t.Fatalf("Owned returned %T, want *Call", msg)
	}
	if got := call.Method(); got != "line\nbreak" {
		t.Fatalf("owned method = %q, want decoded line break", got)
	}
	if got := string(call.Params()); got != `{"k":"v"}` {
		t.Fatalf("owned params = %q, want original params", got)
	}
	id, ok := call.ID().StringValue()
	if !ok || id != "a/b" {
		t.Fatalf("owned ID = %q, %v; want a/b, true", id, ok)
	}
}

func TestFrameViewBorrowedAndOwnedLifetime(t *testing.T) {
	t.Parallel()

	frame := []byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)
	borrowed, err := ScanFrameView(frame)
	if err != nil {
		t.Fatalf("ScanFrameView error: %v", err)
	}
	if borrowed.OwnsFrame() {
		t.Fatal("borrowed OwnsFrame = true, want false")
	}
	owned, err := NewFrameView(frame)
	if err != nil {
		t.Fatalf("NewFrameView error: %v", err)
	}
	if !owned.OwnsFrame() {
		t.Fatal("owned OwnsFrame = false, want true")
	}

	frame[bytes.Index(frame, []byte("true"))] = 'f'

	if got := string(borrowed.MessageView().Result); got != `{"ok":frue}` {
		t.Fatalf("borrowed result = %q, want mutated frame view", got)
	}
	if got := string(owned.MessageView().Result); got != `{"ok":true}` {
		t.Fatalf("owned result = %q, want original owned view", got)
	}

	cloned, err := borrowed.Clone()
	if err != nil {
		t.Fatalf("borrowed.Clone error after mutation: %v", err)
	}
	if !cloned.OwnsFrame() {
		t.Fatal("cloned OwnsFrame = false, want true")
	}
}

func TestScanRequestViewsBorrowedBatch(t *testing.T) {
	t.Parallel()

	frame := []byte(`[{"jsonrpc":"2.0","method":"one","id":1},{"jsonrpc":"2.0","method":1,"id":2},{"jsonrpc":"2.0","id":3,"result":true},{"jsonrpc":"2.0","method":"note","params":{"x":1}}]`)
	got, err := ScanRequestViews(frame)
	if err != nil {
		t.Fatalf("ScanRequestViews error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("ScanRequestViews len = %d, want 4", len(got))
	}
	if got[0].Err != nil || !got[0].Batch || got[0].View.Kind != MessageViewCall {
		t.Fatalf("entry 0 = %#v, want batch call", got[0])
	}
	if method := string(got[0].View.MethodBytes); method != "one" {
		t.Fatalf("entry 0 method = %q, want one", method)
	}
	if id, ok := got[0].View.ID.Number(); !ok || id != 1 {
		t.Fatalf("entry 0 id = %d, %v; want 1, true", id, ok)
	}
	if !errors.Is(got[1].Err, ErrInvalidRequest) || !got[1].Batch {
		t.Fatalf("entry 1 err = %v, batch = %v; want invalid request in batch", got[1].Err, got[1].Batch)
	}
	if !errors.Is(got[2].Err, ErrInvalidRequest) || !got[2].Batch {
		t.Fatalf("entry 2 err = %v, batch = %v; want invalid request in batch", got[2].Err, got[2].Batch)
	}
	if got[3].Err != nil || got[3].View.Kind != MessageViewNotification {
		t.Fatalf("entry 3 = %#v, want notification", got[3])
	}

	frame[bytes.Index(frame, []byte("one"))] = 't'
	if method := string(got[0].View.MethodBytes); method != "tne" {
		t.Fatalf("borrowed method after frame mutation = %q, want tne", method)
	}
}

func TestAppendRequestViewsReusesDestination(t *testing.T) {
	t.Parallel()

	dst := []ParsedMessageView{{Err: ErrUnknown}}
	got, err := AppendRequestViews(dst, []byte(`{"jsonrpc":"2.0","method":"m","id":1}`))
	if err != nil {
		t.Fatalf("AppendRequestViews error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("AppendRequestViews len = %d, want 2", len(got))
	}
	if got[0].Err != ErrUnknown {
		t.Fatalf("existing entry = %v, want ErrUnknown", got[0].Err)
	}
	if got[1].Err != nil || got[1].Batch || got[1].View.Kind != MessageViewCall {
		t.Fatalf("appended entry = %#v, want non-batch call", got[1])
	}
}

func TestScanMessageView_EscapedStringViews(t *testing.T) {
	t.Parallel()

	view, err := ScanMessageView([]byte(`{"jsonrpc":"2.0","method":"line\nbreak","id":"a\u002fb"}`))
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	if view.Kind != MessageViewCall {
		t.Fatalf("Kind = %v, want %v", view.Kind, MessageViewCall)
	}
	if got := string(view.MethodBytes); got != `line\nbreak` {
		t.Fatalf("MethodBytes = %q, want raw escaped method", got)
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

func TestScanMessageView_ResponseErrorBorrowed(t *testing.T) {
	t.Parallel()

	frame := []byte(`{"jsonrpc":"2.0","error":{"code":-32602,"message":"bad\nparams","data":{"field":"x"}},"id":2}`)
	view, err := ScanMessageView(frame)
	if err != nil {
		t.Fatalf("ScanMessageView error: %v", err)
	}
	if view.Kind != MessageViewResponseError {
		t.Fatalf("Kind = %v, want %v", view.Kind, MessageViewResponseError)
	}
	if got := string(view.ErrorRaw); got != `{"code":-32602,"message":"bad\nparams","data":{"field":"x"}}` {
		t.Fatalf("ErrorRaw = %q, want borrowed error object", got)
	}
	if view.Error.Code != InvalidParams {
		t.Fatalf("Error.Code = %d, want %d", view.Error.Code, InvalidParams)
	}
	if got := string(view.Error.MessageBytes); got != `bad\nparams` {
		t.Fatalf("Error.MessageBytes = %q, want raw escaped body", got)
	}
	if !view.Error.MessageEscaped {
		t.Fatal("Error.MessageEscaped = false, want true")
	}
	if msg, ok := view.Error.MessageString(); !ok || msg != "bad\nparams" {
		t.Fatalf("Error.MessageString = %q, %v; want decoded message", msg, ok)
	}
	if got := string(view.Error.Data); got != `{"field":"x"}` {
		t.Fatalf("Error.Data = %q, want borrowed data object", got)
	}

	owned, err := view.Owned()
	if err != nil {
		t.Fatalf("Owned error: %v", err)
	}
	resp := owned.(*Response)
	werr := resp.Err().(*Error)
	if werr.Message != "bad\nparams" || string(werr.Data) != `{"field":"x"}` {
		t.Fatalf("owned error = %#v, want decoded message and cloned data", werr)
	}
}

func TestScanMessageView_IDBoundaries(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

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
	t.Parallel()

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
		{name: "escaped method key ignored", wire: `{"jsonrpc":"2.0","m\u0065thod":"m","id":1}`, want: ErrInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ScanMessageView([]byte(tt.wire))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ScanMessageView = %#v, %v; want error %v", got, err, tt.want)
			}
		})
	}
}

func TestScanMessageView_AllocatesZero(t *testing.T) {
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

func compareParsedMessageView(t *testing.T, input []byte, i int, got ParsedMessageView, want *ParsedMessage) {
	t.Helper()
	if got.Batch != want.Batch {
		t.Fatalf("entry %d Batch = %v, want %v", i, got.Batch, want.Batch)
	}
	if (got.Err != nil) != (want.Err != nil) {
		t.Fatalf("entry %d Err = %v, want %v", i, got.Err, want.Err)
	}
	if got.Err != nil {
		if got.Err.Code != want.Err.Code {
			t.Fatalf("entry %d Err.Code = %d, want %d", i, got.Err.Code, want.Err.Code)
		}
		return
	}
	compareMessageView(t, input, got.View, want.Msg)
}

func compareMessageView(t *testing.T, input []byte, got MessageView, want Message) {
	t.Helper()
	if got.Kind != wantMessageViewKind(want) {
		t.Fatalf("Kind = %v, want %v for %q", got.Kind, wantMessageViewKind(want), input)
	}
	switch want := want.(type) {
	case *Call:
		if method := mustMethodString(t, got); method != want.Method() {
			t.Fatalf("method = %q, want %q for %q", method, want.Method(), input)
		}
		if !rawJSONEqual(t, got.Params, want.Params()) {
			t.Fatalf("params = %q, want %q for %q", got.Params, want.Params(), input)
		}
		gotID, ok := got.ID.ID()
		if !ok || gotID != want.ID() {
			t.Fatalf("id = %#v, %v; want %#v for %q", gotID, ok, want.ID(), input)
		}
	case *Notification:
		if method := mustMethodString(t, got); method != want.Method() {
			t.Fatalf("method = %q, want %q for %q", method, want.Method(), input)
		}
		if !rawJSONEqual(t, got.Params, want.Params()) {
			t.Fatalf("params = %q, want %q for %q", got.Params, want.Params(), input)
		}
	case *Response:
		gotID, ok := got.ID.ID()
		if !ok || gotID != want.ID() {
			t.Fatalf("id = %#v, %v; want %#v for %q", gotID, ok, want.ID(), input)
		}
		if respErr := want.Err(); respErr != nil {
			if got.Error.Code != respErr.(*Error).Code {
				t.Fatalf("error code = %d, want %d for %q", got.Error.Code, respErr.(*Error).Code, input)
			}
			return
		}
		if !rawJSONEqual(t, got.Result, want.Result()) {
			t.Fatalf("result = %q, want %q for %q", got.Result, want.Result(), input)
		}
	default:
		t.Fatalf("unexpected message type %T", want)
	}
}

func rawJSONEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	return jsonEqual(t, a, b)
}

func wantMessageViewKind(msg Message) MessageViewKind {
	switch msg := msg.(type) {
	case *Call:
		return MessageViewCall
	case *Notification:
		return MessageViewNotification
	case *Response:
		if msg.Err() != nil {
			return MessageViewResponseError
		}
		return MessageViewResponseResult
	default:
		return MessageViewInvalid
	}
}

func mustMethodString(t *testing.T, view MessageView) string {
	t.Helper()
	method, ok := view.MethodString()
	if !ok {
		t.Fatalf("MethodString failed for %#v", view)
	}
	return method
}
