// Copyright 2020 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

// +build gojay

package jsonrpc2_test

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/francoispqt/gojay"
	"github.com/google/go-cmp/cmp"

	"go.lsp.dev/jsonrpc2"
)

var idTestData = []struct {
	name    string
	id      jsonrpc2.ID
	encoded []byte
	plain   string
	quoted  string
}{
	{
		name:    `empty`,
		encoded: []byte(`0`),
		plain:   `0`,
		quoted:  `#0`,
	}, {
		name:    `number`,
		id:      jsonrpc2.NewNumberID(43),
		encoded: []byte(`43`),
		plain:   `43`,
		quoted:  `#43`,
	}, {
		name:    `string`,
		id:      jsonrpc2.NewStringID("life"),
		encoded: []byte(`"life"`),
		plain:   `life`,
		quoted:  `"life"`,
	},
}

func TestIDFormat(t *testing.T) {
	for _, test := range idTestData {
		t.Run(test.name, func(t *testing.T) {
			got := fmt.Sprint(test.id)
			if diff := cmp.Diff(test.plain, got); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}

			got2 := fmt.Sprintf("%q", test.id)
			if diff := cmp.Diff(test.quoted, got2); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestIDEncode(t *testing.T) {
	for _, test := range idTestData {
		t.Run(test.name, func(t *testing.T) {
			data, err := gojay.MarshalJSONObject(&test.id)
			if err != nil {
				t.Fatal(err)
			}

			checkJSON(t, data, test.encoded)
		})
	}
}

func TestIDDecode(t *testing.T) {
	for _, test := range idTestData {
		t.Run(test.name, func(t *testing.T) {
			var got *jsonrpc2.ID
			if err := gojay.UnmarshalJSONObject(test.encoded, got); err != nil {
				t.Fatal(err)
			}

			if reflect.ValueOf(&got).IsZero() {
				t.Errorf("got nil want %s", test.id)
			}

			if *got != test.id {
				t.Errorf("got %s want %s", got, test.id)
			}
		})
	}
}

func TestErrorEncode(t *testing.T) {
	b, err := gojay.MarshalJSONObject(jsonrpc2.NewError(0, ""))
	if err != nil {
		t.Fatal(err)
	}

	checkJSON(t, b, []byte(`{
		"code": 0,
		"message": ""
	}`))
}

func TestErrorResponse(t *testing.T) {
	// originally reported in #39719, this checks that result is not present if
	// it is an error response
	r, _ := jsonrpc2.NewResponse(jsonrpc2.NewNumberID(3), nil, fmt.Errorf("computing fix edits"))
	data, err := gojay.MarshalAny(r)
	if err != nil {
		t.Fatal(err)
	}

	checkJSON(t, data, []byte(`{
		"jsonrpc":"2.0",
		"error":{
			"code":0,
			"message":"computing fix edits"
		},
		"id":3
	}`))
}

func checkJSON(t *testing.T, got, want []byte) {
	// compare the compact form, to allow for formatting differences
	g := &bytes.Buffer{}
	dec := gojay.NewDecoder(g)
	gotRaw := gojay.EmbeddedJSON(got)
	if err := dec.Decode(&gotRaw); err != nil {
		t.Fatal(err)
	}
	w := &bytes.Buffer{}
	dec2 := gojay.NewDecoder(w)
	wantRaw := gojay.EmbeddedJSON(want)
	if err := dec2.Decode(&wantRaw); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(w.String(), g.String()); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}
}
