// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"context"
	"io"
	"sync"

	"go.lsp.dev/jsonrpc2"
)

// directStream is a tiny in-memory transport used only by the benchmark module.
// It bypasses net.Pipe and moves raw framed byte slices through paired channels
// while still implementing the jsonrpc2.Stream and frame-stream surfaces.
//
// The transport is intentionally minimal: it preserves backpressure by using
// unbuffered channels, and Close unblocks both directions by closing a shared
// done channel.
type directStream struct {
	in   <-chan []byte
	out  chan<- []byte
	pair *directPair
}

type directPair struct {
	done chan struct{}
	once sync.Once
	aToB chan []byte
	bToA chan []byte
}

// newDirectStreamPair returns two connected jsonrpc2.Streams backed by the
// benchmark-local direct transport.
func newDirectStreamPair() (jsonrpc2.Stream, jsonrpc2.Stream) {
	p := &directPair{
		done: make(chan struct{}),
		aToB: make(chan []byte),
		bToA: make(chan []byte),
	}
	return &directStream{in: p.bToA, out: p.aToB, pair: p}, &directStream{in: p.aToB, out: p.bToA, pair: p}
}

func (s *directStream) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	body, n, err := s.ReadFrame(ctx)
	if err != nil {
		return nil, 0, err
	}
	msg, derr := jsonrpc2.DecodeMessage(body)
	if derr != nil {
		return nil, 0, derr
	}
	return msg, n, nil
}

func (s *directStream) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, err
	}
	n, werr := s.WriteFrame(ctx, data)
	if werr != nil {
		return 0, werr
	}
	return n, nil
}

func (s *directStream) ReadFrame(ctx context.Context) ([]byte, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-s.pair.done:
		return nil, 0, io.EOF
	case body := <-s.in:
		return body, int64(len(body)), nil
	}
}

func (s *directStream) WriteFrame(ctx context.Context, data []byte) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.pair.done:
		return 0, io.EOF
	case s.out <- data:
		return int64(len(data)), nil
	}
}

func (s *directStream) Close() error {
	s.pair.once.Do(func() { close(s.pair.done) })
	return nil
}
