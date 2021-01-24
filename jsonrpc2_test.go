// Copyright 2019 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2_test

// type callTest struct {
// 	method string
// 	params interface{}
// 	expect interface{}
// }
//
// var callTests = []callTest{
// 	{"no_args", nil, true},
// 	{"one_string", "fish", "got:fish"},
// 	{"one_number", 10, "got:10"},
// 	{"join", []string{"a", "b", "c"}, "a/b/c"},
// 	//TODO: expand the test cases
// }
//
// func (test *callTest) newResults() interface{} {
// 	switch e := test.expect.(type) {
// 	case []interface{}:
// 		var r []interface{}
// 		for _, v := range e {
// 			r = append(r, reflect.New(reflect.TypeOf(v)).Interface())
// 		}
// 		return r
// 	case nil:
// 		return nil
// 	default:
// 		return reflect.New(reflect.TypeOf(test.expect)).Interface()
// 	}
// }
//
// func (test *callTest) verifyResults(t *testing.T, results interface{}) {
// 	if results == nil {
// 		return
// 	}
// 	val := reflect.Indirect(reflect.ValueOf(results)).Interface()
// 	if !reflect.DeepEqual(val, test.expect) {
// 		t.Errorf("%v:Results are incorrect, got %+v expect %+v", test.method, val, test.expect)
// 	}
// }
//
// func TestRequest(t *testing.T) {
// 	ctx := context.Background()
// 	for _, headers := range []bool{false, true} {
// 		name := "Plain"
// 		if headers {
// 			name = "Headers"
// 		}
// 		t.Run(name, func(t *testing.T) {
// 			a, b, done := prepare(ctx, t, headers)
// 			defer done()
// 			for _, test := range callTests {
// 				t.Run(test.method, func(t *testing.T) {
// 					results := test.newResults()
// 					if _, err := a.Call(ctx, test.method, test.params, results); err != nil {
// 						t.Fatalf("%v:Call failed: %v", test.method, err)
// 					}
// 					test.verifyResults(t, results)
// 					if _, err := b.Call(ctx, test.method, test.params, results); err != nil {
// 						t.Fatalf("%v:Call failed: %v", test.method, err)
// 					}
// 					test.verifyResults(t, results)
// 				})
// 			}
// 		})
// 	}
// }
//
// func prepare(ctx context.Context, t *testing.T, withHeaders bool) (jsonrpc2.Conn, jsonrpc2.Conn, func()) {
// 	// make a wait group that can be used to wait for the system to shut down
// 	aPipe, bPipe := net.Pipe()
// 	a := run(ctx, withHeaders, aPipe)
// 	b := run(ctx, withHeaders, bPipe)
// 	return a, b, func() {
// 		a.Close()
// 		b.Close()
// 		<-a.Done()
// 		<-b.Done()
// 	}
// }
//
// func run(ctx context.Context, withHeaders bool, nc net.Conn) jsonrpc2.Conn {
// 	var stream jsonrpc2.Stream
// 	if withHeaders {
// 		stream = jsonrpc2.NewHeaderStream(nc)
// 	} else {
// 		stream = jsonrpc2.NewRawStream(nc)
// 	}
// 	conn := jsonrpc2.NewConn(stream)
// 	conn.Go(ctx, testHandler())
// 	return conn
// }
