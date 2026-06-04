// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import "unsafe"

// MethodID is an interned method token. MethodUnknown means the method is not in
// the table used for scanning or lookup.
type MethodID uint32

const (
	MethodUnknown MethodID = 0
)

// MethodTable maps known method names to compact IDs for allocation-free hot
// dispatch. IDs are assigned from 1 in the order the names are first provided.
type MethodTable struct {
	byName map[string]MethodID
	names  []string
}

// NewMethodTable constructs a method table. Duplicate names keep the ID of
// their first occurrence.
func NewMethodTable(names ...string) *MethodTable {
	t := &MethodTable{
		byName: make(map[string]MethodID, len(names)),
		names:  make([]string, 0, len(names)),
	}
	for _, name := range names {
		if _, exists := t.byName[name]; exists {
			continue
		}
		id := MethodID(len(t.names) + 1)
		t.byName[name] = id
		t.names = append(t.names, name)
	}
	return t
}

// Lookup returns the ID for method, or MethodUnknown when method is not known.
func (t *MethodTable) Lookup(method []byte) MethodID {
	if t == nil || len(t.byName) == 0 {
		return MethodUnknown
	}
	id, ok := t.byName[unsafeStringView(method)]
	if !ok {
		return MethodUnknown
	}
	return id
}

// Name returns the method name for id.
func (t *MethodTable) Name(id MethodID) (string, bool) {
	if t == nil || id == MethodUnknown {
		return "", false
	}
	i := int(id) - 1
	if i < 0 || i >= len(t.names) {
		return "", false
	}
	return t.names[i], true
}

func unsafeStringView(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}
