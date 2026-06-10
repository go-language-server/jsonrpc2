// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"context"
	"io"
	"sync"
)

// NewChannelStreamPair returns two connected in-memory streams backed by
// bounded channels of encoded JSON-RPC frames.
//
// The capacity controls the number of frames each direction can buffer before a
// writer blocks. A capacity of zero gives rendezvous semantics. Negative
// capacities panic, matching make(chan T, capacity).
//
// This stream pair is for same-process peers that need an in-memory JSON-RPC
// transport; it is not a replacement for network or stdio transports.
//
// Frames sent with WriteFrame are copied before they are queued, so the caller
// may reuse or mutate its buffer immediately after WriteFrame returns. Frames
// returned by ReadFrame are owned by the receiving stream. Closing either stream
// closes the pair, makes later reads and writes fail with io.EOF, and unblocks
// pending reads and writes with io.EOF.
func NewChannelStreamPair(capacity int) (left, right Stream) {
	if capacity < 0 {
		panic("jsonrpc2: negative channel stream capacity")
	}
	p := &channelStreamPair{
		done: make(chan struct{}),
		aToB: make(chan []byte, capacity),
		bToA: make(chan []byte, capacity),
	}
	return &channelStream{in: p.bToA, out: p.aToB, pair: p}, &channelStream{in: p.aToB, out: p.bToA, pair: p}
}

type channelStreamPair struct {
	done chan struct{}
	once sync.Once
	aToB chan []byte
	bToA chan []byte
}

type channelStream struct {
	in   <-chan []byte
	out  chan<- []byte
	pair *channelStreamPair

	writeMu sync.Mutex
	wbuf    []byte
}

func (s *channelStream) Read(ctx context.Context) (Message, int64, error) {
	frame, n, err := s.ReadFrame(ctx)
	if err != nil {
		return nil, 0, err
	}
	msg, derr := DecodeMessage(frame)
	if derr != nil {
		return nil, 0, derr
	}
	return msg, n, nil
}

func (s *channelStream) Write(ctx context.Context, msg Message) (int64, error) {
	frame, err := EncodeMessage(msg)
	if err != nil {
		return 0, err
	}
	return s.sendFrame(ctx, frame)
}

func (s *channelStream) ReadFrame(ctx context.Context) (frame []byte, n int64, err error) {
	if err := s.closedOrCanceled(ctx); err != nil {
		return nil, 0, err
	}
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-s.pair.done:
		return nil, 0, io.EOF
	case frame := <-s.in:
		return frame, int64(len(frame)), nil
	}
}

func (s *channelStream) WriteFrame(ctx context.Context, data []byte) (int64, error) {
	return s.sendFrame(ctx, cloneBytes(data))
}

func (s *channelStream) writeCall(ctx context.Context, id ID, method string, params RawMessage) (int64, error) {
	return s.composeAndSend(ctx, func(buf []byte) []byte {
		return appendCallFields(buf, id, method, params)
	})
}

func (s *channelStream) writeNotification(ctx context.Context, method string, params RawMessage) (int64, error) {
	return s.composeAndSend(ctx, func(buf []byte) []byte {
		return appendNotificationFields(buf, method, params)
	})
}

func (s *channelStream) writeResponse(ctx context.Context, id ID, result RawMessage, err error) (int64, error) {
	return s.composeAndSend(ctx, func(buf []byte) []byte {
		return appendResponseFields(buf, id, result, err)
	})
}

func (s *channelStream) composeAndSend(ctx context.Context, appendFrame func([]byte) []byte) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.writeMu.Lock()
	frame := appendFrame(s.wbuf[:0])
	queued := cloneBytes(frame)
	s.wbuf = frame
	s.writeMu.Unlock()
	return s.sendFrame(ctx, queued)
}

func (s *channelStream) sendFrame(ctx context.Context, frame []byte) (int64, error) {
	if err := s.closedOrCanceled(ctx); err != nil {
		return 0, err
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.pair.done:
		return 0, io.EOF
	case s.out <- frame:
		return int64(len(frame)), nil
	}
}

func (s *channelStream) closedOrCanceled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.pair.done:
		return io.EOF
	default:
		return nil
	}
}

func (s *channelStream) Close() error {
	s.pair.once.Do(func() { close(s.pair.done) })
	return nil
}
