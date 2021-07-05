// SPDX-FileCopyrightText: 2021 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2_test

import (
	"bytes"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/segmentio/encoding/json"

	"go.lsp.dev/jsonrpc2"
)

func TestMessage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		want    jsonrpc2.Message
		encoded []byte
	}{
		"notification": {
			want:    newNotification(t, "alive", nil),
			encoded: []byte(`{"jsonrpc":"2.0","method":"alive"}`),
		},
		"call": {
			want:    newCall(t, "msg1", "ping", nil),
			encoded: []byte(`{"jsonrpc":"2.0","id":"msg1","method":"ping"}`),
		},
		"response": {
			want:    newResponse(t, "msg2", "pong", nil),
			encoded: []byte(`{"jsonrpc":"2.0","id":"msg2","result":"pong"}`),
		},
		"numerical id": {
			want:    newCall(t, 1, "poke", nil),
			encoded: []byte(`{"jsonrpc":"2.0","id":1,"method":"poke"}`),
		},
		"computing fix edits": {
			// originally reported in #39719, this checks that result is not present if
			// it is an error response
			want: newResponse(t, 3, nil, jsonrpc2.NewError(0, "computing fix edits")),
			encoded: []byte(`{
		"jsonrpc":"2.0",
		"id":3,
		"error":{
			"code":0,
			"message":"computing fix edits"
		}
	}`),
		},
	}
	for name, tt := range tests {
		tt := tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			buf, err := jsonrpc2.EncodeMessage(tt.want)
			if err != nil {
				t.Fatal(err)
			}

			// compare the compact form, to allow for formatting differences
			gotBuf := &bytes.Buffer{}
			if err := json.Compact(gotBuf, buf); err != nil {
				t.Fatal(err)
			}
			wantBuf := &bytes.Buffer{}
			if err := json.Compact(wantBuf, tt.encoded); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(wantBuf.String(), gotBuf.String()); diff != "" {
				t.Fatalf("encoded message does not match (-want +got):\n%s", diff)
			}

			got, err := jsonrpc2.DecodeMessage(tt.encoded)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreUnexported(jsonrpc2.ID{})); diff != "" {
				t.Fatalf("decoded message does not match (-want +got):\n%s", diff)
			}
		})
	}
}

func newID(tb testing.TB, id interface{}) jsonrpc2.ID {
	tb.Helper()

	switch v := id.(type) {
	case nil:
		return jsonrpc2.ID{}

	case string:
		return jsonrpc2.StringID(v)

	case int:
		return jsonrpc2.Int64ID(int64(v))

	case int64:
		return jsonrpc2.Int64ID(v)

	default:
		tb.Fatal("invalid ID type")
	}

	return jsonrpc2.ID{} // unreachable
}

func newNotification(tb testing.TB, method string, params interface{}) jsonrpc2.Message {
	tb.Helper()

	msg, err := jsonrpc2.NewNotification(method, params)
	if err != nil {
		tb.Fatal(err)
	}

	return msg
}

func newCall(tb testing.TB, id interface{}, method string, params interface{}) jsonrpc2.Message {
	tb.Helper()

	msg, err := jsonrpc2.NewRequest(newID(tb, id), method, params)
	if err != nil {
		tb.Fatal(err)
	}

	return msg
}

func newResponse(tb testing.TB, id, result interface{}, rerr error) jsonrpc2.Message {
	tb.Helper()

	msg, err := jsonrpc2.NewResponse(newID(tb, id), result, rerr)
	if err != nil {
		tb.Fatal(err)
	}

	return msg
}
