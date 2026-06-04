// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2_test

import (
	"os"
	"path/filepath"
	"testing"
)

// unixSocketAddr returns a short Unix-domain socket path. Darwin rejects long
// sockaddr_un paths with EINVAL; t.TempDir paths include the full test name and
// can exceed that limit when this repository is checked out under nested worktrees.
func unixSocketAddr(t *testing.T, name string) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "jsonrpc2-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("RemoveAll(%q): %v", dir, err)
		}
	})

	return filepath.Join(dir, name)
}
