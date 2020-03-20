// Copyright 2019 The Go Language Server Authors. All rights reserved.
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
)

const (
	// ContentTypeJSONRPC is the custom mime type content for the Language Server Protocol.
	ContentTypeJSONRPC = "application/jsonrpc; charset=utf-8"

	// ContentTypeVSCodeJSONRPC is the default mime type content for the Language Server Protocol Specification.
	ContentTypeVSCodeJSONRPC = "application/vscode-jsonrpc; charset=utf-8"
)

const (
	// HeaderContentLength is the HTTP header name of the length of the content part in bytes. This header is required.
	// This entity header indicates the size of the entity-body, in bytes, sent to the recipient.
	//
	// RFC 7230, section 3.3.2: Content-Length:
	//  https://tools.ietf.org/html/rfc7230#section-3.3.2
	HeaderContentLength = "Content-Length"

	// HeaderContentType is the mime type of the content part. Defaults to "application/vscode-jsonrpc; charset=utf-8".
	// This entity header is used to indicate the media type of the resource.
	//
	// RFC 7231, section 3.1.1.5: Content-Type:
	//  https://tools.ietf.org/html/rfc7231#section-3.1.1.5
	HeaderContentType = "Content-Type"

	// HeaderContentSeparator is the header and content part separator.
	HeaderContentSeparator = "\r\n\r\n"
)

// Stream abstracts the transport mechanics from the JSON-RPC protocol.
type Stream interface {
	// Read gets the next message from the stream.
	Read(ctx context.Context) (data []byte, n int64, err error)

	// Write sends a message to the stream.
	Write(ctx context.Context, data []byte) (n int64, err error)
}

type stream struct {
	in    *bufio.Reader
	out   io.Writer
	outMu sync.Mutex
}

// compile time check whether the stream implements Stream interface.
var _ Stream = (*stream)(nil)

// NewStream returns a Stream built on top of an io.Reader and io.Writer
// The messages are sent with HTTP content length and MIME type headers.
// This is the format used by LSP and others.
func NewStream(in io.Reader, out io.Writer) Stream {
	return &stream{
		in:  bufio.NewReader(in),
		out: out,
	}
}

// Read reads data from stream.
func (s *stream) Read(ctx context.Context) (data []byte, n int64, err error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}

	var length int64
	// read the header, stop on the first empty line
	for {
		line, err := s.in.ReadString('\n')
		n += int64(len(line))
		if err != nil {
			return nil, n, fmt.Errorf("failed reading header line %q", err)
		}

		line = strings.TrimSpace(line)
		if line == "" { // check we have a header line
			break
		}

		colon := strings.IndexRune(line, ':')
		if colon < 0 {
			return nil, n, fmt.Errorf("invalid header line %q", line)
		}

		name, value := line[:colon], strings.TrimSpace(line[colon+1:])
		switch name {
		case HeaderContentLength:
			if length, err = strconv.ParseInt(value, 10, 32); err != nil {
				return nil, n, fmt.Errorf("failed to parsing Content-Length: %v", value)
			}
			if length <= 0 {
				return nil, n, fmt.Errorf("invalid Content-Length: %v", length)
			}
		default:
			// ignoring unknown headers
		}
	}
	if length == 0 {
		return nil, n, fmt.Errorf("missing %s header", HeaderContentLength)
	}

	data = make([]byte, length)
	if _, err := io.ReadFull(s.in, data); err != nil {
		return nil, n, err
	}
	n += length

	return data, n, nil
}

// Write writes data to stream.
func (s *stream) Write(ctx context.Context, data []byte) (total int64, err error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	s.outMu.Lock()
	var n int
	msg := HeaderContentLength + ": " + strconv.FormatInt(int64(len(data)), 10) + HeaderContentSeparator
	n, err = s.out.Write([]byte(msg))
	total = int64(n)
	if err == nil {
		n, err = s.out.Write(data)
		total += int64(n)
	}
	s.outMu.Unlock()

	return total, err
}
