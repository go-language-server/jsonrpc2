// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/xerrors"
)

// Stream abstracts the transport mechanics from the JSON RPC protocol.
type Stream interface {
	// Read gets the next message from the stream.
	Read(ctx context.Context, p []byte) (n int, err error)

	// Write sends a message to the stream.
	Write(ctx context.Context, p []byte) (n int, err error)
}

type stream struct {
	in  *bufio.Reader
	out io.Writer
	sync.Mutex
}

func NewStream(in io.Reader, out io.Writer) Stream {
	return &stream{
		in:  bufio.NewReader(in),
		out: out,
	}
}

func (s *stream) Read(ctx context.Context, p []byte) (n int, err error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	var length int64
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			return 0, xerrors.Errorf("failed reading header line: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" { // check we have a header line
			break
		}

		colon := strings.IndexRune(line, ':')
		if colon < 0 {
			return 0, xerrors.Errorf("invalid header line: %q", line)
		}

		name, value := line[:colon], strings.TrimSpace(line[colon+1:])
		if name != "Content-Length" {
			continue
		}

		if length, err = strconv.ParseInt(value, 10, 32); err != nil {
			return 0, xerrors.Errorf("failed parsing Content-Length: %v", value)
		}

		if length <= 0 {
			return 0, xerrors.Errorf("invalid Content-Length: %v", length)
		}
	}

	if length == 0 {
		return 0, xerrors.New("missing Content-Length header")
	}

	p = make([]byte, length)
	n, err = io.ReadFull(s.in, p)
	if err != nil {
		return 0, xerrors.Errorf("failed reading data: %w", err)
	}

	return n, nil
}

func (s *stream) Write(ctx context.Context, p []byte) (n int, err error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	s.Lock()
	n, err = fmt.Fprintf(s.out, "Content-Length: %v\r\n\r\n", len(p))
	if err == nil {
		n, err = s.out.Write(p)
	}
	s.Unlock()

	return n, err
}
