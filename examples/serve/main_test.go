// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var out bytes.Buffer
	if err := run(ctx, &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	const want = "symbol: jsonrpc2.Conn (interface)\n"
	if got := out.String(); got != want {
		t.Fatalf("output mismatch:\n got: %q\nwant: %q", got, want)
	}
}
