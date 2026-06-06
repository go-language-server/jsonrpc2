# jsonrpc2

[![CircleCI][circleci-badge]][circleci] [![pkg.go.dev][pkg.go.dev-badge]][pkg.go.dev] [![Go module][module-badge]][module] [![codecov.io][codecov-badge]][codecov] [![GA][ga-badge]][ga]

Package `jsonrpc2` is a fast, allocation-conscious implementation of the
[JSON-RPC 2.0](https://www.jsonrpc.org/specification) wire protocol for Go,
designed for Language Server Protocol (LSP) and Model Context Protocol (MCP)
style transports.

## Overview

The library is built around a reflection-free wire core plus pluggable framing,
a swappable payload codec, and a bidirectional connection state machine:

- **Reflection-free envelope.** Message envelopes are encoded by appending
  directly into a pooled byte buffer and decoded with a single-pass span
  scanner, so the hot path performs no reflection and copies each payload at most
  once. Reflection is confined to the user payload (`params` / `result`) and only
  through a swappable codec.
- **Symmetric peer connection.** A `Conn` is a bidirectional peer that can both
  issue and answer calls, notifications, responses, and errors — the shape LSP
  requires.
- **Batch, cancellation, graceful shutdown.** JSON-RPC 2.0 batch arrays,
  per-request cancellation, and idle-detecting graceful close are all supported.
- **Two wire framings and a pluggable codec.** Newline-delimited JSON (MCP
  stdio) and LSP `Content-Length` header framing; the payload codec defaults to
  `encoding/json/v2` and can be swapped for opt-in `sonic` or `goccy` codecs.
- **Explicit fast-path modes.** `Conn`/`Peer` stay bidirectional; `SingleClient`
  is the serialized caller-owned-read-loop path; `PipelineClient` is a
  concurrent client-only mode; `BatchClient` exposes raw frame batch I/O.

## Install

```sh
go get go.lsp.dev/jsonrpc2
```

The module requires Go 1.26 or later. The importable core depends only on
[`github.com/go-json-experiment/json`](https://github.com/go-json-experiment/json)
(encoding/json/v2); no assembly, JIT, or heavy transitive dependencies enter the
core module graph.

## Runtime modes and borrowed views

Choose the smallest mode that matches the workload; the fast paths are not all
interchangeable:

- `NewConn` / `NewPeer`: bidirectional JSON-RPC peer mode. Use it for LSP-style
  connections where either side may send calls, notifications, responses, and
  server-initiated requests.
- `NewSingleClient` / `NewSyncClient`: serialized single-flight client mode. The
  caller owns the read loop for each `Call`, so it avoids the background-reader
  hand-off, but only one call may be outstanding and server-initiated requests
  are not dispatched.
- `NewPipelineClient`: concurrent client-only mode. It uses generated numeric
  IDs, dense wait slots, pooled waiters, and a canonical success-response scanner
  for the common response shape. Start its response reader with `Go` once; use
  `Conn`/`Peer` instead when the remote can initiate requests.
- `NewBatchClient`: raw-frame batch mode for callers that already build a JSON
  batch array and want to write/read one frame without routing each member
  through `Conn.Call`.

For parser-only fast paths, `ScanMessageView`, `ScanFrameView`, and
`AppendRequestViews` return borrowed spans over caller-owned frame bytes. Those
views are valid only while the source frame remains valid and unmodified; call
`Clone`/`Owned` before retaining data beyond the callback or read iteration.

## Quickstart

### Client: issue a call

```go
package main

import (
	"context"
	"log"
	"net"

	"go.lsp.dev/jsonrpc2"
)

func main() {
	ctx := context.Background()

	nc, err := net.Dial("tcp", "127.0.0.1:4389")
	if err != nil {
		log.Fatal(err)
	}

	// NewStream uses the LSP Content-Length header framing by default.
	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(nc))
	conn.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	defer conn.Close()

	type hoverParams struct {
		URI  string `json:"uri"`
		Line int    `json:"line"`
	}
	type hoverResult struct {
		Contents string `json:"contents"`
	}

	var res hoverResult
	if _, err := conn.Call(ctx, "textDocument/hover",
		hoverParams{URI: "file:///a.go", Line: 12}, &res); err != nil {
		log.Fatal(err)
	}
	log.Printf("hover: %s", res.Contents)

	// Notifications are fire-and-forget: no id, no response.
	if err := conn.Notify(ctx, "textDocument/didSave", hoverParams{URI: "file:///a.go"}); err != nil {
		log.Fatal(err)
	}
}
```

### Server: HandlerServer + Serve

A `Handler` answers each incoming request by calling `reply` exactly once for a
call. `HandlerServer` adapts a `Handler` into a `StreamServer`, and `Serve`
accepts connections from a `net.Listener`, driving each on its own goroutine.

```go
package main

import (
	"context"
	"log"
	"net"

	"go.lsp.dev/jsonrpc2"
)

func handler(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	switch req.Method() {
	case "textDocument/hover":
		// Decode params, do work, reply with a typed result.
		return reply(ctx, map[string]string{"contents": "hello"}, nil)
	default:
		// Answer unknown calls with the standard error.
		return reply(ctx, nil, jsonrpc2.ErrMethodNotFound)
	}
}

func main() {
	ctx := context.Background()

	ln, err := net.Listen("tcp", "127.0.0.1:4389")
	if err != nil {
		log.Fatal(err)
	}

	server := jsonrpc2.HandlerServer(handler)

	// idleTimeout = 0 means "serve until ctx is canceled or accept fails".
	if err := jsonrpc2.Serve(ctx, ln, server, 0); err != nil {
		log.Fatal(err)
	}
}
```

`ListenAndServe(ctx, network, addr, server, idleTimeout)` is a convenience that
creates the listener for you (and removes the socket file for a `unix` network).

A request handler runs inline on the read goroutine by default, so handlers
observe requests in wire order. A handler that needs to overlap later requests
(a long-running call, or a server that calls back into the same connection) must
release itself with `jsonrpc2.Async(ctx)` or be wrapped with
`jsonrpc2.AsyncHandler`. `jsonrpc2.CancelHandler` adds cancellation by request
id.

## Framing options

A `Stream` adapts a byte transport (`io.ReadWriteCloser`) to message reads and
writes. Two framings are provided:

| Constructor | Framing | Compatible with |
|-------------|---------|-----------------|
| `NewStream` / `NewHeaderStream` | `Content-Length` header block then body | LSP, gopls |
| `NewNDJSONStream` / `NewRawStream` | one JSON value per line (`\n`-delimited) | MCP stdio transport |

```go
// LSP header framing (the gopls-compatible default).
conn := jsonrpc2.NewConn(jsonrpc2.NewStream(rwc))

// Newline-delimited JSON framing (MCP-compatible).
conn := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(rwc))
```

Both framings write each message with a single `Write` (header and body, or
payload and its newline, are composed into one pooled buffer), avoiding the
two-syscall-per-message pattern.

## Pluggable codec

The envelope is never routed through a codec; only the user payload (`params`
and `result`) is. The payload `Codec` is swappable per connection and defaults to
`encoding/json/v2`:

```go
// Default: encoding/json/v2 (pure Go, all platforms, no JIT/asm).
conn := jsonrpc2.NewConn(stream)

// Opt-in faster codecs live in separate modules so their dependencies never
// enter the core module graph:
import sonic "go.lsp.dev/jsonrpc2/codec/sonic"
conn := jsonrpc2.NewConn(stream, jsonrpc2.WithCodec(sonic.Codec{}))

import goccy "go.lsp.dev/jsonrpc2/codec/goccy"
conn := jsonrpc2.NewConn(stream, jsonrpc2.WithCodec(goccy.Codec{}))
```

A `RawMessage` passed as params/result, or decoded into a `*RawMessage`, bypasses
the codec entirely and is carried verbatim.

## Performance

`jsonrpc2` is benchmarked head-to-head against
[`github.com/creachadair/jrpc2`](https://github.com/creachadair/jrpc2) and the
Model Context Protocol Go SDK's `jsonrpc2`, using the harness in
[`internal/benchmark`](./internal/benchmark). The harness holds the transport
constant per comparison, isolates rival dependencies in a separate module, and
asserts (via `go-cmp`) that all three decoders extract the same method and params
from identical input, so the comparison measures equivalent work.

Lower is better; the winner is per row. The **`amd64`** table is a measured
historical claim anchor; the `arm64` table and mode-specific artifacts below are
the current developer baseline for the latest local optimization pass.

### Headline: void round-trip (nil params) — amd64 (historical claim arch)

`linux/amd64`, Intel Xeon Platinum 8481C (GCE c3, 44 vCPU), Debian 13, Go 1.26.4,
`benchstat` over `-count=10`:

| Library | ns/op | B/op | allocs/op | vs ours |
|---------|------:|-----:|----------:|---------|
| **jsonrpc2 (ours)** | **~4780** | **585** | **12** | — (fastest) |
| jrpc2 | ~12330 | 4480 | 100 | 2.6× slower, 8.3× allocs |
| mcp | ~44220 | 100919 | 46 | 9.3× slower |

### Same, on `arm64` (Apple M3 Max, secondary baseline)

These arm64 numbers reflect the **next-layer allocation pass** (the concurrent
`Conn` void round trip fell from 10 to **6 allocs/op** on the root benchmark via
two general, GC-safe changes: a concrete non-interface write path that drops the
per-send envelope box, and folding the per-request releaser and replied flag into
the request struct — no pooling, no `unsafe`, no ownership change). The B/op and
ns/op fell correspondingly.

| Library | ns/op | B/op | allocs/op (root) |
|---------|------:|-----:|----------:|
| **jsonrpc2 (ours)** | **~2900** | **408** | **6** |
| jrpc2 | ~7990 | 4469 | 100 |
| mcp | ~22500 | 100550 | 46 |

> **amd64 refresh caveat.** The amd64 headline table above is a measured artifact,
> but it has not been refreshed for the latest local arm64 allocation/runtime-mode
> pass. Re-run the CircleCI `bench` job before quoting amd64 numbers for the new
> six-allocation `Conn` state or the new mode-specific clients. The arm64 row here
> is the directly measured secondary baseline for the new state.

### Synchronous-client mode (`SyncClient`) — a distinct, lower-latency request path

`SyncClient` is a synchronous client that **owns its read loop** instead of
running a background reader goroutine: each `Call` writes the request and reads
its own response on the caller's goroutine, collapsing the dedicated-reader-to-
caller hand-off (the third goroutine hop) a concurrent `Conn` pays. On the same
`net.Pipe` + NDJSON transport against an ordinary `Conn` server:

| Path | ns/op | B/op | allocs/op |
|------|------:|-----:|----------:|
| `Conn` round trip (concurrent) | ~2900 | 408 | 6 |
| `SyncClient` round trip | **~2030** | 408 | 6 |

**Read this as a mode, not a speedup of `Conn`.** `SyncClient` removes the
*client's* reader goroutine; it cannot receive server-initiated requests and
serializes its calls (one outstanding at a time). It is therefore reported
separately — the same integrity reason the batch rows are excluded — and is **not**
a head-to-head claim against jrpc2's `channel.Direct` (which keeps a client-side
reader). A lower number here means "ours' fastest in-process request path," not
that the bidirectional `Conn` got faster. Use `Conn` with `Conn.Go` when you need
concurrent calls or server-to-client requests.

### Pipelined-client mode (`PipelineClient`) — concurrent client-only path

`PipelineClient` keeps many client-originated calls in flight, but it does not
dispatch server-initiated calls. It uses dense generated-ID slots and scans
canonical success responses before falling back to the borrowed `MessageView`
scanner. On Apple M3 Max (`go1.26.4 darwin/arm64`, `net.Pipe` + NDJSON,
`-benchtime=200x -count=10`), the preserved artifact
`internal/benchmark/artifacts/20260606T012124Z-root-pipeline-fastpath-vs-conn`
showed:

| Inflight | Conn ns/op | PipelineClient ns/op | allocs/op |
|---------:|-----------:|---------------------:|----------:|
| 1 | 2.914 µs | **2.565 µs** (-12.0%) | 6 → **4** |
| 8 | 34.99 µs | **32.00 µs** (-8.6%) | 57 → **41** |
| 64 | 325.4 µs | **257.1 µs** (-21.0%) | 451 → **322** |
| 256 | 1.232 ms | 1.241 ms (statistically neutral) | 1810 → **1296** |

Read this as a client-only mode result: it proves lower allocation pressure at
all measured inflight levels and latency wins up to inflight 64 on this harness;
it is not a bidirectional `Conn` replacement.

### Pure decode on identical bytes (no transport, AC-P2 anchor) — amd64

| Input | ours ns / B / allocs | jrpc2 ns / B / allocs | mcp ns / B / allocs |
|-------|----------------------|-----------------------|---------------------|
| Minimal | **175 / 88 / 2** | 3228 / 1504 / 31 (18.4×) | 7127 / 33032 / 8 (40.7×) |
| Medium | **399 / 192 / 3** | 5189 / 1781 / 36 | 9041 / 33147 / 9 |
| Batch | **1857 / 1008 / 25** | 17290 / 7256 / 144 (9.3×) | n/a |

`Encode` is **85 ns / 112 B / 1 alloc** for `ours` vs `mcp` 898 ns / 289 B / 3
(10.6×); `jrpc2` exposes no single-message encoder.

### Caveats (read before quoting these numbers)

These caveats are load-bearing; the benchmark is reported honestly rather than to
flatter the library.

- **Keep fastest claims artifact-scoped.** The amd64 headline above was measured
  directly on an Intel Xeon 8481C server (Debian 13, Go 1.26.4, `-count=10`), but
  it is not a substitute for current-head artifacts when quoting newer
  allocation/runtime-mode work. Use the raw artifact path, command, Go version,
  GOOS/GOARCH, CPU, git SHA, and mode disclosure whenever publishing numbers.
- **Batch rows are excluded from the "lowest-cost on every workload" claim.** The
  batch mechanics differ by library — `jrpc2` issues a true single
  batch request/response, `mcp` bursts N concurrent independent calls, and `ours`
  hand-frames the JSON-RPC array — so the batch numbers are **not** an
  apples-to-apples protocol comparison even though `ours` posts the lowest
  figures there too.
- **A documented standalone-decode allocation floor.** `DecodeMessage` and
  `ParseRequests` sit at a 2–4 alloc/op floor (message struct + copied
  method/params, plus the public slice shape for `ParseRequests`) because their
  returned `RawMessage` must own its bytes and never alias a pooled buffer. The
  `≤ 1 alloc/op` decode target is therefore documented as infeasible for the
  standalone API without breaking ownership or the public return types; the
  connection's round-trip decode is a separate, decisively winning path.

The full methodology, the `native` vs `common` transport families, the
per-workload tables, the optimization log, and the honest AC status are in
[`internal/benchmark/RESULTS.md`](./internal/benchmark/RESULTS.md).

## License

BSD-3-Clause. See [LICENSE](./LICENSE).


<!-- badge links -->
[circleci]: https://app.circleci.com/pipelines/github/go-language-server/jsonrpc2
[pkg.go.dev]: https://pkg.go.dev/go.lsp.dev/jsonrpc2
[module]: https://github.com/go-language-server/jsonrpc2/releases/latest
[codecov]: https://codecov.io/gh/go-language-server/jsonrpc2
[ga]: https://github.com/go-language-server/jsonrpc2

[circleci-badge]: https://img.shields.io/circleci/build/github/go-language-server/jsonrpc2/main.svg?style=for-the-badge&label=CIRCLECI&logo=circleci
[pkg.go.dev-badge]: https://bit.ly/shields-io-pkg-go-dev
[module-badge]: https://img.shields.io/github/release/go-language-server/jsonrpc2.svg?color=00add8&label=MODULE&style=for-the-badge&logoWidth=25&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9Ijg1IDU1IDEyMCAxMjAiPjxwYXRoIGZpbGw9IiMwMEFERDgiIGQ9Ik00MC4yIDEwMS4xYy0uNCAwLS41LS4yLS4zLS41bDIuMS0yLjdjLjItLjMuNy0uNSAxLjEtLjVoMzUuN2MuNCAwIC41LjMuMy42bC0xLjcgMi42Yy0uMi4zLS43LjYtMSAuNmwtMzYuMi0uMXptLTE1LjEgOS4yYy0uNCAwLS41LS4yLS4zLS41bDIuMS0yLjdjLjItLjMuNy0uNSAxLjEtLjVoNDUuNmMuNCAwIC42LjMuNS42bC0uOCAyLjRjLS4xLjQtLjUuNi0uOS42bC00Ny4zLjF6bTI0LjIgOS4yYy0uNCAwLS41LS4zLS4zLS42bDEuNC0yLjVjLjItLjMuNi0uNiAxLS42aDIwYy40IDAgLjYuMy42LjdsLS4yIDIuNGMwIC40LS40LjctLjcuN2wtMjEuOC0uMXptMTAzLjgtMjAuMmMtNi4zIDEuNi0xMC42IDIuOC0xNi44IDQuNC0xLjUuNC0xLjYuNS0yLjktMS0xLjUtMS43LTIuNi0yLjgtNC43LTMuOC02LjMtMy4xLTEyLjQtMi4yLTE4LjEgMS41LTYuOCA0LjQtMTAuMyAxMC45LTEwLjIgMTkgLjEgOCA1LjYgMTQuNiAxMy41IDE1LjcgNi44LjkgMTIuNS0xLjUgMTctNi42LjktMS4xIDEuNy0yLjMgMi43LTMuN2gtMTkuM2MtMi4xIDAtMi42LTEuMy0xLjktMyAxLjMtMy4xIDMuNy04LjMgNS4xLTEwLjkuMy0uNiAxLTEuNiAyLjUtMS42aDM2LjRjLS4yIDIuNy0uMiA1LjQtLjYgOC4xLTEuMSA3LjItMy44IDEzLjgtOC4yIDE5LjYtNy4yIDkuNS0xNi42IDE1LjQtMjguNSAxNy05LjggMS4zLTE4LjktLjYtMjYuOS02LjYtNy40LTUuNi0xMS42LTEzLTEyLjctMjIuMi0xLjMtMTAuOSAxLjktMjAuNyA4LjUtMjkuMyA3LjEtOS4zIDE2LjUtMTUuMiAyOC0xNy4zIDkuNC0xLjcgMTguNC0uNiAyNi41IDQuOSA1LjMgMy41IDkuMSA4LjMgMTEuNiAxNC4xLjYuOS4yIDEuNC0xIDEuN3oiLz48cGF0aCBmaWxsPSIjMDBBREQ4IiBkPSJNMTg2LjIgMTU0LjZjLTkuMS0uMi0xNy40LTIuOC0yNC40LTguOC01LjktNS4xLTkuNi0xMS42LTEwLjgtMTkuMy0xLjgtMTEuMyAxLjMtMjEuMyA4LjEtMzAuMiA3LjMtOS42IDE2LjEtMTQuNiAyOC0xNi43IDEwLjItMS44IDE5LjgtLjggMjguNSA1LjEgNy45IDUuNCAxMi44IDEyLjcgMTQuMSAyMi4zIDEuNyAxMy41LTIuMiAyNC41LTExLjUgMzMuOS02LjYgNi43LTE0LjcgMTAuOS0yNCAxMi44LTIuNy41LTUuNC42LTggLjl6bTIzLjgtNDAuNGMtLjEtMS4zLS4xLTIuMy0uMy0zLjMtMS44LTkuOS0xMC45LTE1LjUtMjAuNC0xMy4zLTkuMyAyLjEtMTUuMyA4LTE3LjUgMTcuNC0xLjggNy44IDIgMTUuNyA5LjIgMTguOSA1LjUgMi40IDExIDIuMSAxNi4zLS42IDcuOS00LjEgMTIuMi0xMC41IDEyLjctMTkuMXoiLz48L3N2Zz4=
[codecov-badge]: https://img.shields.io/codecov/c/github/go-language-server/jsonrpc2/main?logo=codecov&style=for-the-badge
[ga-badge]: https://gh-ga-beacon.appspot.com/UA-89201129-1/go-language-server/jsonrpc2?useReferer&pixel
