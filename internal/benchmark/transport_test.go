// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package benchmark

import (
	"context"
	"testing"
)

func TestJSONRPC2HeaderAndDirectTransportBatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		make func(ctx context.Context) (*jsonrpc2Adapter, error)
	}{
		{name: "header", make: newJSONRPC2HeaderAdapter},
		{name: "direct", make: newJSONRPC2DirectAdapter},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			c, err := tc.make(ctx)
			if err != nil {
				t.Fatalf("make adapter: %v", err)
			}
			defer c.Close()

			if err := c.Batch(ctx, 4); err != nil {
				t.Fatalf("Batch: %v", err)
			}
		})
	}
}
