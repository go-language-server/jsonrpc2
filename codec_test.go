// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bytes"
	stdjson "encoding/json"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

// codecPayload is a representative struct exercised by the default-codec matrix.
type codecPayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Tags  []string
}

// TestDefaultCodec_RoundTrip exercises the default codec across the payload
// matrix (struct, map, slice, nil, string) and asserts that decoding the encoded
// bytes reproduces the original value.
func TestDefaultCodec_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value any
		into  func() any
		want  any
	}{
		"success: struct": {
			value: codecPayload{Name: "ada", Count: 3, Tags: []string{"a", "b"}},
			into:  func() any { return new(codecPayload) },
			want:  &codecPayload{Name: "ada", Count: 3, Tags: []string{"a", "b"}},
		},
		"success: map": {
			value: map[string]int{"x": 1, "y": 2},
			into:  func() any { return new(map[string]int) },
			want:  &map[string]int{"x": 1, "y": 2},
		},
		"success: int slice": {
			value: []int{1, 2, 3},
			into:  func() any { return new([]int) },
			want:  &[]int{1, 2, 3},
		},
		"success: nil": {
			value: nil,
			into:  func() any { return new(any) },
			want:  new(any),
		},
		"success: string": {
			value: "hello",
			into:  func() any { return new(string) },
			want:  func() any { s := "hello"; return &s }(),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := DefaultCodec.Marshal(tt.value)
			if err != nil {
				t.Fatalf("Marshal(%v) error: %v", tt.value, err)
			}

			dst := tt.into()
			if err := DefaultCodec.Unmarshal(data, dst); err != nil {
				t.Fatalf("Unmarshal(%q) error: %v", data, err)
			}
			if diff := gocmp.Diff(tt.want, dst); diff != "" {
				t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDefaultCodec_NilMarshalsToNull verifies the nil -> null convention.
func TestDefaultCodec_NilMarshalsToNull(t *testing.T) {
	t.Parallel()

	data, err := DefaultCodec.Marshal(nil)
	if err != nil {
		t.Fatalf("Marshal(nil) error: %v", err)
	}
	if string(data) != "null" {
		t.Fatalf("Marshal(nil) = %q, want %q", data, "null")
	}
}

// TestDefaultCodec_NoHTMLEscaping verifies that HTML-sensitive characters are
// emitted verbatim, matching a json.Encoder with SetEscapeHTML(false).
func TestDefaultCodec_NoHTMLEscaping(t *testing.T) {
	t.Parallel()

	data, err := DefaultCodec.Marshal("<a> & </b>")
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if want := `"<a> & </b>"`; string(data) != want {
		t.Fatalf("Marshal(HTML) = %q, want %q", data, want)
	}
}

// TestDefaultCodec_RawMessagePassthrough verifies that both this package's
// RawMessage and encoding/json.RawMessage are passed through verbatim on both
// encode and decode, and that decode copies rather than aliases.
func TestDefaultCodec_RawMessagePassthrough(t *testing.T) {
	t.Parallel()

	t.Run("jsonrpc2.RawMessage", func(t *testing.T) {
		t.Parallel()
		raw := RawMessage(`{"k":<unescaped>,"n":1}`)

		data, err := DefaultCodec.Marshal(raw)
		if err != nil {
			t.Fatalf("Marshal(RawMessage) error: %v", err)
		}
		if !bytes.Equal(data, raw) {
			t.Fatalf("Marshal(RawMessage) = %q, want verbatim %q", data, raw)
		}

		var out RawMessage
		if err := DefaultCodec.Unmarshal(data, &out); err != nil {
			t.Fatalf("Unmarshal(*RawMessage) error: %v", err)
		}
		if !bytes.Equal(out, raw) {
			t.Fatalf("Unmarshal(*RawMessage) = %q, want %q", out, raw)
		}
		if &out[0] == &raw[0] {
			t.Error("Unmarshal(*RawMessage) aliased the source; want a copy")
		}
	})

	t.Run("encoding/json.RawMessage", func(t *testing.T) {
		t.Parallel()
		raw := stdjson.RawMessage(`[1,2,3]`)

		data, err := DefaultCodec.Marshal(raw)
		if err != nil {
			t.Fatalf("Marshal(json.RawMessage) error: %v", err)
		}
		if !bytes.Equal(data, raw) {
			t.Fatalf("Marshal(json.RawMessage) = %q, want verbatim %q", data, raw)
		}

		var out stdjson.RawMessage
		if err := DefaultCodec.Unmarshal(data, &out); err != nil {
			t.Fatalf("Unmarshal(*json.RawMessage) error: %v", err)
		}
		if !bytes.Equal(out, raw) {
			t.Fatalf("Unmarshal(*json.RawMessage) = %q, want %q", out, raw)
		}
	})
}

// TestDefaultCodec_NilRawMessageMarshalsToNull verifies the nil-raw -> null rule.
func TestDefaultCodec_NilRawMessageMarshalsToNull(t *testing.T) {
	t.Parallel()

	data, err := DefaultCodec.Marshal(RawMessage(nil))
	if err != nil {
		t.Fatalf("Marshal(nil RawMessage) error: %v", err)
	}
	if string(data) != "null" {
		t.Fatalf("Marshal(nil RawMessage) = %q, want %q", data, "null")
	}
}

// TestMarshalParams verifies the envelope-facing helper: nil and null payloads
// are omitted (nil RawMessage), a RawMessage passes through, and a struct is
// encoded.
func TestMarshalParams(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value   any
		want    RawMessage
		wantNil bool
	}{
		"omit: nil": {
			value:   nil,
			wantNil: true,
		},
		"omit: explicit null raw": {
			value:   RawMessage("null"),
			wantNil: true,
		},
		"omit: empty raw": {
			value:   RawMessage{},
			wantNil: true,
		},
		"passthrough: raw object": {
			value: RawMessage(`{"a":1}`),
			want:  RawMessage(`{"a":1}`),
		},
		"encode: struct": {
			value: struct {
				A int `json:"a"`
			}{A: 1},
			want: RawMessage(`{"a":1}`),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := marshalParams(nil, tt.value)
			if err != nil {
				t.Fatalf("marshalParams error: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("marshalParams = %q, want nil (omitted)", got)
				}
				return
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("marshalParams = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUnmarshalResult verifies the response-facing helper: empty/null results
// leave the destination untouched, a RawMessage destination is honored even for
// null, and a populated result decodes.
func TestUnmarshalResult(t *testing.T) {
	t.Parallel()

	t.Run("empty result is a no-op", func(t *testing.T) {
		t.Parallel()
		got := 42
		if err := unmarshalResult(nil, nil, &got); err != nil {
			t.Fatalf("unmarshalResult error: %v", err)
		}
		if got != 42 {
			t.Fatalf("unmarshalResult mutated destination: got %d, want 42", got)
		}
	})

	t.Run("null result is a no-op for typed destination", func(t *testing.T) {
		t.Parallel()
		got := 42
		if err := unmarshalResult(nil, RawMessage("null"), &got); err != nil {
			t.Fatalf("unmarshalResult error: %v", err)
		}
		if got != 42 {
			t.Fatalf("unmarshalResult mutated destination: got %d, want 42", got)
		}
	})

	t.Run("null result is preserved for RawMessage destination", func(t *testing.T) {
		t.Parallel()
		var got RawMessage
		if err := unmarshalResult(nil, RawMessage("null"), &got); err != nil {
			t.Fatalf("unmarshalResult error: %v", err)
		}
		if string(got) != "null" {
			t.Fatalf("unmarshalResult = %q, want %q", got, "null")
		}
	})

	t.Run("populated result decodes", func(t *testing.T) {
		t.Parallel()
		var got struct {
			A int `json:"a"`
		}
		if err := unmarshalResult(nil, RawMessage(`{"a":7}`), &got); err != nil {
			t.Fatalf("unmarshalResult error: %v", err)
		}
		if got.A != 7 {
			t.Fatalf("unmarshalResult A = %d, want 7", got.A)
		}
	})

	t.Run("nil destination is a no-op", func(t *testing.T) {
		t.Parallel()
		if err := unmarshalResult(nil, RawMessage(`{"a":7}`), nil); err != nil {
			t.Fatalf("unmarshalResult(nil dst) error: %v", err)
		}
	})
}
