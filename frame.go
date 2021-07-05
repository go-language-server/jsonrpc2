// SPDX-FileCopyrightText: 2021 The Go Language Server Authors
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"bufio"
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/segmentio/encoding/json"
)

const (
	// HdrContentLength is the HTTP header name of the length of the content part in bytes. This header is required.
	// This entity header indicates the size of the entity-body, in bytes, sent to the recipient.
	//
	// RFC 7230, section 3.3.2: Content-Length:
	//  https://tools.ietf.org/html/rfc7230#section-3.3.2
	HdrContentLength = "Content-Length"

	// HeaderContentType is the mime type of the content part. Defaults to "application/vscode-jsonrpc; charset=utf-8".
	// This entity header is used to indicate the media type of the resource.
	//
	// RFC 7231, section 3.1.1.5: Content-Type:
	//  https://tools.ietf.org/html/rfc7231#section-3.1.1.5
	HdrContentType = "Content-Type"

	// HeaderContentSeparator is the header and content part separator.
	HdrContentSeparator = "\r\n\r\n"
)

// Reader abstracts the transport mechanics from the JSON RPC protocol.
//
// A Conn reads messages from the reader it was provided on construction,
// and assumes that each call to Read fully transfers a single message,
// or returns an error.
//
// A reader is not safe for concurrent use, it is expected it will be used by
// a single Conn in a safe manner.
type Reader interface {
	// Read gets the next message from the stream.
	Read(ctx context.Context) (msg Message, n int64, err error)
}

// Writer abstracts the transport mechanics from the JSON RPC protocol.
//
// A Conn writes messages using the writer it was provided on construction,
// and assumes that each call to Write fully transfers a single message,
// or returns an error.
//
// A writer is not safe for concurrent use, it is expected it will be used by
// a single Conn in a safe manner.
type Writer interface {
	// Write sends a message to the stream.
	Write(ctx context.Context, msg Message) (n int64, err error)
}

// Framer wraps low level byte readers and writers into jsonrpc2 message
// readers and writers.
//
// It is responsible for the framing and encoding of messages into wire form.
type Framer interface {
	// Reader wraps a byte reader into a message reader.
	Reader(r io.Reader) Reader

	// Writer wraps a byte writer into a message writer.
	Writer(w io.Writer) Writer
}

// RawFramer returns a new raw Framer.
//
// The messages are sent with no wrapping, and rely on json decode consistency
// to determine message boundaries.
func RawFramer() Framer {
	return rawFramer{}
}

type rawFramer struct{}

type rawReader struct {
	in *stdjson.Decoder
}

type rawWriter struct {
	out io.Writer
}

// Reader implements Framer.Reader.
func (rawFramer) Reader(r io.Reader) Reader {
	return &rawReader{
		in: stdjson.NewDecoder(r),
	}
}

// Writer implements Framer.Writer.
func (rawFramer) Writer(w io.Writer) Writer {
	return &rawWriter{
		out: w,
	}
}

// Read implements Reader.Read.
func (r *rawReader) Read(ctx context.Context) (msg Message, n int64, err error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}

	var raw json.RawMessage
	if err := r.in.Decode(&raw); err != nil {
		return nil, 0, fmt.Errorf("failed to Decode: %w", err)
	}

	msg, err = DecodeMessage(raw)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to DecodeMessage: %w", err)
	}

	return msg, int64(len(raw)), nil
}

// Write implements Writer.Write.
func (w *rawWriter) Write(ctx context.Context, msg Message) (n int64, err error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	data, err := EncodeMessage(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %w", err)
	}

	total, err := w.out.Write(data)
	if err != nil {
		return 0, fmt.Errorf("failed to write: %w", err)
	}

	return int64(total), nil
}

// HeaderFramer returns a new header Framer.
//
// The messages are sent with HTTP content length and MIME type headers.
// This is the format used by LSP and others.
func HeaderFramer() Framer {
	return headerFramer{}
}

type headerFramer struct{}

type headerReader struct {
	in *bufio.Reader
}

type headerWriter struct {
	out io.Writer
}

// Reader implements Framer.Reader.
func (headerFramer) Reader(r io.Reader) Reader {
	return &headerReader{
		in: bufio.NewReader(r),
	}
}

// Writer implements Framer.Writer.
func (headerFramer) Writer(w io.Writer) Writer {
	return &headerWriter{
		out: w,
	}
}

// Read implements Reader.Read.
func (r *headerReader) Read(ctx context.Context) (msg Message, n int64, err error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}

	var length int64
	// read the header, stop on the first empty line
	for {
		line, err := r.in.ReadString('\n')
		n += int64(len(line))
		if err != nil {
			return nil, n, fmt.Errorf("failed reading header line: %w", err)
		}

		line = strings.TrimSpace(line)
		// check have a header line
		if line == "" {
			break
		}

		colon := strings.IndexRune(line, ':')
		if colon < 0 {
			return nil, n, fmt.Errorf("invalid header line %q", line)
		}

		name, value := line[:colon], strings.TrimSpace(line[colon+1:])
		switch name {
		case HdrContentLength:
			if length, err = strconv.ParseInt(value, 10, 32); err != nil {
				return nil, n, fmt.Errorf("failed parsing Content-Length: %v", value)
			}
			if length <= 0 {
				return nil, n, fmt.Errorf("invalid Content-Length: %v", length)
			}

		default:
			// ignoring unknown headers
		}
	}

	if length == 0 {
		return nil, n, errors.New("missing Content-Length header")
	}

	data := make([]byte, length)
	total, err := io.ReadFull(r.in, data)
	n += int64(total)
	if err != nil {
		return nil, n, fmt.Errorf("failed to ReadFull: %w", err)
	}

	msg, err = DecodeMessage(data)
	if err != nil {
		return nil, n, fmt.Errorf("failed to DecodeMessage: %w", err)
	}

	return msg, n, nil
}

// Write implements Writer.Write.
func (w *headerWriter) Write(ctx context.Context, msg Message) (n int64, err error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	data, err := EncodeMessage(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling message: %w", err)
	}

	total, err := fmt.Fprintf(w.out, "%s: %d%s", HdrContentLength, len(data), HdrContentSeparator)
	n = int64(total)
	if err == nil {
		total, err = w.out.Write(data)
		n += int64(total)
		if err != nil {
			return 0, fmt.Errorf("failed to write: %w", err)
		}
	}

	if err != nil {
		return 0, fmt.Errorf("failed to write Content-Length: %w", err)
	}

	return n, nil
}
