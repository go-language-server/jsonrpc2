// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"fmt"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

func TestID_AppendID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		id   ID
		want string
	}{
		"success: positive number": {
			id:   NewNumberID(42),
			want: `42`,
		},
		"success: zero number": {
			id:   NewNumberID(0),
			want: `0`,
		},
		"success: negative number": {
			id:   NewNumberID(-15),
			want: `-15`,
		},
		"success: large number": {
			id:   NewNumberID(9223372036854775807),
			want: `9223372036854775807`,
		},
		"success: plain string": {
			id:   NewStringID("abc"),
			want: `"abc"`,
		},
		"success: empty string": {
			id:   NewStringID(""),
			want: `""`,
		},
		"success: string needing escape": {
			id:   NewStringID("a\"b\\c\n"),
			want: `"a\"b\\c\n"`,
		},
		"success: unset id encodes as null": {
			id:   ID{},
			want: `null`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := string(tt.id.appendID(nil))
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("appendID mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDecodeID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		span    string
		want    ID
		wantOK  bool
		wantNum int64
		isNum   bool
		isStr   bool
	}{
		"success: positive number": {
			span:    `42`,
			want:    NewNumberID(42),
			wantOK:  true,
			wantNum: 42,
			isNum:   true,
		},
		"success: negative number": {
			span:    `-15`,
			want:    NewNumberID(-15),
			wantOK:  true,
			wantNum: -15,
			isNum:   true,
		},
		"success: string": {
			span:   `"hello"`,
			want:   NewStringID("hello"),
			wantOK: true,
			isStr:  true,
		},
		"success: escaped string": {
			span:   `"a\nb"`,
			want:   NewStringID("a\nb"),
			wantOK: true,
			isStr:  true,
		},
		"success: null becomes unset": {
			span:   `null`,
			want:   ID{},
			wantOK: true,
		},
		"error: fractional number": {
			span:   `1.5`,
			wantOK: false,
		},
		"error: object": {
			span:   `{}`,
			wantOK: false,
		},
		"error: empty span": {
			span:   ``,
			wantOK: false,
		},
		"error: bare not-null literal": {
			span:   `nope`,
			wantOK: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := decodeID([]byte(tt.span))
			if ok != tt.wantOK {
				t.Fatalf("decodeID ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if diff := gocmp.Diff(tt.want, got, gocmp.AllowUnexported(ID{})); diff != "" {
				t.Errorf("decodeID mismatch (-want +got):\n%s", diff)
			}
			if tt.isNum {
				n, isNum := got.Number()
				if !isNum || n != tt.wantNum {
					t.Errorf("Number() = (%d, %v), want (%d, true)", n, isNum, tt.wantNum)
				}
			}
			if tt.isStr && !got.IsString() {
				t.Errorf("IsString() = false, want true")
			}
		})
	}
}

func TestID_RoundTrip(t *testing.T) {
	t.Parallel()

	ids := []ID{
		NewNumberID(0),
		NewNumberID(123456789),
		NewNumberID(-987654321),
		NewStringID("simple"),
		NewStringID("with \"quotes\" and \\ slashes\nand newline"),
		{},
	}

	for _, id := range ids {
		encoded := id.appendID(nil)
		got, ok := decodeID(encoded)
		if !ok {
			t.Fatalf("decodeID failed for %q", encoded)
		}
		if diff := gocmp.Diff(id, got, gocmp.AllowUnexported(ID{})); diff != "" {
			t.Errorf("round-trip mismatch for %q (-want +got):\n%s", encoded, diff)
		}
	}
}

func TestID_Format(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		id   ID
		verb string
		want string
	}{
		"success: number with %s": {id: NewNumberID(7), verb: "%s", want: "7"},
		"success: number with %q": {id: NewNumberID(7), verb: "%q", want: "#7"},
		"success: string with %s": {id: NewStringID("x"), verb: "%s", want: "x"},
		"success: string with %q": {id: NewStringID("x"), verb: "%q", want: `"x"`},
		"success: unset with %s":  {id: ID{}, verb: "%s", want: "null"},
		"success: unset with %q":  {id: ID{}, verb: "%q", want: "null"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := fmt.Sprintf(tt.verb, tt.id)
			if diff := gocmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Format mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestID_Validity(t *testing.T) {
	t.Parallel()

	if (ID{}).IsValid() {
		t.Error("zero ID should not be valid")
	}
	if !NewNumberID(1).IsValid() {
		t.Error("number ID should be valid")
	}
	if !NewStringID("a").IsValid() {
		t.Error("string ID should be valid")
	}
	if !NewNumberID(1).IsNumber() {
		t.Error("number ID should report IsNumber")
	}
	if !NewStringID("a").IsString() {
		t.Error("string ID should report IsString")
	}
	if s, ok := NewStringID("a").StringValue(); !ok || s != "a" {
		t.Errorf("StringValue() = (%q, %v), want (\"a\", true)", s, ok)
	}
}
