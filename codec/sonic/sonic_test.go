// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package sonic_test

import (
	"bytes"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/jsonrpc2/codec/sonic"
)

// payload is a representative struct used across the codec parity matrix.
type payload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Tags  []string
}

// codec satisfies the parent jsonrpc2.Codec interface.
var codec jsonrpc2.Codec = sonic.Codec{}

func TestCodec_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value any
		into  func() any
	}{
		"success: struct": {
			value: payload{Name: "ada", Count: 3, Tags: []string{"a", "b"}},
			into:  func() any { return new(payload) },
		},
		"success: map": {
			value: map[string]int{"x": 1, "y": 2},
			into:  func() any { return new(map[string]int) },
		},
		"success: int slice": {
			value: []int{1, 2, 3},
			into:  func() any { return new([]int) },
		},
		"success: nil": {
			value: nil,
			into:  func() any { return new(any) },
		},
		"success: string": {
			value: "hello",
			into:  func() any { return new(string) },
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := codec.Marshal(tt.value)
			if err != nil {
				t.Fatalf("Marshal(%v) error: %v", tt.value, err)
			}

			// Re-encode via the default codec for a value-level comparison that is
			// independent of incidental key ordering or whitespace.
			ref, err := jsonrpc2.DefaultCodec.Marshal(tt.value)
			if err != nil {
				t.Fatalf("reference Marshal(%v) error: %v", tt.value, err)
			}

			dstSonic := tt.into()
			if err := codec.Unmarshal(data, dstSonic); err != nil {
				t.Fatalf("Unmarshal(%q) error: %v", data, err)
			}
			dstRef := tt.into()
			if err := jsonrpc2.DefaultCodec.Unmarshal(ref, dstRef); err != nil {
				t.Fatalf("reference Unmarshal(%q) error: %v", ref, err)
			}

			if diff := gocmp.Diff(dstRef, dstSonic); diff != "" {
				t.Errorf("round-trip mismatch vs default codec (-default +sonic):\n%s", diff)
			}
		})
	}
}

func TestCodec_RawMessagePassthrough(t *testing.T) {
	t.Parallel()

	raw := jsonrpc2.RawMessage(`{"k":<unescaped>,"n":1}`)

	data, err := codec.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal(RawMessage) error: %v", err)
	}
	if !bytes.Equal(data, raw) {
		t.Fatalf("Marshal(RawMessage) = %q, want verbatim %q", data, raw)
	}

	var out jsonrpc2.RawMessage
	if err := codec.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal(*RawMessage) error: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatalf("Unmarshal(*RawMessage) = %q, want verbatim %q", out, raw)
	}
	if &out[0] == &raw[0] {
		t.Error("Unmarshal(*RawMessage) aliased the source bytes; want a copy")
	}
}

func TestCodec_NilRawMessageMarshalsToNull(t *testing.T) {
	t.Parallel()

	data, err := codec.Marshal(jsonrpc2.RawMessage(nil))
	if err != nil {
		t.Fatalf("Marshal(nil RawMessage) error: %v", err)
	}
	if string(data) != "null" {
		t.Fatalf("Marshal(nil RawMessage) = %q, want %q", data, "null")
	}
}

func TestCodec_NoHTMLEscaping(t *testing.T) {
	t.Parallel()

	data, err := codec.Marshal("<a> & </b>")
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if want := `"<a> & </b>"`; string(data) != want {
		t.Fatalf("Marshal(HTML) = %q, want %q", data, want)
	}
}
