// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"io"
	"sync"

	"github.com/francoispqt/gojay"
)

// Stream abstracts the transport mechanics from the JSON RPC protocol.
type Stream interface {
	// Read gets the next message from the stream.
	Read(context.Context) ([]byte, error)
	// Write sends a message to the stream.
	Write(context.Context, []byte) error
}

func NewStream(in io.Reader, out io.Writer) Stream {
	return &stream{
		in:  gojay.BorrowDecoder(in),
		out: out,
	}
}

type stream struct {
	in *gojay.Decoder
	sync.Mutex
	out io.Writer
}

func (s *stream) Read(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	defer s.in.Release()

	var data []byte
	if err := s.in.Decode(&data); err != nil {
		return nil, err
	}

	return data, nil
}

func (s *stream) Write(ctx context.Context, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	s.Lock()
	_, err := s.out.Write(data)
	s.Unlock()

	return err
}
