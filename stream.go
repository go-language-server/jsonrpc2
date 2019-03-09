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
	Read(context.Context) ([]byte, error)
	// Write sends a message to the stream.
	Write(context.Context, []byte) error
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

func (s *stream) Read(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	var length int64
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			return nil, xerrors.Errorf("failed reading header line: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" { // check we have a header line
			break
		}

		colon := strings.IndexRune(line, ':')
		if colon < 0 {
			return nil, xerrors.Errorf("invalid header line: %q", line)
		}

		name, value := line[:colon], strings.TrimSpace(line[colon+1:])
		switch name {
		case "Content-Length":
			if length, err = strconv.ParseInt(value, 10, 32); err != nil {
				return nil, xerrors.Errorf("failed parsing Content-Length: %v", value)
			}

			if length <= 0 {
				return nil, xerrors.Errorf("invalid Content-Length: %v", length)
			}
		default:
			// ignoring unknown headers
		}
	}

	if length == 0 {
		return nil, xerrors.New("missing Content-Length header")
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(s.in, data); err != nil {
		return nil, xerrors.Errorf("failed reading data: %w", err)
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
	_, err := fmt.Fprintf(s.out, "Content-Length: %v\r\n\r\n", len(data))
	if err == nil {
		_, err = s.out.Write(data)
	}
	s.Unlock()

	return err
}
