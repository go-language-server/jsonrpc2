// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestHeaderContentFmt(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "ContentLength",
			header: fmt.Sprintf(HeaderContentLengthFmt, 512),
			want:   "Content-Length: 512\r\n",
		},
		{
			name:   "ContentType",
			header: fmt.Sprintf(HeaderContentTypeFmt, ContentTypeJSONRPC),
			want:   "Content-Type: application/jsonrpc; charset=utf-8\r\n",
		},
		{
			name:   "Both",
			header: fmt.Sprintf(HeaderContentLengthFmt+HeaderContentTypeFmt, 512, ContentTypeVSCodeJSONRPC),
			want:   "Content-Length: 512\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.header, tt.want); diff != "" {
				t.Errorf("%s: (-got, +want)\n%s", tt.name, diff)
			}
		})
	}
}
