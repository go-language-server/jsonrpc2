// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright 2021 The Go Language Server Authors

// +build !gojay

package jsonrpc2_test

import (
	"context"
	"fmt"
	"net"
	"path"
	"reflect"
	"testing"

	json "github.com/goccy/go-json"

	"go.lsp.dev/jsonrpc2"
)

type callTest struct {
	method string
	params interface{}
	expect interface{}
}

var callTests = []callTest{
	{"no_args", nil, true},
	{"one_string", "fish", "got:fish"},
	{"one_number", 10, "got:10"},
	{"join", []string{"a", "b", "c"}, "a/b/c"},
	// TODO: expand the test cases
}

func (test *callTest) newResults() interface{} {
	switch e := test.expect.(type) {
	case []interface{}:
		var r []interface{}
		for _, v := range e {
			r = append(r, reflect.New(reflect.TypeOf(v)).Interface())
		}
		return r
	case nil:
		return nil
	default:
		return reflect.New(reflect.TypeOf(test.expect)).Interface()
	}
}

func (test *callTest) verifyResults(t *testing.T, results interface{}) {
	t.Helper()

	if results == nil {
		return
	}
	val := reflect.Indirect(reflect.ValueOf(results)).Interface()
	if !reflect.DeepEqual(val, test.expect) {
		t.Errorf("%v:Results are incorrect, got %+v expect %+v", test.method, val, test.expect)
	}
}

func TestRequest(t *testing.T) {
	ctx := context.Background()
	a, b, done := prepare(ctx, t)
	defer done()

	for _, test := range callTests {
		t.Run(test.method, func(t *testing.T) {
			results := test.newResults()
			if _, err := a.Call(ctx, test.method, test.params, results); err != nil {
				t.Fatalf("%v:Call failed: %v", test.method, err)
			}
			test.verifyResults(t, results)
			if _, err := b.Call(ctx, test.method, test.params, results); err != nil {
				t.Fatalf("%v:Call failed: %v", test.method, err)
			}
			test.verifyResults(t, results)
		})
	}
}

func prepare(ctx context.Context, t *testing.T) (conn, conn2 jsonrpc2.Conn, done func()) {
	t.Helper()

	// make a wait group that can be used to wait for the system to shut down
	aPipe, bPipe := net.Pipe()
	a := run(ctx, aPipe)
	b := run(ctx, bPipe)
	return a, b, func() {
		a.Close()
		b.Close()
		<-a.Done()
		<-b.Done()
	}
}

func run(ctx context.Context, nc net.Conn) jsonrpc2.Conn {
	stream := jsonrpc2.NewStream(nc)
	conn := jsonrpc2.NewConn(stream)
	conn.Go(ctx, testHandler())
	return conn
}

func testHandler() jsonrpc2.Handler {
	return func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		switch req.Method() {
		case "no_args":
			if len(req.Params()) > 0 {
				return reply(ctx, nil, fmt.Errorf("%w: expected no params", jsonrpc2.ErrInvalidParams))
			}
			return reply(ctx, true, nil)
		case "one_string":
			var v string
			if err := json.UnmarshalNoEscape(req.Params(), &v); err != nil {
				return reply(ctx, nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err))
			}
			return reply(ctx, "got:"+v, nil)
		case "one_number":
			var v int
			if err := json.UnmarshalNoEscape(req.Params(), &v); err != nil {
				return reply(ctx, nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err))
			}
			return reply(ctx, fmt.Sprintf("got:%d", v), nil)
		case "join":
			var v []string
			if err := json.UnmarshalNoEscape(req.Params(), &v); err != nil {
				return reply(ctx, nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err))
			}
			return reply(ctx, path.Join(v...), nil)
		default:
			return jsonrpc2.MethodNotFoundHandler(ctx, reply, req)
		}
	}
}