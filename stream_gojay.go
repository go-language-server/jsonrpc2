// Copyright 2018 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

// +build gojay

package jsonrpc2

import (
	"context"
	"fmt"
	"net"

	"github.com/francoispqt/gojay"
)

type rawStream struct {
	conn net.Conn
	in   *gojay.Decoder
}

// NewRawStream returns a Stream built on top of a net.Conn.
// The messages are sent with no wrapping, and rely on json decode consistency
// to determine message boundaries.
func NewRawStream(conn net.Conn) Stream {
	return &rawStream{
		conn: conn,
		in:   gojay.NewDecoder(conn),
	}
}

func (s *rawStream) Write(ctx context.Context, msg Message) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	data, err := gojay.MarshalAny(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %v", err)
	}
	n, err := s.conn.Write(data)
	return int64(n), err
}

func (s *headerStream) Write(ctx context.Context, msg Message) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	data, err := gojay.MarshalAny(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %v", err)
	}
	n, err := fmt.Fprintf(s.conn, "Content-Length: %v\r\n\r\n", len(data))
	total := int64(n)
	if err == nil {
		n, err = s.conn.Write(data)
		total += int64(n)
	}
	return total, err
}
