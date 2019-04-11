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

const (
	// "Header Content-Length" is the HTTP header name of the length of the content part in bytes. This header is required.
	// This entity header indicates the size of the entity-body, in bytes, sent to the recipient.
	//
	// RFC 7230, section 3.3.2: Content-Length:
	//  https://tools.ietf.org/html/rfc7230#section-3.3.2
	HeaderContentLength = "Content-Length"

	// "HeaderContentType" is the mime type of the content part. Defaults to "application/vscode-jsonrpc; charset=utf-8".
	// This entity header is used to indicate the media type of the resource.
	//
	// RFC 7231, section 3.1.1.5: Content-Type:
	//  https://tools.ietf.org/html/rfc7231#section-3.1.1.5
	HeaderContentType = "Content-Type"

	// HeaderContentSeparator is the header and content part separator.
	HeaderContentSeparator = "\r\n"

	headerSeparatorComma = ":"
)

const (
	// ContentTypeJSONRPC is the custom mime type content for the Language Server Protocol.
	ContentTypeJSONRPC = "application/jsonrpc; charset=utf-8"

	// ContentTypeVSCodeJSONRPC is the default mime type content for the Language Server Protocol Specification.
	ContentTypeVSCodeJSONRPC = "application/vscode-jsonrpc; charset=utf-8"
)

const (
	// HeaderContentLengthFmt is the a format of "Content-Length" header for fmt function arg.
	HeaderContentLengthFmt = HeaderContentLength + headerSeparatorComma + " %d" + HeaderContentSeparator
	// HeaderContentTypeFmt is the a format of "Content-Type" header for fmt function arg.
	HeaderContentTypeFmt = HeaderContentType + headerSeparatorComma + " %s" + HeaderContentSeparator
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
	n, err = fmt.Fprintf(s.out, HeaderContentLengthFmt+HeaderContentTypeFmt+HeaderContentSeparator, len(p), ContentTypeJSONRPC)
	if err == nil {
		n, err = s.out.Write(p)
	}
	s.Unlock()

	return n, err
}
