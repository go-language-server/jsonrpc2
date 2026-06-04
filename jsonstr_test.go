// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

func TestUnquoteJSONString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		span   string
		want   string
		wantOK bool
	}{
		"success: plain ascii": {
			span:   `"hello"`,
			want:   "hello",
			wantOK: true,
		},
		"success: empty": {
			span:   `""`,
			want:   "",
			wantOK: true,
		},
		"success: escaped quote and backslash": {
			span:   `"a\"b\\c"`,
			want:   `a"b\c`,
			wantOK: true,
		},
		"success: control escapes": {
			span:   `"\b\f\n\r\t"`,
			want:   "\b\f\n\r\t",
			wantOK: true,
		},
		"success: forward slash escape": {
			span:   `"a\/b"`,
			want:   "a/b",
			wantOK: true,
		},
		"success: basic unicode escape": {
			span:   `"Aé"`,
			want:   "Aé",
			wantOK: true,
		},
		"success: surrogate pair": {
			span:   `"𝄞"`,
			want:   "\U0001D11E",
			wantOK: true,
		},
		"success: unpaired high surrogate becomes replacement": {
			span:   `"\uD834"`,
			want:   "�",
			wantOK: true,
		},
		"success: unpaired low surrogate becomes replacement": {
			span:   `"\uDD1E"`,
			want:   "�",
			wantOK: true,
		},
		"error: not a quoted string": {
			span:   `abc`,
			wantOK: false,
		},
		"error: dangling backslash": {
			span:   `"abc\`,
			wantOK: false,
		},
		"error: invalid escape": {
			span:   `"a\xb"`,
			wantOK: false,
		},
		"error: short unicode escape": {
			span:   `"\u12"`,
			wantOK: false,
		},
		"error: non-hex unicode digit": {
			span:   `"\u12zz"`,
			wantOK: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := unquoteJSONString([]byte(tt.span))
			if ok != tt.wantOK {
				t.Fatalf("unquoteJSONString(%q) ok = %v, want %v", tt.span, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("unquoteJSONString(%q) mismatch (-want +got):\n%s", tt.span, diff)
			}
		})
	}
}

func TestAppendQuotedString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		want string
	}{
		"success: no escape needed": {
			in:   "hello",
			want: `"hello"`,
		},
		"success: html chars not escaped": {
			in:   "<a> & </a>",
			want: `"<a> & </a>"`,
		},
		"success: quote and backslash": {
			in:   `a"b\c`,
			want: `"a\"b\\c"`,
		},
		"success: control characters": {
			in:   "\n\r\t",
			want: `"\n\r\t"`,
		},
		"success: low control to u form": {
			in:   "\x01",
			want: "\"\\u0001\"",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := string(appendQuotedString(nil, tt.in))
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("appendQuotedString(%q) mismatch (-want +got):\n%s", tt.in, diff)
			}

			rt, ok := unquoteJSONString([]byte(got))
			if !ok {
				t.Fatalf("unquoteJSONString(%q) failed", got)
			}
			if rt != tt.in {
				t.Errorf("round-trip = %q, want %q", rt, tt.in)
			}
		})
	}
}

func TestErrIdleTimeout(t *testing.T) {
	t.Parallel()

	if got := ErrIdleTimeout.Error(); got != "timed out waiting for new connections" {
		t.Errorf("ErrIdleTimeout.Error() = %q", got)
	}
}
