// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"context"
	"strconv"
	"testing"

	jrpc2 "github.com/creachadair/jrpc2"
	"go.lsp.dev/jsonrpc2"
	mcp "go.lsp.dev/jsonrpc2/internal/benchmark/mcpref"
)

// adapterFactory builds a fresh rpcClient for one benchmark sub-run. Each factory
// is labeled by library and transport family so the bench name records which is
// which (per §6 the transport must be visible in the bench name).
type adapterFactory struct {
	name string
	make func(ctx context.Context) (rpcClient, error)
}

// nativeAdapters returns the fastest-per-library native transports: jsonrpc2 and mcp
// over net.Pipe with NDJSON framing, jrpc2 over channel.Direct (server.NewLocal).
func nativeAdapters() []adapterFactory {
	return []adapterFactory{
		{"jsonrpc2/native", func(ctx context.Context) (rpcClient, error) { return newOursAdapter(ctx) }},
		{"jrpc2/native", func(ctx context.Context) (rpcClient, error) { return newJRPC2NativeAdapter(ctx) }},
		{"mcp/native", func(ctx context.Context) (rpcClient, error) { return newMCPAdapter(ctx) }},
	}
}

// commonAdapters returns all three libraries over the SAME net.Pipe transport
// family (NDJSON-style framing): jsonrpc2 via NewNDJSONStream, jrpc2 via
// channel.RawJSON, mcp via the ndjson Reader/Writer in adapters.go.
func commonAdapters() []adapterFactory {
	return []adapterFactory{
		{"jsonrpc2/common", func(ctx context.Context) (rpcClient, error) { return newOursAdapter(ctx) }},
		{"jrpc2/common", func(ctx context.Context) (rpcClient, error) { return newJRPC2CommonAdapter(ctx) }},
		{"mcp/common", func(ctx context.Context) (rpcClient, error) { return newMCPAdapter(ctx) }},
	}
}

// allAdapters is the union used by transport-comparing benchmarks.
func allAdapters() []adapterFactory {
	return append(nativeAdapters(), commonAdapters()...)
}

// benchmarkSingleAdapter runs a benchmark body against one adapter factory.
// It is used for transport-specific jsonrpc2-only benchmarks that do not compare
// against the other libraries.
func benchmarkSingleAdapter(b *testing.B, name string, make func(ctx context.Context) (rpcClient, error), body func(*testing.B, context.Context, rpcClient)) {
	b.Run(name, func(b *testing.B) {
		ctx := b.Context()
		c, err := make(ctx)
		if err != nil {
			b.Fatalf("make adapter: %v", err)
		}
		defer c.Close()

		b.ReportAllocs()
		body(b, ctx, c)
	})
}

func newOursHeaderRPCClient(ctx context.Context) (rpcClient, error) {
	return newOursHeaderAdapter(ctx)
}

func newOursDirectRPCClient(ctx context.Context) (rpcClient, error) {
	return newOursDirectAdapter(ctx)
}

func newOursSyncRPCClient(ctx context.Context) (rpcClient, error) {
	return newOursSyncAdapter(ctx)
}

// paramsSmall, paramsMedium, paramsLarge are identical pre-encoded JSON payloads
// reused verbatim across every library, so the only variable is the library, not
// the bytes on the wire. Sizes are approximately 50 B, 256 B, and 4 KiB.
var (
	paramsSmall  = makeParams(50)
	paramsMedium = makeParams(256)
	paramsLarge  = makeParams(4096)
)

// makeParams builds a compact JSON object {"d":"<filler>"} whose total encoded
// length is approximately n bytes, so the same byte slice can be sent by all
// three libraries.
func makeParams(n int) []byte {
	const prefix = `{"d":"`
	const suffix = `"}`
	fill := n - len(prefix) - len(suffix)
	if fill < 0 {
		fill = 0
	}
	b := make([]byte, 0, n)
	b = append(b, prefix...)
	for range fill {
		b = append(b, 'x')
	}
	b = append(b, suffix...)
	return b
}

// BenchmarkRoundTripVoid is the AC-P1 headline: a sequential void round-trip
// with nil params over each library and transport. It reports ns/op, B/op, and
// allocs/op.
func BenchmarkRoundTripVoid(b *testing.B) {
	for _, f := range allAdapters() {
		b.Run(f.name, func(b *testing.B) {
			ctx := b.Context()
			c, err := f.make(ctx)
			if err != nil {
				b.Fatalf("make adapter: %v", err)
			}
			defer c.Close()

			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.Call(ctx, voidMethod, nil); err != nil {
					b.Fatalf("Call: %v", err)
				}
			}
		})
	}
}

// BenchmarkRoundTripVoidParallel approximates the jrpc2 C4/C12 concurrency
// sweep: it drives the void round-trip from multiple goroutines via
// b.RunParallel with SetParallelism. The native jrpc2 server defaults to
// Concurrency:1 here to keep the comparison about transport+protocol overhead;
// raising parallelism stresses each library's connection mutexes.
func BenchmarkRoundTripVoidParallel(b *testing.B) {
	for _, par := range []int{4, 12} {
		for _, f := range allAdapters() {
			b.Run(f.name+"/P"+strconv.Itoa(par), func(b *testing.B) {
				ctx := b.Context()
				c, err := f.make(ctx)
				if err != nil {
					b.Fatalf("make adapter: %v", err)
				}
				defer c.Close()

				b.SetParallelism(par)
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if _, err := c.Call(ctx, voidMethod, nil); err != nil {
							b.Errorf("Call: %v", err)
							return
						}
					}
				})
			})
		}
	}
}

// BenchmarkRoundTripParams measures the round-trip cost as a function of payload
// size, using the same pre-encoded params bytes for every library.
func BenchmarkRoundTripParams(b *testing.B) {
	sizes := []struct {
		name   string
		params []byte
	}{
		{"small", paramsSmall},
		{"medium", paramsMedium},
		{"large", paramsLarge},
	}
	for _, sz := range sizes {
		for _, f := range allAdapters() {
			b.Run(sz.name+"/"+f.name, func(b *testing.B) {
				ctx := b.Context()
				c, err := f.make(ctx)
				if err != nil {
					b.Fatalf("make adapter: %v", err)
				}
				defer c.Close()

				b.SetBytes(int64(len(sz.params)))
				b.ReportAllocs()
				for b.Loop() {
					if _, err := c.Call(ctx, voidMethod, sz.params); err != nil {
						b.Fatalf("Call: %v", err)
					}
				}
			})
		}
	}
}

// BenchmarkNotify measures a fire-and-forget notification. Because a
// notification produces no response, the benchmark only times the send; the
// connection's read goroutine still drains the frame on the server side.
func BenchmarkNotify(b *testing.B) {
	for _, f := range allAdapters() {
		b.Run(f.name, func(b *testing.B) {
			ctx := b.Context()
			c, err := f.make(ctx)
			if err != nil {
				b.Fatalf("make adapter: %v", err)
			}
			defer c.Close()

			b.ReportAllocs()
			for b.Loop() {
				if err := c.Notify(ctx, voidMethod, nil); err != nil {
					b.Fatalf("Notify: %v", err)
				}
			}
		})
	}
}

// BenchmarkBatch measures batches of 1, 4, and 16 void calls. jrpc2 uses its
// public Client.Batch; mcp issues concurrent calls (no public batch-send); jsonrpc2
// hand-frames the batch array and reads the response array (no public
// batch-send on Conn). The differing batch mechanics are documented in
// adapters.go and RESULTS.md.
func BenchmarkBatch(b *testing.B) {
	for _, n := range []int{1, 4, 16} {
		for _, f := range allAdapters() {
			b.Run("n"+strconv.Itoa(n)+"/"+f.name, func(b *testing.B) {
				ctx := b.Context()
				c, err := f.make(ctx)
				if err != nil {
					b.Fatalf("make adapter: %v", err)
				}
				defer c.Close()

				b.ReportAllocs()
				for b.Loop() {
					if err := c.Batch(ctx, n); err != nil {
						b.Fatalf("Batch: %v", err)
					}
				}
			})
		}
	}
}

// BenchmarkRoundTripVoidHeader measures the jsonrpc2 adapter over the header
// transport, which is the package's default framing.
func BenchmarkRoundTripVoidHeader(b *testing.B) {
	benchmarkSingleAdapter(b, "jsonrpc2/header", newOursHeaderRPCClient, func(b *testing.B, ctx context.Context, c rpcClient) {
		for b.Loop() {
			if _, err := c.Call(ctx, voidMethod, nil); err != nil {
				b.Fatalf("Call: %v", err)
			}
		}
	})
}

// BenchmarkRoundTripVoidDirect measures the benchmark-local direct transport,
// which bypasses net.Pipe while still exercising the same Conn and batch code.
func BenchmarkRoundTripVoidDirect(b *testing.B) {
	benchmarkSingleAdapter(b, "jsonrpc2/direct", newOursDirectRPCClient, func(b *testing.B, ctx context.Context, c rpcClient) {
		for b.Loop() {
			if _, err := c.Call(ctx, voidMethod, nil); err != nil {
				b.Fatalf("Call: %v", err)
			}
		}
	})
}

// BenchmarkRoundTripVoidSync measures the A1c synchronous-client mode: jsonrpc2
// SyncClient (no client-side background reader; the caller owns the read loop)
// against an ordinary jsonrpc2 Conn server over net.Pipe with NDJSON framing.
//
// MODE DISCLOSURE: this is a DISTINCT execution model, not the concurrent Conn
// (jsonrpc2/native) and not comparable head-to-head with jrpc2's channel.Direct
// (both keep a client-side reader). It removes the client's third goroutine hop,
// so a lower number here means "jsonrpc2's fastest in-process request path," NOT that
// the concurrent Conn got faster. It is reported separately for exactly the same
// integrity reason the batch rows are: the operation is not the same shape as
// the rivals'. See sync_adapter.go and RESULTS.md.
func BenchmarkRoundTripVoidSync(b *testing.B) {
	benchmarkSingleAdapter(b, "jsonrpc2/sync", newOursSyncRPCClient, func(b *testing.B, ctx context.Context, c rpcClient) {
		for b.Loop() {
			if _, err := c.Call(ctx, voidMethod, nil); err != nil {
				b.Fatalf("Call: %v", err)
			}
		}
	})
}

// BenchmarkBatchHeader measures batch handling over the header transport.
func BenchmarkBatchHeader(b *testing.B) {
	benchmarkSingleAdapter(b, "n16/jsonrpc2/header", newOursHeaderRPCClient, func(b *testing.B, ctx context.Context, c rpcClient) {
		for b.Loop() {
			if err := c.Batch(ctx, 16); err != nil {
				b.Fatalf("Batch: %v", err)
			}
		}
	})
}

// BenchmarkBatchDirect measures batch handling over the benchmark-local direct
// transport.
func BenchmarkBatchDirect(b *testing.B) {
	benchmarkSingleAdapter(b, "n16/jsonrpc2/direct", newOursDirectRPCClient, func(b *testing.B, ctx context.Context, c rpcClient) {
		for b.Loop() {
			if err := c.Batch(ctx, 16); err != nil {
				b.Fatalf("Batch: %v", err)
			}
		}
	})
}

// decodeInputs mirror the jrpc2 BenchmarkParseRequests inputs so the pure-decode
// comparison (AC-P2 anchor) runs on identical bytes.
var decodeInputs = []struct {
	name  string
	input string
	batch bool
}{
	{"Minimal", `{"jsonrpc":"2.0","id":1,"method":"Foo.Bar","params":null}`, false},
	{"Medium", `{
  "jsonrpc": "2.0",
  "id": 23593,
  "method": "Four square meals in one day",
  "params": [
     "year",
     1994,
     {"month": "July", "day": 26},
     true
  ]
}`, false},
	{"Batch", `[{"jsonrpc":"2.0","id":1,"method":"Abel","params":[1,3,5]},
        {"jsonrpc":"2.0","id":2,"method":"Baker","params":{"x":99}},
        {"jsonrpc":"2.0","id":3,"method":"Charlie","params":["foo",19,true]},
        {"jsonrpc":"2.0","id":4,"method":"Delta","params":{}},
        {"jsonrpc":"2.0","id":5,"method":"Echo","params":[]}]`, true},
}

// BenchmarkDecode measures pure decode cost on identical bytes with NO transport
// involved. For single-message inputs jsonrpc2 uses DecodeMessage and mcp uses
// DecodeMessage; for the batch input jsonrpc2 and jrpc2 use their ParseRequests.
// jrpc2 uses ParseRequests for every input (it has no single-message decoder).
func BenchmarkDecode(b *testing.B) {
	for _, in := range decodeInputs {
		msg := []byte(in.input)

		b.Run(in.name+"/jsonrpc2", func(b *testing.B) {
			b.ReportAllocs()
			if in.batch {
				for b.Loop() {
					if _, err := jsonrpc2.ParseRequests(msg); err != nil {
						b.Fatalf("jsonrpc2.ParseRequests: %v", err)
					}
				}
			} else {
				for b.Loop() {
					if _, err := jsonrpc2.DecodeMessage(msg); err != nil {
						b.Fatalf("jsonrpc2.DecodeMessage: %v", err)
					}
				}
			}
		})

		b.Run(in.name+"/jrpc2", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := jrpc2.ParseRequests(msg); err != nil {
					b.Fatalf("jrpc2.ParseRequests: %v", err)
				}
			}
		})

		// mcp.DecodeMessage decodes a single message only; for the batch input it
		// cannot parse an array, so the mcp row is reported only for the
		// single-message inputs.
		if !in.batch {
			b.Run(in.name+"/mcp", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := mcp.DecodeMessage(msg); err != nil {
						b.Fatalf("mcp.DecodeMessage: %v", err)
					}
				}
			})
		}
	}
}

// BenchmarkDecodeParseRequests isolates the request-array parse path on the same
// inputs for jsonrpc2 vs jrpc2 (both expose ParseRequests). It complements
// BenchmarkDecode by always exercising the batch-capable entry point, even for
// single-message inputs, so the two libraries' ParseRequests are compared
// directly. mcp has no ParseRequests and is omitted.
func BenchmarkDecodeParseRequests(b *testing.B) {
	for _, in := range decodeInputs {
		msg := []byte(in.input)

		b.Run(in.name+"/jsonrpc2", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := jsonrpc2.ParseRequests(msg); err != nil {
					b.Fatalf("jsonrpc2.ParseRequests: %v", err)
				}
			}
		})

		b.Run(in.name+"/jrpc2", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := jrpc2.ParseRequests(msg); err != nil {
					b.Fatalf("jrpc2.ParseRequests: %v", err)
				}
			}
		})
	}
}

// encodeInputs build one message per library from identical logical content so
// the pure-encode comparison runs on the same envelope shape. Each library
// encodes its own native message type; the resulting bytes are equivalent JSON.
func BenchmarkEncode(b *testing.B) {
	const method = "Foo.Bar"
	id := int64(1)
	params := jsonrpc2.RawMessage(paramsSmall)

	// jsonrpc2: build a *Call and append-encode it.
	jsonrpc2Call := jsonrpc2.NewCall(jsonrpc2.NewNumberID(id), method, params)

	// mcp: build a *Request call with the same method/params.
	mcpCall, err := mcp.NewCall(mcp.Int64ID(id), method, paramsSmall)
	if err != nil {
		b.Fatalf("mcp.NewCall: %v", err)
	}

	b.Run("jsonrpc2", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := jsonrpc2.EncodeMessage(jsonrpc2Call); err != nil {
				b.Fatalf("jsonrpc2.EncodeMessage: %v", err)
			}
		}
	})

	b.Run("mcp", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := mcp.EncodeMessage(mcpCall); err != nil {
				b.Fatalf("mcp.EncodeMessage: %v", err)
			}
		}
	})

	// jrpc2 exposes no public single-message encoder equivalent to
	// EncodeMessage/AppendMessage; its wire encoding is internal to the
	// client/server. The encode comparison is therefore jsonrpc2 vs mcp only, which
	// is recorded in RESULTS.md.
}
