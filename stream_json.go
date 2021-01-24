// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright 2018 The Go Language Server Authors

// +build !gojay

package jsonrpc2

import (
	"context"
	"fmt"
	"net"

	json "github.com/goccy/go-json"
)

type rawStream struct {
	conn net.Conn
	in   *json.Decoder
}

// NewRawStream returns a Stream built on top of a net.Conn.
// The messages are sent with no wrapping, and rely on json decode consistency
// to determine message boundaries.
func NewRawStream(conn net.Conn) Stream {
	return &rawStream{
		conn: conn,
		in:   json.NewDecoder(conn),
	}
}

func (s *rawStream) Write(ctx context.Context, msg Message) (total int64, err error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	data, err := json.MarshalNoEscape(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %w", err)
	}
	n, err := s.conn.Write(data)
	total = int64(n)
	return
}

func (s *stream) Write(ctx context.Context, msg Message) (total int64, err error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	data, err := json.MarshalNoEscape(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %w", err)
	}
	n, err := fmt.Fprintf(s.conn, "Content-Length: %v\r\n\r\n", len(data))
	total = int64(n)
	if err == nil {
		n, err = s.conn.Write(data)
		total += int64(n)
	}
	return
}
