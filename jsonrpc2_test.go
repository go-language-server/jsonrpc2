// SPDX-FileCopyrightText: 2021 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2_test

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"testing"
	"time"

	"github.com/segmentio/encoding/json"

	"go.lsp.dev/pkg/stack/stacktest"

	"go.lsp.dev/jsonrpc2"
)

type invoker interface {
	Name() string
	Invoke(t *testing.T, ctx context.Context, h *handler)
}

type notify struct {
	params interface{}
	method string
}

func (n notify) Name() string { return n.method }

func (n notify) Invoke(t *testing.T, ctx context.Context, h *handler) {
	t.Helper()

	if err := h.conn.Notify(ctx, n.method, n.params); err != nil {
		t.Fatalf("%v:Notify failed: %v", n.method, err)
	}
}

type call struct {
	params interface{}
	expect interface{}
	method string
}

func (c call) Name() string { return c.method }

func (c call) Invoke(t *testing.T, ctx context.Context, h *handler) {
	t.Helper()

	results := newResults(t, c.expect)
	if err := h.conn.Request(ctx, c.method, c.params).Await(ctx, results); err != nil {
		t.Fatalf("%v:Call failed: %v", c.method, err)
	}

	verifyResults(t, c.method, results, c.expect)
}

type sequence struct {
	name  string
	tests []invoker
}

func (s sequence) Name() string { return s.name }
func (s sequence) Invoke(t *testing.T, ctx context.Context, h *handler) {
	t.Helper()

	for _, child := range s.tests {
		child.Invoke(t, ctx, h)
	}
}

type async struct {
	params interface{}
	name   string
	method string
}

func (a async) Name() string { return a.name }

func (a async) Invoke(t *testing.T, ctx context.Context, h *handler) {
	t.Helper()

	h.calls[a.name] = h.conn.Request(ctx, a.method, a.params)
}

type collect struct {
	expect interface{}
	name   string
	fails  bool
}

func (c collect) Name() string { return c.name }

func (c collect) Invoke(t *testing.T, ctx context.Context, h *handler) {
	t.Helper()

	o := h.calls[c.name]
	results := newResults(t, c.expect)
	err := o.Await(ctx, results)

	switch {
	case c.fails && err == nil:
		t.Fatalf("%v:Collect was supposed to fail", c.name)

	case !c.fails && err != nil:
		t.Fatalf("%v:Collect failed: %v", c.name, err)
	}

	if results != nil {
		verifyResults(t, c.name, results, c.expect)
	}
}

type cancel struct {
	name string
}

func (c cancel) Name() string { return c.name }
func (c cancel) Invoke(t *testing.T, ctx context.Context, h *handler) {
	t.Helper()

	o := h.calls[c.name]

	if err := h.conn.Notify(ctx, "cancel", &cancelParams{o.ID().Raw().(int64)}); err != nil {
		t.Fatalf("%v:Collect failed: %v", c.name, err)
	}
}

type echo call

func (e echo) Invoke(t *testing.T, ctx context.Context, h *handler) {
	t.Helper()

	results := newResults(t, e.expect)
	if err := h.conn.Request(ctx, "echo", []interface{}{e.method, e.params}).Await(ctx, results); err != nil {
		t.Fatalf("%v:Echo failed: %v", e.method, err)
	}

	verifyResults(t, e.method, results, e.expect)
}

type binder struct {
	framer  jsonrpc2.Framer
	runTest func(*handler)
}

func (b binder) Bind(ctx context.Context, conn *jsonrpc2.Connection) (jsonrpc2.Conn, error) {
	h := &handler{
		conn:    conn,
		waiters: make(chan map[string]chan struct{}, 1),
		calls:   make(map[string]*jsonrpc2.AsyncRequest),
	}

	h.waiters <- make(map[string]chan struct{})
	if b.runTest != nil {
		go b.runTest(h)
	}

	return jsonrpc2.Conn{
		Framer:    b.framer,
		Preempter: h,
		Handler:   h,
	}, nil
}

type cancelParams struct {
	ID int64
}

type handler struct {
	conn        *jsonrpc2.Connection
	waiters     chan map[string]chan struct{}
	calls       map[string]*jsonrpc2.AsyncRequest
	accumulator int
}

func (h *handler) waiter(name string) chan struct{} {
	waiters := <-h.waiters
	defer func() { h.waiters <- waiters }()

	waiter, found := waiters[name]
	if !found {
		waiter = make(chan struct{})
		waiters[name] = waiter
	}

	return waiter
}

func (h *handler) Preempt(ctx context.Context, req *jsonrpc2.Request) (interface{}, error) {
	switch req.Method {
	case "unblock":
		var name string
		if err := json.Unmarshal(req.Params, &name); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}
		close(h.waiter(name))

		return nil, nil

	case "peek":
		if len(req.Params) > 0 {
			return nil, fmt.Errorf("expected no params: %w", jsonrpc2.ErrInvalidParams)
		}

		return h.accumulator, nil

	case "cancel":
		var params cancelParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}
		h.conn.Cancel(jsonrpc2.Int64ID(params.ID))

		return nil, nil

	default:
		return nil, jsonrpc2.ErrNotHandled
	}
}

func (h *handler) Handle(ctx context.Context, req *jsonrpc2.Request) (interface{}, error) {
	switch req.Method {
	case "no_args":
		if len(req.Params) > 0 {
			return nil, fmt.Errorf("expected no params: %w", jsonrpc2.ErrInvalidParams)
		}

		return true, nil

	case "one_string":
		var v string
		if err := json.Unmarshal(req.Params, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}

		return "got:" + v, nil

	case "one_number":
		var v int
		if err := json.Unmarshal(req.Params, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}

		return fmt.Sprintf("got:%d", v), nil

	case "set":
		var v int
		if err := json.Unmarshal(req.Params, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}
		h.accumulator = v

		return nil, nil

	case "add":
		var v int
		if err := json.Unmarshal(req.Params, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}
		h.accumulator += v

		return nil, nil

	case "get":
		if len(req.Params) > 0 {
			return nil, fmt.Errorf("%w: expected no params", jsonrpc2.ErrInvalidParams)
		}

		return h.accumulator, nil

	case "join":
		var v []string
		if err := json.Unmarshal(req.Params, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}

		return path.Join(v...), nil

	case "echo":
		var v []interface{}
		if err := json.Unmarshal(req.Params, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}
		var result interface{}
		err := h.conn.Request(ctx, v[0].(string), v[1]).Await(ctx, &result)

		return result, err

	case "wait":
		var name string
		if err := json.Unmarshal(req.Params, &name); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}
		select {
		case <-h.waiter(name):
			return true, nil

		case <-ctx.Done():
			return nil, ctx.Err()

		case <-time.After(time.Second):
			return nil, fmt.Errorf("wait for %q timed out", name)
		}

	case "fork":
		var name string
		if err := json.Unmarshal(req.Params, &name); err != nil {
			return nil, fmt.Errorf("%s: %w", jsonrpc2.ErrParse, err)
		}

		waitFor := h.waiter(name)
		go func() {
			select {
			case <-waitFor:
				h.conn.Response(req.ID, true, nil)

			case <-ctx.Done():
				h.conn.Response(req.ID, nil, ctx.Err())

			case <-time.After(time.Second):
				h.conn.Response(req.ID, nil, fmt.Errorf("wait for %q timed out", name))
			}
		}()

		return nil, jsonrpc2.ErrAsyncResponse

	default:
		return nil, jsonrpc2.ErrNotHandled
	}
}

var callTests = []invoker{
	call{
		method: "no_args",
		params: nil,
		expect: true,
	},
	call{
		method: "one_string",
		params: "fish",
		expect: "got:fish",
	},
	call{
		method: "one_number",
		params: 10,
		expect: "got:10",
	},
	call{
		method: "join",
		params: []string{"a", "b", "c"},
		expect: "a/b/c",
	},
	sequence{
		name: "notify",
		tests: []invoker{
			notify{
				method: "set",
				params: 3,
			},
			notify{
				method: "add",
				params: 5,
			},
			call{
				method: "get",
				params: nil,
				expect: 8,
			},
		},
	},
	sequence{
		name: "preempt",
		tests: []invoker{
			async{
				name:   "a",
				method: "wait",
				params: "a",
			},
			notify{
				method: "unblock",
				params: "a",
			},
			collect{
				name:   "a",
				expect: true,
				fails:  false,
			},
		},
	},
	sequence{
		name: "basic cancel",
		tests: []invoker{
			async{
				name:   "b",
				method: "wait",
				params: "b",
			},
			cancel{
				name: "b",
			},
			collect{
				name:   "b",
				expect: nil,
				fails:  true,
			},
		},
	},
	sequence{
		name: "queue",
		tests: []invoker{
			async{
				name:   "a",
				method: "wait",
				params: "a",
			},
			notify{
				method: "set",
				params: 1,
			},
			notify{
				method: "add",
				params: 2,
			},
			notify{
				method: "add",
				params: 3,
			},
			notify{
				method: "add",
				params: 4,
			},
			// accumulator will not have any adds yet
			call{
				method: "peek",
				params: nil,
				expect: 0,
			},
			notify{
				method: "unblock",
				params: "a",
			},
			collect{
				name:   "a",
				expect: true,
				fails:  false,
			},
			// accumulator now has all the adds
			call{
				method: "get",
				params: nil,
				expect: 10,
			},
		},
	},
	sequence{
		name: "fork",
		tests: []invoker{
			async{
				name:   "a",
				method: "fork",
				params: "a",
			},
			notify{
				method: "set",
				params: 1,
			},
			notify{
				method: "add",
				params: 2,
			},
			notify{
				method: "add",
				params: 3,
			},
			notify{
				method: "add",
				params: 4,
			},
			// fork will not have blocked the adds
			call{
				method: "get",
				params: nil,
				expect: 10,
			},
			notify{
				method: "unblock",
				params: "a",
			},
			collect{
				name:   "a",
				expect: true,
				fails:  false,
			},
		},
	},
}

func TestConnectionRaw(t *testing.T) {
	t.Parallel()

	testConnection(t, jsonrpc2.RawFramer())
}

func TestConnectionHeader(t *testing.T) {
	t.Parallel()

	testConnection(t, jsonrpc2.HeaderFramer())
}

func testConnection(t *testing.T, framer jsonrpc2.Framer) {
	t.Helper()

	stacktest.NoLeak(t)
	ctx := context.Background()
	listener, err := jsonrpc2.NetPipe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	server, err := jsonrpc2.Serve(ctx, listener, binder{framer, nil})
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		listener.Close()
		server.Wait()
	}()

	callTests := callTests
	for _, tt := range callTests {
		t.Run(tt.Name(), func(t *testing.T) {
			client, err := jsonrpc2.Dial(ctx,
				listener.Dialer(), binder{framer, func(h *handler) {
					defer h.conn.Close()
					ctx := ctx
					tt.Invoke(t, ctx, h)
					if call, ok := tt.(*call); ok {
						// also run all simple call tests in echo mode
						(*echo)(call).Invoke(t, ctx, h)
					}
				}})
			if err != nil {
				t.Fatal(err)
			}
			client.Wait()
		})
	}
}

// newResults makes a new empty copy of the expected type to put the results into.
func newResults(_ testing.TB, expect interface{}) interface{} {
	switch e := expect.(type) {
	case nil:
		return nil

	case []interface{}:
		var r []interface{}
		for _, v := range e {
			r = append(r, reflect.New(reflect.TypeOf(v)).Interface())
		}

		return r

	default:
		return reflect.New(reflect.TypeOf(expect)).Interface()
	}
}

// verifyResults compares the results to the expected values.
func verifyResults(tb testing.TB, method string, results, expect interface{}) {
	tb.Helper()

	if expect == nil {
		if results != nil {
			tb.Fatalf("%v:Got results %+v where none expeted", method, expect)
		}
	}

	val := reflect.Indirect(reflect.ValueOf(results)).Interface()
	if !reflect.DeepEqual(val, expect) {
		tb.Fatalf("%v:Results are incorrect, got %+v expect %+v", method, val, expect)
	}
}
