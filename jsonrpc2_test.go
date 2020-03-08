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
	"time"

	"go.uber.org/zap"

	"go.lsp.dev/jsonrpc2"
)

var logRPC = flag.Bool("logrpc", false, "Enable jsonrpc2 communication logging")

type callTest struct {
	method string
	params interface{}
	expect interface{}
}

var callTests = []callTest{
	{
		method: "no_args",
		params: nil,
		expect: true,
	},
	{
		method: "one_string",
		params: "fish",
		expect: "got:fish",
	},
	{
		method: "one_number",
		params: 10,
		expect: "got:10",
	},
	{
		method: "join",
		params: []string{"a", "b", "c"},
		expect: "a/b/c",
	},
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

func prepare(ctx context.Context, t *testing.T) (a, b *jsonrpc2.Conn) {
	aR, bW := io.Pipe()
	bR, aW := io.Pipe()
	a = run(ctx, t, aR, aW)
	b = run(ctx, t, bR, bW)
	return a, b
}

func run(ctx context.Context, t *testing.T, r io.ReadCloser, w io.WriteCloser) *jsonrpc2.Conn {
	stream := jsonrpc2.NewStream(r, w)

	h := handle{
		log: *logRPC,
	}
	if *logRPC {
		h.logger, _ = zap.NewDevelopment()
	}
	conn := jsonrpc2.NewConn(stream, jsonrpc2.WithHandlers(h))

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

type handle struct {
	log    bool
	logger *zap.Logger
}

func (h handle) Deliver(ctx context.Context, r *jsonrpc2.Request, delivered bool) bool {
	switch r.Method {
	case "no_args":
		if r.Params != nil {
			r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.InvalidParams, "Expected no params"))
			return true
		}
		r.Reply(ctx, true, nil)

	case "one_string":
		var v string
		if err := json.Unmarshal(*r.Params, &v); err != nil {
			r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.ParseError, "%v", err.Error()))
			return true
		}
		r.Reply(ctx, "got:"+v, nil)

	case "one_number":
		var v int
		if err := json.Unmarshal(*r.Params, &v); err != nil {
			r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.ParseError, "%v", err.Error()))
			return true
		}
		r.Reply(ctx, fmt.Sprintf("got:%d", v), nil)

	case "join":
		var v []string
		if err := json.Unmarshal(*r.Params, &v); err != nil {
			r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.ParseError, "%v", err.Error()))
			return true
		}
		r.Reply(ctx, path.Join(v...), nil)

	default:
		r.Reply(ctx, nil, jsonrpc2.Errorf(jsonrpc2.MethodNotFound, "method %q not found", r.Method))
	}

	return true
}

func (handle) Cancel(context.Context, *jsonrpc2.Conn, jsonrpc2.ID, bool) bool {
	return false
}

type (
	ctxMethodKey struct{}
	ctxTimeKey   struct{}
)

func (h handle) Request(ctx context.Context, conn *jsonrpc2.Conn, direction jsonrpc2.Direction, r *jsonrpc2.WireRequest) context.Context {
	if h.log {
		if r.ID != nil {
			h.logger.Info("Request", zap.String("call", fmt.Sprintf("%v call [%v] %s %v", direction, r.ID, r.Method, r.Params)))
		} else {
			h.logger.Info("Request", zap.String("notification", fmt.Sprintf("%v notification %s %v", direction, r.Method, r.Params)))
		}
		ctx = context.WithValue(ctx, ctxMethodKey{}, r.Method)
		ctx = context.WithValue(ctx, ctxTimeKey{}, time.Now())
	}
	return ctx
}

func (h handle) Response(ctx context.Context, conn *jsonrpc2.Conn, direction jsonrpc2.Direction, r *jsonrpc2.WireResponse) context.Context {
	if h.log {
		method := ctx.Value(ctxMethodKey{})
		elapsed := time.Since(ctx.Value(ctxTimeKey{}).(time.Time))
		h.logger.Info("Response", zap.String(method.(string), fmt.Sprintf("%v response in %v [%v] %s %v", direction, elapsed, r.ID, method, r.Result)))
	}
	return ctx
}

func (handle) Done(context.Context, error) {}

func (handle) Read(ctx context.Context, _ int64) context.Context { return ctx }

func (handle) Write(ctx context.Context, _ int64) context.Context { return ctx }

func (h handle) Error(_ context.Context, err error) {
	h.logger.Error("Error", zap.Error(err))
}
