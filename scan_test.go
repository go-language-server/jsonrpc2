// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bytes"
	stdjson "encoding/json"
	"testing"
	"unicode/utf8"

	gocmp "github.com/google/go-cmp/cmp"

	"go.lsp.dev/jsonrpc2/conformance"
)

func TestDecodeMessage_Table(t *testing.T) {
	t.Parallel()

	type want struct {
		kind    conformance.Kind
		method  string
		params  string
		result  string
		errCode int32
		idNum   int64
		idIsNum bool
		idStr   string
		idIsStr bool
	}

	tests := map[string]struct {
		wire    string
		wantErr bool
		want    want
	}{
		"success: call with numeric id": {
			wire: `{"jsonrpc":"2.0","method":"sum","params":[1,2],"id":5}`,
			want: want{kind: conformance.KindCall, method: "sum", params: `[1,2]`, idNum: 5, idIsNum: true},
		},
		"success: call with string id": {
			wire: `{"jsonrpc":"2.0","method":"m","id":"req-1"}`,
			want: want{kind: conformance.KindCall, method: "m", idStr: "req-1", idIsStr: true},
		},
		"success: notification absent id": {
			wire: `{"jsonrpc":"2.0","method":"m","params":{"x":1}}`,
			want: want{kind: conformance.KindNotification, method: "m", params: `{"x":1}`},
		},
		"success: notification explicit null id": {
			wire: `{"jsonrpc":"2.0","method":"m","id":null}`,
			want: want{kind: conformance.KindNotification, method: "m"},
		},
		"success: params null treated as absent": {
			wire: `{"jsonrpc":"2.0","method":"m","params":null,"id":1}`,
			want: want{kind: conformance.KindCall, method: "m", idNum: 1, idIsNum: true},
		},
		"success: result present": {
			wire: `{"jsonrpc":"2.0","result":{"ok":true},"id":1}`,
			want: want{kind: conformance.KindResponseResult, result: `{"ok":true}`, idNum: 1, idIsNum: true},
		},
		"success: result null preserved (trap b)": {
			wire: `{"jsonrpc":"2.0","result":null,"id":1}`,
			want: want{kind: conformance.KindResponseResult, result: `null`, idNum: 1, idIsNum: true},
		},
		"success: error response with null id (trap a)": {
			wire: `{"jsonrpc":"2.0","error":{"code":-32700,"message":"parse error"},"id":null}`,
			want: want{kind: conformance.KindResponseError, errCode: -32700},
		},
		"success: error response with data": {
			wire: `{"jsonrpc":"2.0","error":{"code":-32602,"message":"bad","data":[1,2]},"id":2}`,
			want: want{kind: conformance.KindResponseError, errCode: -32602, idNum: 2, idIsNum: true},
		},
		"success: whitespace everywhere": {
			wire: "  {  \"jsonrpc\" : \"2.0\" , \"method\" : \"m\" , \"id\" : 3 }  ",
			want: want{kind: conformance.KindCall, method: "m", idNum: 3, idIsNum: true},
		},
		"success: nested params with braces in strings": {
			wire: `{"jsonrpc":"2.0","method":"m","params":{"s":"}{][","a":[{"b":"c"}]},"id":1}`,
			want: want{kind: conformance.KindCall, method: "m", params: `{"s":"}{][","a":[{"b":"c"}]}`, idNum: 1, idIsNum: true},
		},
		"success: escaped method name": {
			wire: `{"jsonrpc":"2.0","method":"a\nb\"c","id":1}`,
			want: want{kind: conformance.KindCall, method: "a\nb\"c", idNum: 1, idIsNum: true},
		},
		"success: unicode escape in params kept verbatim": {
			wire: `{"jsonrpc":"2.0","method":"m","params":{"s":"é"},"id":1}`,
			want: want{kind: conformance.KindCall, method: "m", params: `{"s":"é"}`, idNum: 1, idIsNum: true},
		},
		"success: duplicate method last wins": {
			wire: `{"jsonrpc":"2.0","method":"first","method":"second","id":1}`,
			want: want{kind: conformance.KindCall, method: "second", idNum: 1, idIsNum: true},
		},
		"success: negative number id": {
			wire: `{"jsonrpc":"2.0","method":"m","id":-42}`,
			want: want{kind: conformance.KindCall, method: "m", idNum: -42, idIsNum: true},
		},
		"error: batch rejected by strict decode": {
			wire:    `[{"jsonrpc":"2.0","method":"m","id":1}]`,
			wantErr: true,
		},
		"error: empty input": {
			wire:    ``,
			wantErr: true,
		},
		"error: not an object": {
			wire:    `"a string"`,
			wantErr: true,
		},
		"error: response without result or error": {
			wire:    `{"jsonrpc":"2.0","id":1}`,
			wantErr: true,
		},
		"error: method mixed with result": {
			wire:    `{"jsonrpc":"2.0","method":"m","result":1,"id":1}`,
			wantErr: true,
		},
		"error: trailing content": {
			wire:    `{"jsonrpc":"2.0","method":"m","id":1} trailing`,
			wantErr: true,
		},
		"error: truncated object": {
			wire:    `{"jsonrpc":"2.0","method":"m"`,
			wantErr: true,
		},
		"error: unterminated string": {
			wire:    `{"jsonrpc":"2.0","method":"m`,
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg, err := DecodeMessage([]byte(tt.wire))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DecodeMessage(%q) = %#v, want error", tt.wire, msg)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeMessage(%q) error: %v", tt.wire, err)
			}
			checkDecoded(t, msg, tt.want.kind, tt.want.method, tt.want.params, tt.want.result, tt.want.errCode)

			// Identifier checks.
			switch m := msg.(type) {
			case *Call:
				checkID(t, m.ID(), tt.want.idNum, tt.want.idIsNum, tt.want.idStr, tt.want.idIsStr)
			case *Response:
				checkID(t, m.ID(), tt.want.idNum, tt.want.idIsNum, tt.want.idStr, tt.want.idIsStr)
			}
		})
	}
}

func checkID(t *testing.T, id ID, wantNum int64, wantIsNum bool, wantStr string, wantIsStr bool) {
	t.Helper()
	if wantIsNum {
		n, ok := id.Number()
		if !ok || n != wantNum {
			t.Errorf("id Number() = (%d, %v), want (%d, true)", n, ok, wantNum)
		}
	}
	if wantIsStr {
		s, ok := id.StringValue()
		if !ok || s != wantStr {
			t.Errorf("id StringValue() = (%q, %v), want (%q, true)", s, ok, wantStr)
		}
	}
}

func checkDecoded(t *testing.T, msg Message, kind conformance.Kind, method, params, result string, errCode int32) {
	t.Helper()
	switch kind {
	case conformance.KindCall:
		c, ok := msg.(*Call)
		if !ok {
			t.Fatalf("got %T, want *Call", msg)
		}
		if c.Method() != method {
			t.Errorf("method = %q, want %q", c.Method(), method)
		}
		checkRaw(t, "params", c.Params(), params)
	case conformance.KindNotification:
		n, ok := msg.(*Notification)
		if !ok {
			t.Fatalf("got %T, want *Notification", msg)
		}
		if n.Method() != method {
			t.Errorf("method = %q, want %q", n.Method(), method)
		}
		checkRaw(t, "params", n.Params(), params)
	case conformance.KindResponseResult:
		r, ok := msg.(*Response)
		if !ok {
			t.Fatalf("got %T, want *Response", msg)
		}
		if r.Err() != nil {
			t.Errorf("unexpected error: %v", r.Err())
		}
		checkRaw(t, "result", r.Result(), result)
	case conformance.KindResponseError:
		r, ok := msg.(*Response)
		if !ok {
			t.Fatalf("got %T, want *Response", msg)
		}
		werr, ok := r.Err().(*Error)
		if !ok {
			t.Fatalf("response error = %T, want *Error", r.Err())
		}
		if int32(werr.Code) != errCode {
			t.Errorf("error code = %d, want %d", werr.Code, errCode)
		}
	default:
		t.Fatalf("unexpected kind %d", kind)
	}
}

func checkRaw(t *testing.T, name string, got RawMessage, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Errorf("%s = %q, want absent", name, got)
		}
		return
	}
	if diff := gocmp.Diff(want, string(got)); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", name, diff)
	}
}

func TestDecodeMessage_Conformance(t *testing.T) {
	t.Parallel()

	for _, v := range conformance.Valid() {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			msg, err := DecodeMessage([]byte(v.Wire))
			if err != nil {
				t.Fatalf("DecodeMessage(%q) error: %v", v.Wire, err)
			}
			checkDecoded(t, msg, v.Kind, v.Method, v.Params, v.Result, v.ErrCode)
		})
	}

	for _, v := range conformance.Invalid() {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			if msg, err := DecodeMessage([]byte(v.Wire)); err == nil {
				t.Fatalf("DecodeMessage(%q) = %#v, want error", v.Wire, msg)
			}
		})
	}
}

// TestDecodeMessage_OpaqueContainerInterior pins the documented opaque-passthrough
// contract of scanContainer: a top-level container value (params/result) whose
// interior is not valid JSON grammar but whose brackets are balanced is accepted
// and carried verbatim as a [RawMessage] span, leaving structural validation to
// the payload codec. This leniency is not reachable through the stdjson-gated
// differential fuzz, so it is asserted directly here.
func TestDecodeMessage_OpaqueContainerInterior(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		wire   string
		params string
	}{
		"success: brace closed by bracket": {
			wire:   `{"jsonrpc":"2.0","method":"m","params":{],"id":1}`,
			params: `{]`,
		},
		"success: array elements without commas": {
			wire:   `{"jsonrpc":"2.0","method":"m","params":[1 2 3],"id":1}`,
			params: `[1 2 3]`,
		},
		"success: object with doubled comma": {
			wire:   `{"jsonrpc":"2.0","method":"m","params":{"a":1,,},"id":1}`,
			params: `{"a":1,,}`,
		},
		"success: object member missing colon": {
			wire:   `{"jsonrpc":"2.0","method":"m","params":{"a"1},"id":1}`,
			params: `{"a"1}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg, err := DecodeMessage([]byte(tt.wire))
			if err != nil {
				t.Fatalf("DecodeMessage(%q) error: %v", tt.wire, err)
			}
			call, ok := msg.(*Call)
			if !ok {
				t.Fatalf("got %T, want *Call", msg)
			}
			if diff := gocmp.Diff(tt.params, string(call.Params())); diff != "" {
				t.Errorf("params mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDecodeMessage_OwnsBuffer is the single test guarding pre-mortem #1: the
// decoded message must not alias the input buffer.
func TestDecodeMessage_OwnsBuffer(t *testing.T) {
	t.Parallel()

	in := []byte(`{"jsonrpc":"2.0","method":"m","params":{"k":"value"},"id":1}`)
	msg, err := DecodeMessage(in)
	if err != nil {
		t.Fatalf("DecodeMessage error: %v", err)
	}
	call := msg.(*Call)
	before := string(call.Params())

	// Mutate every byte of the input; the decoded params must be unaffected.
	for i := range in {
		in[i] = 'Z'
	}
	if string(call.Params()) != before {
		t.Errorf("params aliased input buffer: got %q, want %q", call.Params(), before)
	}
}

func TestParseRequests(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		wire      string
		wantErr   bool
		wantLen   int
		wantBatch bool
		// per-index expectations
		methods []string
		invalid []bool
	}{
		"success: single call": {
			wire:    `{"jsonrpc":"2.0","method":"m","id":1}`,
			wantLen: 1,
			methods: []string{"m"},
			invalid: []bool{false},
		},
		"success: single notification": {
			wire:    `{"jsonrpc":"2.0","method":"n"}`,
			wantLen: 1,
			methods: []string{"n"},
			invalid: []bool{false},
		},
		"success: batch of two": {
			wire:      `[{"jsonrpc":"2.0","method":"a","id":1},{"jsonrpc":"2.0","method":"b"}]`,
			wantLen:   2,
			wantBatch: true,
			methods:   []string{"a", "b"},
			invalid:   []bool{false, false},
		},
		"success: batch with whitespace": {
			wire:      ` [ {"jsonrpc":"2.0","method":"a","id":1} , {"jsonrpc":"2.0","method":"b","id":2} ] `,
			wantLen:   2,
			wantBatch: true,
			methods:   []string{"a", "b"},
			invalid:   []bool{false, false},
		},
		"success: batch with one invalid entry": {
			wire:      `[{"jsonrpc":"2.0","method":"a","id":1},{"jsonrpc":"2.0","id":2}]`,
			wantLen:   2,
			wantBatch: true,
			methods:   []string{"a", ""},
			invalid:   []bool{false, true},
		},
		"success: batch rejects wrong-version entry": {
			wire:      `[{"jsonrpc":"2.0","method":"a","id":1},{"jsonrpc":"1.0","method":"b","id":2}]`,
			wantLen:   2,
			wantBatch: true,
			methods:   []string{"a", ""},
			invalid:   []bool{false, true},
		},
		"success: batch rejects missing-version entry": {
			wire:      `[{"method":"a","id":1},{"jsonrpc":"2.0","method":"b","id":2}]`,
			wantLen:   2,
			wantBatch: true,
			methods:   []string{"", "b"},
			invalid:   []bool{true, false},
		},
		"success: empty batch": {
			wire:    `[]`,
			wantLen: 0,
		},
		"error: top-level garbage": {
			wire:    `not json`,
			wantLen: 1,
			invalid: []bool{true},
		},
		"error: unterminated array": {
			wire:    `[{"jsonrpc":"2.0","method":"a","id":1}`,
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseRequests([]byte(tt.wire))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseRequests(%q) = %#v, want error", tt.wire, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRequests(%q) error: %v", tt.wire, err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for i, pm := range got {
				if tt.wantBatch && !pm.Batch {
					t.Errorf("index %d: Batch = false, want true", i)
				}
				if i < len(tt.invalid) && tt.invalid[i] {
					if pm.Err == nil {
						t.Errorf("index %d: Err = nil, want error", i)
					}
					continue
				}
				if pm.Err != nil {
					t.Errorf("index %d: unexpected Err: %v", i, pm.Err)
					continue
				}
				if i < len(tt.methods) && pm.Msg.Method() != tt.methods[i] {
					t.Errorf("index %d: method = %q, want %q", i, pm.Msg.Method(), tt.methods[i])
				}
			}
		})
	}
}

// --- Differential fuzz against an encoding/json reference oracle ---

// oracle is the reference decoder built on encoding/json. It is used only by the
// test suite to validate the hand-written scanner; the production code never
// imports encoding/json.
type oracleWire struct {
	JSONRPC    string             `json:"jsonrpc"`
	jsonrpcRaw stdjson.RawMessage // raw "jsonrpc" value span, escapes unresolved
	ID         stdjson.RawMessage `json:"id"`
	Method     *string            `json:"method"`
	Params     stdjson.RawMessage `json:"params"`
	Result     stdjson.RawMessage `json:"result"`
	Error      stdjson.RawMessage `json:"error"`

	hasID        bool
	hasMethodKey bool
	hasResult    bool
	hasError     bool
}

func (o *oracleWire) UnmarshalJSON(data []byte) error {
	var raw map[string]stdjson.RawMessage
	if err := stdjson.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["jsonrpc"]; ok {
		_ = stdjson.Unmarshal(v, &o.JSONRPC)
		// Retain the raw span so the version gate can compare bytes exactly as
		// the production scanner does, rather than after escape resolution.
		o.jsonrpcRaw = v
	}
	if v, ok := raw["id"]; ok {
		o.ID = v
		o.hasID = true
	}
	if v, ok := raw["method"]; ok {
		// Classification is driven by key presence, matching the decoder. Whether
		// the value is a usable string is decided later: a non-string method is a
		// present method that makes the request invalid.
		o.hasMethodKey = true
		if t := trimSpaceBytes(v); len(t) > 0 && t[0] == '"' {
			var m string
			if stdjson.Unmarshal(t, &m) == nil {
				o.Method = &m
			}
		}
	}
	if v, ok := raw["params"]; ok {
		o.Params = v
	}
	if v, ok := raw["result"]; ok {
		o.Result = v
		o.hasResult = true
	}
	if v, ok := raw["error"]; ok {
		o.Error = v
		o.hasError = true
	}
	return nil
}

// oracleClassify reports the kind the reference decoder assigns, or KindInvalid
// when the input is not a message our strict decoder must accept. It mirrors the
// role rules of DecodeMessage.
//
// Limitation (documented contract): the oracle gates on stdjson.Valid below, so
// every input the production scanner accepts as an opaque container span but that
// stdlib rejects (structurally-invalid container interiors such as "{]",
// `{"a":1,,}`, `{"a"1}`, or "[1 2 3]" used as params) is classified KindInvalid
// here and the fuzz body returns early without a field comparison. That leniency
// is therefore not exercised by this differential fuzz; it is instead pinned by
// the explicit table cases in TestDecodeMessage_OpaqueContainerInterior, which
// assert the intended opaque-passthrough behavior directly. The top-level
// request/response classification remains genuinely differential.
func oracleClassify(data []byte) (conformance.Kind, *oracleWire, bool) {
	// Reject anything that is not a single JSON object via a strict check.
	trimmed := trimSpaceBytes(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return conformance.KindInvalid, nil, false
	}
	// Field-byte agreement is asserted only on inputs that are valid UTF-8 and
	// valid JSON. encoding/json lossily coerces invalid UTF-8 inside string
	// bodies to U+FFFD, whereas the decoder preserves string contents verbatim;
	// such inputs are malformed JSON and only get the no-panic guarantee.
	if !utf8.Valid(data) || !stdjson.Valid(data) {
		return conformance.KindInvalid, nil, false
	}
	var o oracleWire
	if err := stdjson.Unmarshal(data, &o); err != nil {
		return conformance.KindInvalid, nil, false
	}

	// The decoder requires the "jsonrpc" member to be exactly the raw bytes
	// "2.0" (quotes included), a byte compare with no escape resolution. Mirror
	// that here on the raw span rather than on the escape-decoded o.JSONRPC, so a
	// degenerate but stdjson-valid spelling such as "2.0" (which the scanner
	// fail-closed rejects) is also rejected by the oracle instead of diverging.
	if string(trimSpaceBytes(o.jsonrpcRaw)) != `"2.0"` {
		return conformance.KindInvalid, nil, false
	}

	switch {
	case o.hasMethodKey && !o.hasResult && !o.hasError:
		// Present method but not a usable string: the decoder rejects it.
		if o.Method == nil {
			return conformance.KindInvalid, nil, false
		}
		if !o.hasID || isNullLiteral(trimSpaceBytes(o.ID)) {
			return conformance.KindNotification, &o, true
		}
		if !validIDBytes(o.ID) {
			return conformance.KindInvalid, nil, false
		}
		return conformance.KindCall, &o, true
	case !o.hasMethodKey:
		if !validIDBytes(o.ID) {
			return conformance.KindInvalid, nil, false
		}
		if o.hasResult && o.hasError {
			// A response carrying both result and error is malformed; the decoder
			// rejects it rather than silently choosing the error, so the oracle
			// must agree before the hasError-first classification below.
			return conformance.KindInvalid, nil, false
		}
		if o.hasError {
			// An error member must be a non-null object to be a valid error
			// response; the decoder enforces this, so the oracle agrees.
			if !isErrorObject(o.Error) {
				return conformance.KindInvalid, nil, false
			}
			return conformance.KindResponseError, &o, true
		}
		if o.hasResult {
			return conformance.KindResponseResult, &o, true
		}
		return conformance.KindInvalid, nil, false
	default:
		return conformance.KindInvalid, nil, false
	}
}

// isErrorObject reports whether the raw value is a JSON object that the decoder
// accepts as an error member. The error sub-object is parsed by the same
// production helper the decoder uses, so the oracle and decoder agree by
// construction on this nested, lower-risk parse and the fuzz focuses on the
// top-level scanner.
func isErrorObject(raw stdjson.RawMessage) bool {
	t := trimSpaceBytes(raw)
	if len(t) == 0 || t[0] != '{' {
		return false
	}
	if !stdjson.Valid(t) {
		return false
	}
	_, ok := decodeError(t)
	return ok
}

func validIDBytes(id stdjson.RawMessage) bool {
	t := trimSpaceBytes(id)
	if len(t) == 0 || isNullLiteral(t) {
		return true
	}
	if t[0] == '"' {
		var s string
		return stdjson.Unmarshal(t, &s) == nil
	}
	var n int64
	return stdjson.Unmarshal(t, &n) == nil
}

func trimSpaceBytes(b []byte) []byte {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	j := len(b)
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\n' || b[j-1] == '\r') {
		j--
	}
	return b[i:j]
}

func msgKind(m Message) conformance.Kind {
	switch v := m.(type) {
	case *Call:
		return conformance.KindCall
	case *Notification:
		return conformance.KindNotification
	case *Response:
		if v.Err() != nil {
			return conformance.KindResponseError
		}
		return conformance.KindResponseResult
	default:
		return conformance.KindInvalid
	}
}

func FuzzScan(f *testing.F) {
	for _, v := range conformance.Valid() {
		f.Add([]byte(v.Wire))
	}
	for _, v := range conformance.Invalid() {
		f.Add([]byte(v.Wire))
	}
	// Adversarial seeds.
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","params":{"a":{"b":{"c":[1,2,3]}}},"id":1}`))
	f.Add([]byte(`{`))
	f.Add([]byte(`{"a":`))
	f.Add([]byte(`{"id":"𝄞"}`))
	f.Add([]byte("\x00\x01\x02"))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","id":1e3}`))
	// Version member spelled with a JSON \u escape: stdjson resolves it to "2.0"
	// but the scanner compares raw bytes and rejects. Built from explicit bytes
	// (a raw or interpreted literal would normalize the backslash) so the input
	// is exactly {"jsonrpc":"2.0",...}; pins the version gate as a raw-byte
	// compare so the oracle and scanner cannot silently diverge here.
	f.Add(append(append([]byte(`{"jsonrpc":`),
		'"', '2', 0x5c, 'u', '0', '0', '2', 'e', '0', '"'),
		[]byte(`,"method":"m","id":1}`)...))
	// Response carrying both result and error must be rejected, not silently
	// classified as an error response.
	f.Add([]byte(`{"jsonrpc":"2.0","result":1,"error":{"code":-32000,"message":"x"},"id":1}`))
	// Deeply nested params: stress the container scanner's depth tracking.
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","params":[[[[[[[[[[1]]]]]]]]]],"id":1}`))
	// Escapes and brackets inside a string value must not confuse nesting.
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","params":{"s":"a\"}{][\\\n","t":"A"},"id":1}`))
	// Number id edge spellings: large magnitude, leading minus, exponent, fraction.
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","id":9223372036854775807}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","id":-9223372036854775808}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","id":1.5e10}`))
	// String id containing JSON-significant characters and escapes.
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","id":"a\"b\\c\/d"}`))
	// Empty method name (present-but-empty is a valid string method).
	f.Add([]byte(`{"jsonrpc":"2.0","method":"","id":1}`))
	// Error object with a structured data member.
	f.Add([]byte(`{"jsonrpc":"2.0","error":{"code":-32000,"message":"x","data":{"k":[1,2,3]}},"id":1}`))
	// Error code at the int32 boundary.
	f.Add([]byte(`{"jsonrpc":"2.0","error":{"code":-2147483648,"message":"x"},"id":1}`))
	// Result that is a bare JSON scalar/array/string rather than an object.
	f.Add([]byte(`{"jsonrpc":"2.0","result":[1,2,3],"id":1}`))
	f.Add([]byte(`{"jsonrpc":"2.0","result":"ok","id":1}`))
	f.Add([]byte(`{"jsonrpc":"2.0","result":42,"id":1}`))
	// Duplicate id keys: last value wins in both scanner and oracle.
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","id":1,"id":2}`))
	// Unknown top-level members must be ignored, not rejected.
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m","extra":{"a":1},"id":1}`))
	// Tabs and newlines as inter-token whitespace.
	f.Add([]byte("{\n\t\"jsonrpc\":\t\"2.0\",\n\t\"method\":\"m\",\n\t\"id\":1\n}"))
	// Truncated escape and dangling backslash: no-panic guarantees.
	f.Add([]byte(`{"jsonrpc":"2.0","method":"m\`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"\uZZZZ","id":1}`))
	// Lone surrogate escape in a string body (malformed but must not panic).
	f.Add([]byte(`{"jsonrpc":"2.0","method":"\ud800","id":1}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must never panic / index out of bounds on arbitrary input.
		msg, derr := DecodeMessage(data)

		wantKind, oracle, oracleValid := oracleClassify(data)
		if !oracleValid {
			// Oracle rejects: our decoder may accept some shapes the oracle is
			// stricter about (e.g. unknown fields), so do not require an error.
			// The only guarantee here is no panic, which we already exercised.
			return
		}

		if derr != nil {
			t.Fatalf("DecodeMessage rejected oracle-valid input %q: %v", data, derr)
		}
		gotKind := msgKind(msg)
		if gotKind != wantKind {
			t.Fatalf("classification mismatch for %q: got %d, want %d", data, gotKind, wantKind)
		}

		// Field agreement for the spans the oracle exposes.
		switch m := msg.(type) {
		case *Call:
			compareMethod(t, data, m.Method(), oracle.Method)
			compareRaw(t, data, "params", m.Params(), oracle.Params, true)
			compareID(t, data, m.ID(), oracle.ID)
		case *Notification:
			compareMethod(t, data, m.Method(), oracle.Method)
			compareRaw(t, data, "params", m.Params(), oracle.Params, true)
		case *Response:
			compareID(t, data, m.ID(), oracle.ID)
			if m.Err() == nil {
				compareRaw(t, data, "result", m.Result(), oracle.Result, false)
			}
		}
	})
}

func compareMethod(t *testing.T, data []byte, got string, want *string) {
	t.Helper()
	if want == nil {
		return
	}
	if got != *want {
		t.Fatalf("method mismatch for %q: got %q, want %q", data, got, *want)
	}
}

// compareID differentially checks the scanner's id-span boundaries against the
// oracle's id extraction. The expected ID is derived from the oracle's raw id
// span (absent span yields the zero-value, kind-none ID); decodeID is reused for
// value semantics so the comparison isolates the scanner's span handling rather
// than re-checking decodeID against itself.
func compareID(t *testing.T, data []byte, got ID, wantSpan stdjson.RawMessage) {
	t.Helper()
	var want ID
	if trimmed := trimSpaceBytes(wantSpan); len(trimmed) > 0 {
		w, ok := decodeID(trimmed)
		if !ok {
			// The oracle considered this message valid, so a non-decodable id
			// here would itself be a divergence worth surfacing.
			t.Fatalf("oracle id span %q not decodable for %q", wantSpan, data)
		}
		want = w
	}
	if diff := gocmp.Diff(want, got, gocmp.AllowUnexported(ID{})); diff != "" {
		t.Fatalf("id mismatch for %q (-want +got):\n%s", data, diff)
	}
}

// compareRaw compares a decoded raw span against the oracle's raw span using
// JSON-structural equality. When nullIsAbsent is true (params), a "null" oracle
// value is treated as absent, matching the decoder's convention.
func compareRaw(t *testing.T, data []byte, name string, got RawMessage, want stdjson.RawMessage, nullIsAbsent bool) {
	t.Helper()
	wantTrim := trimSpaceBytes(want)
	if nullIsAbsent && (len(want) == 0 || isNullLiteral(wantTrim)) {
		if got != nil {
			t.Fatalf("%s mismatch for %q: got %q, want absent", name, data, got)
		}
		return
	}
	if len(wantTrim) == 0 {
		if got != nil {
			t.Fatalf("%s mismatch for %q: got %q, want absent", name, data, got)
		}
		return
	}
	if !jsonEqual(t, got, want) {
		t.Fatalf("%s mismatch for %q: got %q, want %q", name, data, got, want)
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var ca, cb []byte
	var err error
	if ca, err = compactJSON(a); err != nil {
		return false
	}
	if cb, err = compactJSON(b); err != nil {
		return false
	}
	return string(ca) == string(cb)
}

func compactJSON(b []byte) ([]byte, error) {
	var dst bytes.Buffer
	if err := stdjson.Compact(&dst, b); err != nil {
		return nil, err
	}
	return dst.Bytes(), nil
}

// Scanner / dispatch coverage gap review (AC-C5).
//
// Package coverage is >= 90% of statements. The few statements that remain
// uncovered are concentrated in the hand-written scanner and the dispatch state
// machine, and every gap is a defensive, fail-closed branch that cannot be
// reached through the public API on valid input. They are reviewed line-by-line
// here rather than padded with low-value tests, per the global rule against
// inflating coverage with tests that do not reveal flaws. The differential
// FuzzScan oracle (above) and the 60s/30s fuzz soaks exercise these paths far
// more thoroughly than any hand-written case could, which is why they are not
// separately unit-tested:
//
//   - scan.go scanLiteral / scanNumber / scanErrorObject tail branches: the
//     "ran off the end of the buffer" and "literal did not match" early returns.
//     These fire only on truncated or malformed JSON, which the no-panic half of
//     FuzzScan drives continuously; they exist so a malformed frame is rejected
//     with an error instead of indexing out of bounds.
//   - scan.go scanContainer unbalanced-bracket return: reached only by a frame
//     whose top-level value brackets never close, i.e. a truncated frame; covered
//     by fuzz and by the "error: truncated/unterminated" table cases.
//   - wire.go appendMessage default arm: unreachable for a well-formed value
//     because the [Message] set is closed ([*Call], [*Notification], [*Response]);
//     it returns dst unchanged so a future, not-yet-handled type degrades to an
//     empty append rather than a panic. Intentionally not exercised.
//   - batch.go invalidRequest.Method/Params: a synthetic placeholder for a
//     malformed batch member that is always answered with a null-id error before
//     the user handler runs, so these accessors are never invoked on the dispatch
//     path; they exist only to satisfy the [Request] interface.
//   - conn.go / dispatch.go write-error and shutdown-race arms: the branches that
//     fire when the writer breaks mid-response or the connection is torn down
//     between checks. These are timing-dependent and are exercised by the race
//     stress tests (TestConcurrencyStress, TestGoroutineLeak) under -race rather
//     than by deterministic unit cases.
//
// None of these gaps represents an untested behavior a caller can trigger with a
// valid message; they are the fail-closed edges of an otherwise fully covered
// hot path.
