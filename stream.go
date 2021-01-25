// Copyright 2018 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// Stream abstracts the transport mechanics from the JSON RPC protocol.
// A Conn reads and writes messages using the stream it was provided on
// construction, and assumes that each call to Read or Write fully transfers
// a single message, or returns an error.
// A stream is not safe for concurrent use, it is expected it will be used by
// a single Conn in a safe manner.
type Stream interface {
	// Read gets the next message from the stream.
	Read(context.Context) (Message, int64, error)
	// Write sends a message to the stream.
	Write(context.Context, Message) (int64, error)
	// Close closes the connection.
	// Any blocked Read or Write operations will be unblocked and return errors.
	Close() error
}

// Framer wraps a network connection up into a Stream.
// It is responsible for the framing and encoding of messages into wire form.
// NewRawStream and NewHeaderStream are implementations of a Framer.
type Framer func(conn net.Conn) Stream

func (s *rawStream) Read(ctx context.Context) (Message, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}
	var raw RawMessage
	if err := s.in.Decode(&raw); err != nil {
		return nil, 0, fmt.Errorf("failed to Decode: %w", err)
	}
	msg, err := DecodeMessage(raw)
	return msg, int64(len(raw)), fmt.Errorf("failed to DecodeMessage: %w", err)
}

func (s *rawStream) Close() error {
	return s.conn.Close()
}

// NewHeaderStream returns a Stream built on top of a net.Conn.
// The messages are sent with HTTP content length and MIME type headers.
// This is the format used by LSP and others.
func NewHeaderStream(conn net.Conn) Stream {
	return &headerStream{
		conn: conn,
		in:   bufio.NewReader(conn),
	}
}

type headerStream struct {
	conn net.Conn
	in   *bufio.Reader
}

func (s *headerStream) Read(ctx context.Context) (Message, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}
	var total, length int64
	// read the header, stop on the first empty line
	for {
		line, err := s.in.ReadString('\n')
		total += int64(len(line))
		if err != nil {
			return nil, total, fmt.Errorf("failed reading header line: %w", err)
		}
		line = strings.TrimSpace(line)
		// check we have a header line
		if line == "" {
			break
		}
		colon := strings.IndexRune(line, ':')
		if colon < 0 {
			return nil, total, fmt.Errorf("invalid header line %q", line)
		}
		name, value := line[:colon], strings.TrimSpace(line[colon+1:])
		switch name {
		case "Content-Length":
			if length, err = strconv.ParseInt(value, 10, 32); err != nil {
				return nil, total, fmt.Errorf("failed parsing Content-Length: %v", value)
			}
			if length <= 0 {
				return nil, total, fmt.Errorf("invalid Content-Length: %v", length)
			}
		default:
			// ignoring unknown headers
		}
	}
	if length == 0 {
		return nil, total, fmt.Errorf("missing Content-Length header")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(s.in, data); err != nil {
		return nil, total, fmt.Errorf("failed to ReadFull: %w", err)
	}
	total += length
	msg, err := DecodeMessage(data)
	return msg, total, err
}

func (s *headerStream) Close() error {
	return s.conn.Close()
}
