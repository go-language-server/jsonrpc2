// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path"
	"reflect"
	"testing"

	"go.uber.org/zap"

	"github.com/go-language-server/jsonrpc2"
)

var logRPC = flag.Bool("logrpc", false, "Enable jsonrpc2 communication logging")

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
	if results == nil {
		return
	}
	val := reflect.Indirect(reflect.ValueOf(results)).Interface()
	if !reflect.DeepEqual(val, test.expect) {
		t.Errorf("%v:Results are incorrect, got %+v expect %+v", test.method, val, test.expect)
	}
}

func prepare(ctx context.Context, t *testing.T) (*jsonrpc2.Conn, *jsonrpc2.Conn) {
	aR, bW := io.Pipe()
	bR, aW := io.Pipe()
	a := run(ctx, t, aR, aW)
	b := run(ctx, t, bR, bW)
	return a, b
}

func run(ctx context.Context, t *testing.T, r io.ReadCloser, w io.WriteCloser) *jsonrpc2.Conn {
	stream := jsonrpc2.NewStream(r, w)
	conn := jsonrpc2.NewConn(stream)
	conn.Handler = handle

	if *logRPC {
		conn.Logger, _ = zap.NewDevelopment()
	}

	go func() {
		defer func() {
			r.Close()
			w.Close()
		}()

		if err := conn.Run(ctx); err != nil {
			t.Errorf("Stream failed: %v", err)
		}
	}()

	if t.Failed() {
		t.FailNow()
	}

	return conn
}

func handle(ctx context.Context, r *jsonrpc2.Request) {
	switch r.Method {
	case "no_args":
		if r.Params != nil {
			r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.InvalidParams, "Expected no params"))
			return
		}
		r.Reply(ctx, true, nil)
	case "one_string":
		var v string
		if err := json.Unmarshal(*r.Params, &v); err != nil {
			r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.ParseError, "%v", err.Error()))
			return
		}
		r.Reply(ctx, "got:"+v, nil)
	case "one_number":
		var v int
		if err := json.Unmarshal(*r.Params, &v); err != nil {
			r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.ParseError, "%v", err.Error()))
			return
		}
		r.Reply(ctx, fmt.Sprintf("got:%d", v), nil)
	case "join":
		var v []string
		if err := json.Unmarshal(*r.Params, &v); err != nil {
			r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.ParseError, "%v", err.Error()))
			return
		}
		r.Reply(ctx, path.Join(v...), nil)
	default:
		r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.MethodNotFound, "method %q not found", r.Method))
	}
}

func TestCall(t *testing.T) {
	ctx := context.Background()
	a, b := prepare(ctx, t)
	for _, test := range callTests {
		results := test.newResults()
		if err := a.Call(ctx, test.method, test.params, results); err != nil {
			t.Fatalf("%v:Call failed: %v", test.method, err)
		}
		test.verifyResults(t, results)
		if err := b.Call(ctx, test.method, test.params, results); err != nil {
			t.Fatalf("%v:Call failed: %v", test.method, err)
		}
		test.verifyResults(t, results)
	}
}
