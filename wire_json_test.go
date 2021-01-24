// Copyright 2020 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

// +build !gojay

package jsonrpc2_test

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	json "github.com/goccy/go-json"

	"go.lsp.dev/jsonrpc2"
)

var wireIDTestData = []struct {
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
	t.Parallel()

	for _, test := range wireIDTestData {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := fmt.Sprint(test.id); got != test.plain {
				t.Errorf("got %s expected %s", got, test.plain)
			}
			if got := fmt.Sprintf("%q", test.id); got != test.quoted {
				t.Errorf("got %s want %s", got, test.quoted)
			}
		})
	}
}

func TestIDEncode(t *testing.T) {
	t.Parallel()

	for _, test := range wireIDTestData {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(&test.id)
			if err != nil {
				t.Fatal(err)
			}
			checkJSON(t, data, test.encoded)
		})
	}
}

func TestIDDecode(t *testing.T) {
	t.Parallel()

	for _, test := range wireIDTestData {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var got jsonrpc2.ID
			if err := json.Unmarshal(test.encoded, &got); err != nil {
				t.Fatal(err)
			}

			if reflect.ValueOf(&got).IsZero() {
				t.Fatalf("got nil want %s", test.id)
			}

			if got != test.id {
				t.Fatalf("got %s want %s", got, test.id)
			}
		})
	}
}

func TestErrorEncode(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(jsonrpc2.NewError(0, ""))
	if err != nil {
		t.Fatal(err)
	}

	checkJSON(t, b, []byte(`{
		"code": 0,
		"message": ""
	}`))
}

func TestErrorResponse(t *testing.T) {
	t.Parallel()

	// originally reported in #39719, this checks that result is not present if
	// it is an error response
	r, _ := jsonrpc2.NewResponse(jsonrpc2.NewNumberID(3), nil, fmt.Errorf("computing fix edits"))
	data, err := json.Marshal(r)
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
	t.Helper()

	// compare the compact form, to allow for formatting differences
	g := &bytes.Buffer{}
	if err := json.Compact(g, got); err != nil {
		t.Fatal(err)
	}

	w := &bytes.Buffer{}
	if err := json.Compact(w, want); err != nil {
		t.Fatal(err)
	}

	if g.String() != w.String() {
		t.Fatalf("Got:\n%s\nWant:\n%s", g, w)
	}
}
