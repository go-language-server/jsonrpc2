// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"bytes"
	stdjson "encoding/json"
	"fmt"
	"io"
	"net"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"
	segjson "github.com/segmentio/encoding/json"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/jsonrpc2/codec/goccy"
	"go.lsp.dev/jsonrpc2/codec/sonic"
)

var probeBytes []byte
var probeMessage jsonrpc2.Message

type probeEnvelope struct {
	JSONRPC string             `json:"jsonrpc"`
	Method  string             `json:"method,omitempty"`
	Params  stdjson.RawMessage `json:"params,omitempty"`
	ID      int                `json:"id,omitempty"`
}

// BenchmarkCodecDependencyProbe keeps the opt-in codec dependencies on an
// explicit benchmark-only surface. The root module stays dependency-light; this
// nested benchmark module can compare payload codec costs before deciding
// whether a hot path should route payloads through a different Codec.
func BenchmarkCodecDependencyProbe(b *testing.B) {
	payload := struct {
		Method string         `json:"method"`
		ID     int            `json:"id"`
		Params map[string]any `json:"params"`
	}{
		Method: "textDocument/hover",
		ID:     99,
		Params: map[string]any{
			"textDocument": map[string]string{"uri": "file:///tmp/example.go"},
			"position":     map[string]int{"line": 123, "character": 45},
		},
	}
	codecs := []struct {
		name  string
		codec jsonrpc2.Codec
	}{
		{"default-jsonv2", jsonrpc2.JSONCodec{}},
		{"goccy", goccy.Codec{}},
		{"sonic", sonic.Codec{}},
	}
	for _, c := range codecs {
		b.Run(c.name+"/marshal", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				data, err := c.codec.Marshal(payload)
				if err != nil {
					b.Fatalf("Marshal: %v", err)
				}
				probeBytes = data
			}
		})

		data, err := c.codec.Marshal(payload)
		if err != nil {
			b.Fatalf("%s Marshal setup: %v", c.name, err)
		}
		b.Run(c.name+"/unmarshal", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var dst struct {
					Method string         `json:"method"`
					ID     int            `json:"id"`
					Params map[string]any `json:"params"`
				}
				if err := c.codec.Unmarshal(data, &dst); err != nil {
					b.Fatalf("Unmarshal: %v", err)
				}
			}
		})
	}
}

// BenchmarkEnvelopeDependencyProbe keeps JSON envelope dependency experiments
// separate from the production encoder/decoder. It compares the current
// hand-written envelope path with generic dependency-backed marshal/unmarshal
// paths without adding those dependencies to the root module.
func BenchmarkEnvelopeDependencyProbe(b *testing.B) {
	rawParams := stdjson.RawMessage(`{"textDocument":{"uri":"file:///tmp/example.go"},"position":{"line":123,"character":45}}`)
	params := jsonrpc2.RawMessage(rawParams)
	frame := []byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"textDocument":{"uri":"file:///tmp/example.go"},"position":{"line":123,"character":45}},"id":99}`)
	envelope := probeEnvelope{
		JSONRPC: "2.0",
		Method:  "textDocument/hover",
		Params:  rawParams,
		ID:      99,
	}
	msg := jsonrpc2.NewCall(jsonrpc2.NewNumberID(99), "textDocument/hover", params)

	b.Run("encode/hand", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			data, err := jsonrpc2.EncodeMessage(msg)
			if err != nil {
				b.Fatalf("EncodeMessage: %v", err)
			}
			probeBytes = data
		}
	})
	b.Run("encode/jsonv2", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			data, err := jsonv2.Marshal(envelope)
			if err != nil {
				b.Fatalf("jsonv2.Marshal: %v", err)
			}
			probeBytes = data
		}
	})
	b.Run("encode/segmentio", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			data, err := segjson.Marshal(envelope)
			if err != nil {
				b.Fatalf("segmentio.Marshal: %v", err)
			}
			probeBytes = data
		}
	})

	b.Run("decode/hand", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			msg, err := jsonrpc2.DecodeMessage(frame)
			if err != nil {
				b.Fatalf("DecodeMessage: %v", err)
			}
			probeMessage = msg
		}
	})
	b.Run("decode/jsonv2", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var dst probeEnvelope
			if err := jsonv2.Unmarshal(frame, &dst); err != nil {
				b.Fatalf("jsonv2.Unmarshal: %v", err)
			}
		}
	})
	b.Run("decode/segmentio", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var dst probeEnvelope
			if err := segjson.Unmarshal(frame, &dst); err != nil {
				b.Fatalf("segmentio.Unmarshal: %v", err)
			}
		}
	})
}

// BenchmarkWritevFrameProbe compares the current single composed write with a
// benchmark-only net.Buffers path that can use writev on real net.Conn types.
// It is intentionally a probe: it does not change the production framers, and
// its transport rows separate in-memory net.Pipe from real Unix/TCP sockets.
func BenchmarkWritevFrameProbe(b *testing.B) {
	body := bytes.Repeat([]byte(`{"jsonrpc":"2.0","method":"textDocument/hover","params":{"x":"y"},"id":99}`), 16)
	header := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body)))
	composed := make([]byte, 0, len(header)+len(body))
	composed = append(composed, header...)
	composed = append(composed, body...)

	transports := []struct {
		name string
		pair func(*testing.B) (net.Conn, net.Conn)
	}{
		{"netpipe", func(*testing.B) (net.Conn, net.Conn) { return net.Pipe() }},
		{"unix", func(b *testing.B) (net.Conn, net.Conn) { return dialTransportPair(b, unixListener(b)) }},
		{"tcp", func(b *testing.B) (net.Conn, net.Conn) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				b.Fatalf("listen tcp: %v", err)
			}
			b.Cleanup(func() { _ = ln.Close() })
			return dialTransportPair(b, ln)
		}},
	}
	writers := []struct {
		name  string
		write func(net.Conn) error
	}{
		{"composed-write", func(c net.Conn) error {
			_, err := c.Write(composed)
			return err
		}},
		{"net-buffers", func(c net.Conn) error {
			bufs := net.Buffers{header, body}
			_, err := bufs.WriteTo(c)
			return err
		}},
	}

	for _, tr := range transports {
		for _, wr := range writers {
			b.Run(tr.name+"/"+wr.name, func(b *testing.B) {
				client, server := tr.pair(b)
				done := make(chan struct{})
				go func() {
					_, _ = io.Copy(io.Discard, server)
					_ = server.Close()
					close(done)
				}()
				b.Cleanup(func() {
					_ = client.Close()
					<-done
				})

				b.SetBytes(int64(len(composed)))
				b.ReportAllocs()
				for b.Loop() {
					if err := wr.write(client); err != nil {
						b.Fatalf("write: %v", err)
					}
				}
			})
		}
	}
}
