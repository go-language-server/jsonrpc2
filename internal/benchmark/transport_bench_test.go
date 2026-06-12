// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
)

// This file is the committed re-profiling apparatus for the syscall-batching
// performance layer (.omc/plans/syscall-batching-perf-layer.md). It measures the
// void round trip over real transports (Unix socket, TCP loopback) in addition
// to the in-memory net.Pipe baseline, and counts the client-side Read/Write
// syscalls per call across a concurrency sweep.
//
// The load-bearing finding it reproduces: on a real socket, writes/call stays
// pinned at 1.000 at every concurrency level (each Call issues its own write),
// while reads/call falls toward ~0.088 at 64 in-flight (the bufio reader plus
// netpoller already coalesce response reads for free). The single batchable
// syscall is therefore the outbound write — the only target for a vectored
// (writev) framer — and read batching has no target. See the plan's section 0.

// syscallCountConn wraps a net.Conn and counts Read/Write calls, a proxy for the
// read/write syscalls the runtime issues per frame. The bufio reader inside the
// stream batches reads, so Read here counts the syscall-issuing refills; each
// frame Write is one syscall.
type syscallCountConn struct {
	net.Conn
	reads  *int64
	writes *int64
}

func (c syscallCountConn) Read(p []byte) (int, error) {
	atomic.AddInt64(c.reads, 1)
	return c.Conn.Read(p)
}

func (c syscallCountConn) Write(p []byte) (int, error) {
	atomic.AddInt64(c.writes, 1)
	return c.Conn.Write(p)
}

// dialTransportPair accepts one connection on ln and dials the matching client
// end, returning both. Used to build TCP and Unix socket pairs.
func dialTransportPair(b *testing.B, ln net.Listener) (client, server net.Conn) {
	b.Helper()
	type res struct {
		c   net.Conn
		err error
	}
	accepted := make(chan res, 1)
	go func() {
		c, err := ln.Accept()
		accepted <- res{c, err}
	}()
	c, err := net.Dial(ln.Addr().Network(), ln.Addr().String())
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	r := <-accepted
	if r.err != nil {
		b.Fatalf("accept: %v", r.err)
	}
	return c, r.c
}

// unixListener returns a Unix socket listener under a short /tmp directory
// (Darwin rejects long sockaddr_un paths with EINVAL); teardown is registered
// via b.Cleanup so callers need not thread a cleanup func.
func unixListener(b *testing.B) net.Listener {
	b.Helper()
	dir, err := os.MkdirTemp("/tmp", "jrpc2-bench-")
	if err != nil {
		b.Fatalf("mkdtemp: %v", err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "p.sock"))
	if err != nil {
		_ = os.RemoveAll(dir)
		b.Fatalf("listen unix: %v", err)
	}
	b.Cleanup(func() {
		_ = ln.Close()
		_ = os.RemoveAll(dir)
	})
	return ln
}

// voidServerHandler answers every request with a nil result.
func voidServerHandler(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	return nil, nil
}

// benchVoidConcurrent drives `inflight` concurrent void Calls per b.Loop
// iteration over the supplied client/server connection pair, wrapping the client
// connection in a syscallCountConn so it can report reads/call and writes/call.
// At inflight=1 it degenerates to the sequential round trip; higher values
// exercise the existing Conn's concurrent in-flight path (background reader plus
// id-keyed pending map), which is the correct matched-inflight baseline for the
// plan's two tracks.
//
// Each iteration fans out `inflight` calls and waits for all to return — a
// burst-and-drain, not a sustained steady-state, workload. That under-states the
// response batching a never-draining queue would show, so the reported reads/call
// is a conservative floor on the coalescing effect (which only strengthens the
// "reads already coalesce, writes do not" finding).
func benchVoidConcurrent(b *testing.B, ca, cb net.Conn, inflight int) {
	var creads, cwrites int64
	ctx := b.Context()
	client := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(syscallCountConn{ca, &creads, &cwrites}))
	server := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(cb))
	client.Go(ctx, jsonrpc2.MethodNotFoundHandler)
	server.Go(ctx, voidServerHandler)
	defer func() {
		_ = client.Close()
		<-client.Done()
		_ = server.Close()
		<-server.Done()
	}()

	if _, err := client.Call(ctx, "void", nil, nil); err != nil {
		b.Fatalf("warmup: %v", err)
	}
	atomic.StoreInt64(&creads, 0)
	atomic.StoreInt64(&cwrites, 0)

	b.ReportAllocs()
	var totalCalls int64
	for b.Loop() {
		if inflight == 1 {
			if _, err := client.Call(ctx, "void", nil, nil); err != nil {
				b.Fatalf("Call: %v", err)
			}
		} else {
			var wg sync.WaitGroup
			wg.Add(inflight)
			for range inflight {
				go func() {
					defer wg.Done()
					if _, err := client.Call(ctx, "void", nil, nil); err != nil {
						b.Errorf("Call: %v", err)
					}
				}()
			}
			wg.Wait()
		}
		totalCalls += int64(inflight)
	}
	if calls := float64(totalCalls); calls > 0 {
		b.ReportMetric(float64(atomic.LoadInt64(&creads))/calls, "reads/call")
		b.ReportMetric(float64(atomic.LoadInt64(&cwrites))/calls, "writes/call")
	}
}

// transportInflight is the concurrency sweep the plan's acceptance criteria are
// measured at: C in {1, 8, 64, 256}.
var transportInflight = []int{1, 8, 64, 256}

// BenchmarkTransportNetPipe is the in-memory baseline (no syscalls; the counters
// still report, but reads/writes go through the pipe, not the kernel).
func BenchmarkTransportNetPipe(b *testing.B) {
	for _, c := range transportInflight {
		b.Run(inflightName(c), func(b *testing.B) {
			ca, cb := net.Pipe()
			benchVoidConcurrent(b, ca, cb, c)
		})
	}
}

// BenchmarkTransportUnix is the real-socket sweep that reproduces the write/read
// syscall asymmetry: writes/call pinned at 1.000, reads/call falling with C.
func BenchmarkTransportUnix(b *testing.B) {
	for _, c := range transportInflight {
		b.Run(inflightName(c), func(b *testing.B) {
			ca, cb := dialTransportPair(b, unixListener(b))
			benchVoidConcurrent(b, ca, cb, c)
		})
	}
}

// BenchmarkTransportTCP is the TCP loopback sweep (highest per-syscall cost).
// TCP_NODELAY is enabled by default in Go's net package, so no Nagle tuning is
// needed for a low-latency round-trip measurement.
func BenchmarkTransportTCP(b *testing.B) {
	for _, c := range transportInflight {
		b.Run(inflightName(c), func(b *testing.B) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				b.Fatalf("listen tcp: %v", err)
			}
			defer ln.Close()
			ca, cb := dialTransportPair(b, ln)
			benchVoidConcurrent(b, ca, cb, c)
		})
	}
}

// BenchmarkTransportOSPipe is the stdio-shaped control. JSON-RPC clients used
// by LSP and MCP often communicate over paired process pipes rather than
// sockets; this keeps that workload visible before promoting any writev or
// socket-specific transport optimization.
func BenchmarkTransportOSPipe(b *testing.B) {
	for _, c := range transportInflight {
		b.Run(inflightName(c), func(b *testing.B) {
			ca, cb := osPipePair(b)
			benchVoidConcurrent(b, ca, cb, c)
		})
	}
}

// osPipePair returns a full-duplex connection pair built from two unidirectional
// OS pipes. It implements net.Conn so the existing syscall-counting benchmark
// can drive it through the same connection wrapper used for net.Pipe, Unix, and
// TCP.
func osPipePair(tb testing.TB) (client, server net.Conn) {
	tb.Helper()
	c2sR, c2sW, err := os.Pipe()
	if err != nil {
		tb.Fatalf("os.Pipe client->server: %v", err)
	}
	s2cR, s2cW, err := os.Pipe()
	if err != nil {
		_ = c2sR.Close()
		_ = c2sW.Close()
		tb.Fatalf("os.Pipe server->client: %v", err)
	}
	return pipeConn{r: s2cR, w: c2sW}, pipeConn{r: c2sR, w: s2cW}
}

type pipeConn struct {
	r *os.File
	w *os.File
}

func (c pipeConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c pipeConn) Write(p []byte) (int, error) { return c.w.Write(p) }

func (c pipeConn) Close() error {
	err := c.r.Close()
	if werr := c.w.Close(); err == nil {
		err = werr
	}
	return err
}

func (c pipeConn) LocalAddr() net.Addr { return pipeAddr("local") }

func (c pipeConn) RemoteAddr() net.Addr { return pipeAddr("remote") }

func (c pipeConn) SetDeadline(time.Time) error { return nil }

func (c pipeConn) SetReadDeadline(time.Time) error { return nil }

func (c pipeConn) SetWriteDeadline(time.Time) error { return nil }

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }

func (a pipeAddr) String() string { return string(a) }

// inflightName renders a stable sub-benchmark name for a concurrency level in
// transportInflight.
func inflightName(c int) string {
	return "Inflight" + strconv.Itoa(c)
}
