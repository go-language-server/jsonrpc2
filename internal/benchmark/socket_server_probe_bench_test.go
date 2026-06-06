// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cloudnetpoll "github.com/cloudwego/netpoll"
	gnet "github.com/panjf2000/gnet/v2"
	"github.com/panjf2000/gnet/v2/pkg/logging"
	"go.lsp.dev/jsonrpc2"
)

// This file is a benchmark-only probe for event-loop server candidates. It does
// not change production jsonrpc2 transports. The raw implementations below all
// parse the same NDJSON JSON-RPC request shape and write the same canonical
// success response, so StdNetRaw, NetpollRaw, and GnetRaw measure socket/event
// loop behavior rather than public API compatibility.

const (
	probeMethod        = `"method":"void"`
	probeRequestPrefix = `{"jsonrpc":"2.0","method":"void","id":`
	probeResponseHead  = `{"jsonrpc":"2.0","result":null,"id":`
	probeLineMax       = 1024
)

type socketProbeServer interface {
	Name() string
	Addr() net.Addr
	Close() error
}

type socketProbeImpl struct {
	name  string
	start func(testing.TB, string) socketProbeServer
}

func socketProbeImpls() []socketProbeImpl {
	return []socketProbeImpl{
		{name: "StdNetRaw", start: startStdNetRawProbeServer},
		{name: "StdNetProd", start: startStdNetProdProbeServer},
		{name: "NetpollRaw", start: startNetpollRawProbeServer},
		{name: "GnetRaw", start: startGnetRawProbeServer},
	}
}

func socketProbeConnCounts() []int {
	counts := []int{1, 100}
	if os.Getenv("JSONRPC2_SOCKET_PROBE_FULL") == "1" {
		counts = append(counts, 1000)
	}
	return counts
}

func TestSocketServerProbeCorrectness(t *testing.T) {
	for _, impl := range socketProbeImpls() {
		for _, network := range []string{"tcp", "unix"} {
			t.Run(impl.name+"/"+network, func(t *testing.T) {
				server := impl.start(t, network)
				defer closeProbeServer(t, server)

				conn := dialProbeServer(t, server)
				defer conn.Close()
				br := bufio.NewReader(conn)
				if err := writeProbeRequest(conn, 7); err != nil {
					t.Fatalf("write request: %v", err)
				}
				line, err := br.ReadBytes('\n')
				if err != nil {
					t.Fatalf("read response: %v", err)
				}
				assertProbeResponseID(t, line, 7)

				bad := dialProbeServer(t, server)
				if _, err := bad.Write([]byte("{bad json}\n")); err != nil {
					t.Fatalf("write malformed: %v", err)
				}
				_ = bad.SetReadDeadline(time.Now().Add(2 * time.Second))
				_, err = bufio.NewReader(bad).ReadBytes('\n')
				_ = bad.Close()
				if err == nil {
					t.Fatalf("malformed frame read unexpectedly succeeded")
				}
			})
		}
	}
}

func TestSocketServerBackpressureSlowReader(t *testing.T) {
	for _, impl := range socketProbeImpls() {
		t.Run(impl.name, func(t *testing.T) {
			server := impl.start(t, "tcp")
			defer closeProbeServer(t, server)

			slow := dialProbeServer(t, server)
			defer slow.Close()
			for id := 1; id <= 64; id++ {
				if err := writeProbeRequest(slow, id); err != nil {
					t.Fatalf("write slow request %d: %v", id, err)
				}
			}

			fast := dialProbeServer(t, server)
			defer fast.Close()
			if err := fast.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatalf("set fast deadline: %v", err)
			}
			br := bufio.NewReader(fast)
			for id := 1000; id < 1010; id++ {
				if err := writeProbeRequest(fast, id); err != nil {
					t.Fatalf("write fast request: %v", err)
				}
				line, err := br.ReadBytes('\n')
				if err != nil {
					t.Fatalf("fast response while slow reader is blocked: %v", err)
				}
				assertProbeResponseID(t, line, id)
			}
		})
	}
}

func TestSocketServerCloseDuringInFlight(t *testing.T) {
	for _, impl := range socketProbeImpls() {
		t.Run(impl.name, func(t *testing.T) {
			server := impl.start(t, "tcp")
			defer closeProbeServer(t, server)

			for i := 0; i < 32; i++ {
				conn := dialProbeServer(t, server)
				_ = writeProbeRequest(conn, i)
				_ = conn.Close()
			}

			conn := dialProbeServer(t, server)
			defer conn.Close()
			br := bufio.NewReader(conn)
			if err := writeProbeRequest(conn, 99); err != nil {
				t.Fatalf("write surviving request: %v", err)
			}
			line, err := br.ReadBytes('\n')
			if err != nil {
				t.Fatalf("surviving request after close storm: %v", err)
			}
			assertProbeResponseID(t, line, 99)
		})
	}
}

func TestSocketServerShutdownRejectsNewConnections(t *testing.T) {
	for _, impl := range socketProbeImpls() {
		t.Run(impl.name, func(t *testing.T) {
			server := impl.start(t, "tcp")
			addr := server.Addr()
			if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) && !isProbeShutdownError(err) {
				t.Fatalf("close %s: %v", server.Name(), err)
			}
			for attempt := 0; attempt < 100; attempt++ {
				conn, err := net.DialTimeout(addr.Network(), addr.String(), 10*time.Millisecond)
				if err != nil {
					return
				}
				_ = conn.Close()
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("dial %s succeeded after %s shutdown", addr, server.Name())
		})
	}
}

func BenchmarkSocketServerLatency(b *testing.B) {
	for _, impl := range socketProbeImpls() {
		for _, network := range []string{"tcp", "unix"} {
			for _, conns := range socketProbeConnCounts() {
				// Unix Conn1000 is mostly a portability stress row and tends to be less
				// relevant than TCP high-connection rows for the adoption decision.
				if network == "unix" && conns > 100 {
					continue
				}
				name := impl.name + "/" + socketProbeNetworkName(network) + "/Conn" + strconv.Itoa(conns)
				b.Run(name, func(b *testing.B) {
					runSocketProbeLatencyBench(b, impl, network, conns)
				})
			}
		}
	}
}

func BenchmarkSocketServerBackpressure(b *testing.B) {
	for _, impl := range socketProbeImpls() {
		b.Run(impl.name+"/TCP/SlowReader", func(b *testing.B) {
			server := impl.start(b, "tcp")
			defer closeProbeServer(b, server)
			for b.Loop() {
				slow := dialProbeServer(b, server)
				for id := 0; id < 32; id++ {
					if err := writeProbeRequest(slow, id); err != nil {
						b.Fatalf("write slow: %v", err)
					}
				}
				fast := dialProbeServer(b, server)
				_ = fast.SetDeadline(time.Now().Add(2 * time.Second))
				if err := writeProbeRequest(fast, 9001); err != nil {
					b.Fatalf("write fast: %v", err)
				}
				if _, err := bufio.NewReader(fast).ReadBytes('\n'); err != nil {
					b.Fatalf("read fast: %v", err)
				}
				_ = fast.Close()
				_ = slow.Close()
			}
		})
	}
}

func BenchmarkSocketServerCloseDuringInFlight(b *testing.B) {
	for _, impl := range socketProbeImpls() {
		b.Run(impl.name+"/TCP/Close32", func(b *testing.B) {
			server := impl.start(b, "tcp")
			defer closeProbeServer(b, server)
			for b.Loop() {
				for i := 0; i < 32; i++ {
					conn := dialProbeServer(b, server)
					_ = writeProbeRequest(conn, i)
					_ = conn.Close()
				}
			}
		})
	}
}

func socketProbeNetworkName(network string) string {
	switch network {
	case "tcp":
		return "TCP"
	case "unix":
		return "Unix"
	default:
		return network
	}
}

func runSocketProbeLatencyBench(b *testing.B, impl socketProbeImpl, network string, conns int) {
	server := impl.start(b, network)
	defer closeProbeServer(b, server)

	clients := make([]probeClient, conns)
	for i := range clients {
		conn := dialProbeServer(b, server)
		clients[i] = probeClient{conn: conn, reader: bufio.NewReaderSize(conn, probeLineMax)}
	}
	b.Cleanup(func() {
		for i := range clients {
			_ = clients[i].conn.Close()
		}
	})

	for i := range clients {
		if err := clients[i].call(100000 + i); err != nil {
			b.Fatalf("warmup client %d: %v", i, err)
		}
	}

	var samplesMu sync.Mutex
	var samples []int64
	var failures atomic.Int64
	var totalCalls int64
	const maxSamples = 200000

	b.ReportAllocs()
	b.ReportMetric(float64(conns), "connections/op")
	for b.Loop() {
		startID := int(atomic.AddInt64(&totalCalls, int64(conns)))
		var wg sync.WaitGroup
		wg.Add(conns)
		iterSamples := make([]int64, conns)
		for i := range clients {
			i := i
			go func() {
				defer wg.Done()
				id := startID + i
				start := time.Now()
				if err := clients[i].call(id); err != nil {
					failures.Add(1)
					return
				}
				iterSamples[i] = time.Since(start).Nanoseconds()
			}()
		}
		wg.Wait()
		if len(samples) < maxSamples {
			samplesMu.Lock()
			room := maxSamples - len(samples)
			if room > len(iterSamples) {
				room = len(iterSamples)
			}
			for _, d := range iterSamples[:room] {
				if d > 0 {
					samples = append(samples, d)
				}
			}
			samplesMu.Unlock()
		}
	}

	calls := atomic.LoadInt64(&totalCalls)
	if calls > 0 {
		b.ReportMetric(float64(calls)/float64(b.N), "calls/op")
		b.ReportMetric(float64(failures.Load())/float64(calls), "failures/call")
	}
	reportLatencyPercentiles(b, samples)
}

type probeClient struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (c *probeClient) call(id int) error {
	if err := c.conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := writeProbeRequest(c.conn, id); err != nil {
		return err
	}
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	got, ok := parseProbeResponseID(line)
	if !ok || got != id {
		return fmt.Errorf("response id = %d ok=%v, want %d frame=%q", got, ok, id, line)
	}
	return nil
}

func reportLatencyPercentiles(b *testing.B, samples []int64) {
	if len(samples) == 0 {
		b.ReportMetric(0, "p50-ns")
		b.ReportMetric(0, "p95-ns")
		b.ReportMetric(0, "p99-ns")
		return
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	percentile := func(p float64) float64 {
		idx := int(float64(len(samples)-1) * p)
		return float64(samples[idx])
	}
	b.ReportMetric(percentile(0.50), "p50-ns")
	b.ReportMetric(percentile(0.95), "p95-ns")
	b.ReportMetric(percentile(0.99), "p99-ns")
}

func assertProbeResponseID(tb testing.TB, line []byte, want int) {
	tb.Helper()
	got, ok := parseProbeResponseID(line)
	if !ok || got != want {
		tb.Fatalf("response id = %d ok=%v, want %d frame=%q", got, ok, want, line)
	}
}

func writeProbeRequest(w io.Writer, id int) error {
	_, err := fmt.Fprintf(w, probeRequestPrefix+"%d}\n", id)
	return err
}

func appendProbeResponse(dst []byte, id int) []byte {
	dst = append(dst, probeResponseHead...)
	dst = strconv.AppendInt(dst, int64(id), 10)
	dst = append(dst, '}')
	dst = append(dst, '\n')
	return dst
}

func parseProbeRequestID(line []byte) (int, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || !bytes.Contains(line, []byte(probeMethod)) {
		return 0, false
	}
	return parseJSONIntField(line, []byte(`"id"`))
}

func parseProbeResponseID(line []byte) (int, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || !bytes.Contains(line, []byte(`"result":null`)) {
		return 0, false
	}
	return parseJSONIntField(line, []byte(`"id"`))
}

func parseJSONIntField(line []byte, field []byte) (int, bool) {
	pos := bytes.Index(line, field)
	if pos < 0 {
		return 0, false
	}
	pos += len(field)
	for pos < len(line) && (line[pos] == ' ' || line[pos] == '\t' || line[pos] == ':') {
		pos++
	}
	if pos >= len(line) || line[pos] < '0' || line[pos] > '9' {
		return 0, false
	}
	var n int
	for pos < len(line) && line[pos] >= '0' && line[pos] <= '9' {
		n = n*10 + int(line[pos]-'0')
		pos++
	}
	return n, true
}

func dialProbeServer(tb testing.TB, server socketProbeServer) net.Conn {
	tb.Helper()
	var last error
	for i := 0; i < 100; i++ {
		conn, err := net.DialTimeout(server.Addr().Network(), server.Addr().String(), 2*time.Second)
		if err == nil {
			return conn
		}
		last = err
		time.Sleep(10 * time.Millisecond)
	}
	tb.Fatalf("dial %s: %v", server.Addr(), last)
	panic("unreachable")
}

func closeProbeServer(tb testing.TB, server socketProbeServer) {
	tb.Helper()
	if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) && !isProbeShutdownError(err) {
		tb.Fatalf("close %s: %v", server.Name(), err)
	}
}

func isProbeShutdownError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "closed") || strings.Contains(msg, "shutdown")
}

func unixProbeListener(tb testing.TB) net.Listener {
	tb.Helper()
	dir, err := os.MkdirTemp("/tmp", "jrpc2-sockprobe-")
	if err != nil {
		tb.Fatalf("mkdtemp: %v", err)
	}
	path := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		_ = os.RemoveAll(dir)
		tb.Fatalf("listen unix: %v", err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(dir) })
	return ln
}

func listenProbeNet(tb testing.TB, network string) net.Listener {
	tb.Helper()
	if network == "unix" {
		return unixProbeListener(tb)
	}
	ln, err := net.Listen(network, "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen %s: %v", network, err)
	}
	return ln
}

type stdNetRawProbeServer struct {
	name string
	ln   net.Listener
	done chan struct{}
	wg   sync.WaitGroup
	mu   sync.Mutex
	conn map[net.Conn]struct{}
}

func startStdNetRawProbeServer(tb testing.TB, network string) socketProbeServer {
	ln := listenProbeNet(tb, network)
	s := &stdNetRawProbeServer{name: "StdNetRaw", ln: ln, done: make(chan struct{}), conn: map[net.Conn]struct{}{}}
	s.wg.Add(1)
	go s.acceptLoop()
	return s
}

func (s *stdNetRawProbeServer) Name() string   { return s.name }
func (s *stdNetRawProbeServer) Addr() net.Addr { return s.ln.Addr() }

func (s *stdNetRawProbeServer) Close() error {
	err := s.ln.Close()
	s.mu.Lock()
	for c := range s.conn {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return err
}

func (s *stdNetRawProbeServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conn[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveConn(conn)
			s.mu.Lock()
			delete(s.conn, conn)
			s.mu.Unlock()
			_ = conn.Close()
		}()
	}
}

func (s *stdNetRawProbeServer) serveConn(conn net.Conn) {
	br := bufio.NewReaderSize(conn, probeLineMax)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		id, ok := parseProbeRequestID(line)
		if !ok {
			return
		}
		if _, err := conn.Write(appendProbeResponse(nil, id)); err != nil {
			return
		}
	}
}

func startStdNetProdProbeServer(tb testing.TB, network string) socketProbeServer {
	ln := listenProbeNet(tb, network)
	s := &stdNetRawProbeServer{name: "StdNetProd", ln: ln, done: make(chan struct{}), conn: map[net.Conn]struct{}{}}
	s.wg.Add(1)
	go s.acceptProdLoop()
	return s
}

func (s *stdNetRawProbeServer) acceptProdLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conn[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			jconn := jsonrpc2.NewConn(jsonrpc2.NewNDJSONStream(conn))
			jconn.Go(context.Background(), func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
				if req.Method() != "void" {
					return reply(ctx, nil, jsonrpc2.ErrMethodNotFound)
				}
				return reply(ctx, nil, nil)
			})
			<-jconn.Done()
			_ = jconn.Close()
			s.mu.Lock()
			delete(s.conn, conn)
			s.mu.Unlock()
			_ = conn.Close()
		}()
	}
}

type netpollRawProbeServer struct {
	name string
	ln   net.Listener
	loop cloudnetpoll.EventLoop
	done chan error
}

func startNetpollRawProbeServer(tb testing.TB, network string) socketProbeServer {
	ln := listenProbeNet(tb, network)
	onRequest := func(ctx context.Context, conn cloudnetpoll.Connection) error {
		reader := conn.Reader()
		defer reader.Release()
		for reader.Len() > 0 {
			line, err := reader.Until('\n')
			if err != nil {
				_ = conn.Close()
				return nil
			}
			id, ok := parseProbeRequestID(line)
			if !ok {
				_ = conn.Close()
				return nil
			}
			resp := appendProbeResponse(nil, id)
			writer := conn.Writer()
			buf, err := writer.Malloc(len(resp))
			if err != nil {
				_ = conn.Close()
				return nil
			}
			copy(buf, resp)
			if err := writer.Flush(); err != nil {
				_ = conn.Close()
				return nil
			}
		}
		return nil
	}
	loop, err := cloudnetpoll.NewEventLoop(onRequest)
	if err != nil {
		tb.Fatalf("netpoll NewEventLoop: %v", err)
	}
	s := &netpollRawProbeServer{name: "NetpollRaw", ln: ln, loop: loop, done: make(chan error, 1)}
	go func() { s.done <- loop.Serve(ln) }()
	return s
}

func (s *netpollRawProbeServer) Name() string   { return s.name }
func (s *netpollRawProbeServer) Addr() net.Addr { return s.ln.Addr() }

func (s *netpollRawProbeServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := s.loop.Shutdown(ctx)
	_ = s.ln.Close()
	select {
	case runErr := <-s.done:
		if err == nil && runErr != nil && !isProbeShutdownError(runErr) {
			err = runErr
		}
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		}
	}
	return err
}

type gnetRawProbeServer struct {
	name      string
	network   string
	addr      net.Addr
	protoAddr string
	handler   *gnetProbeHandler
	done      chan error
}

type gnetProbeHandler struct {
	gnet.BuiltinEventEngine
	ready chan gnet.Engine
	eng   gnet.Engine
}

func startGnetRawProbeServer(tb testing.TB, network string) socketProbeServer {
	protoAddr, addr, cleanup := reserveGnetAddr(tb, network)
	tb.Cleanup(cleanup)
	h := &gnetProbeHandler{ready: make(chan gnet.Engine, 1)}
	s := &gnetRawProbeServer{name: "GnetRaw", network: network, addr: addr, protoAddr: protoAddr, handler: h, done: make(chan error, 1)}
	go func() {
		s.done <- gnet.Run(h, protoAddr,
			gnet.WithReuseAddr(true),
			gnet.WithMulticore(false),
			gnet.WithReadBufferCap(probeLineMax*4),
			gnet.WithWriteBufferCap(probeLineMax*4),
			gnet.WithLogPath(filepath.Join(tb.TempDir(), "gnet.log")),
			gnet.WithLogLevel(logging.WarnLevel),
		)
	}()
	select {
	case eng := <-h.ready:
		h.eng = eng
	case <-time.After(3 * time.Second):
		tb.Fatalf("gnet OnBoot timed out for %s", protoAddr)
	}
	return s
}

func (s *gnetRawProbeServer) Name() string   { return s.name }
func (s *gnetRawProbeServer) Addr() net.Addr { return s.addr }

func (s *gnetRawProbeServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var err error
	if s.handler.eng.Validate() == nil {
		err = s.handler.eng.Stop(ctx)
	} else {
		err = gnet.Stop(ctx, s.protoAddr)
	}
	select {
	case runErr := <-s.done:
		if err == nil && runErr != nil && !isProbeShutdownError(runErr) {
			err = runErr
		}
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		}
	}
	return err
}

func (h *gnetProbeHandler) OnBoot(eng gnet.Engine) gnet.Action {
	h.eng = eng
	h.ready <- eng
	return gnet.None
}

func (h *gnetProbeHandler) OnTraffic(c gnet.Conn) gnet.Action {
	for c.InboundBuffered() > 0 {
		buf, err := c.Peek(-1)
		if err != nil || len(buf) == 0 {
			return gnet.None
		}
		newline := bytes.IndexByte(buf, '\n')
		if newline < 0 {
			if len(buf) > probeLineMax {
				return gnet.Close
			}
			return gnet.None
		}
		line := buf[:newline+1]
		id, ok := parseProbeRequestID(line)
		if !ok {
			return gnet.Close
		}
		if _, err := c.Discard(newline + 1); err != nil {
			return gnet.Close
		}
		if _, err := c.Write(appendProbeResponse(nil, id)); err != nil {
			return gnet.Close
		}
		if err := c.Flush(); err != nil {
			return gnet.Close
		}
	}
	return gnet.None
}

func reserveGnetAddr(tb testing.TB, network string) (protoAddr string, addr net.Addr, cleanup func()) {
	tb.Helper()
	switch network {
	case "tcp":
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			tb.Fatalf("reserve tcp: %v", err)
		}
		addr = ln.Addr()
		protoAddr = "tcp://" + addr.String()
		_ = ln.Close()
		return protoAddr, addr, func() {}
	case "unix":
		dir, err := os.MkdirTemp("/tmp", "jrpc2-gnetprobe-")
		if err != nil {
			tb.Fatalf("mkdtemp: %v", err)
		}
		path := filepath.Join(dir, "p.sock")
		return "unix://" + path, unixAddr(path), func() { _ = os.RemoveAll(dir) }
	default:
		tb.Fatalf("unsupported gnet network %q", network)
		panic("unreachable")
	}
}

type unixAddr string

func (a unixAddr) Network() string { return "unix" }
func (a unixAddr) String() string  { return string(a) }

func init() {
	// Keep the probe runner deterministic enough for comparisons while avoiding a
	// tiny GOMAXPROCS=1 event-loop configuration in constrained environments.
	if runtime.GOMAXPROCS(0) < 2 {
		runtime.GOMAXPROCS(2)
	}
}
