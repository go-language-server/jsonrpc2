// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import "sync"

// encodeBufInitCap is the initial capacity of a pooled encode buffer. It is
// sized to hold a typical small envelope without a reallocation.
const encodeBufInitCap = 256

// encodeBufMaxCap bounds the capacity of buffers returned to the pool, so that
// an occasional very large message does not keep a large buffer pinned for the
// lifetime of the process.
const encodeBufMaxCap = 1 << 16

// encodeBufPool recycles the byte buffers used to append message envelopes.
var encodeBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, encodeBufInitCap)
		return &b
	},
}

// getEncodeBuf returns a reset buffer from the pool.
func getEncodeBuf() *[]byte {
	bp := encodeBufPool.Get().(*[]byte)
	*bp = (*bp)[:0]
	return bp
}

// putEncodeBuf returns bp to the pool unless its backing array has grown beyond
// encodeBufMaxCap, in which case it is dropped so the oversized array can be
// collected.
func putEncodeBuf(bp *[]byte) {
	if cap(*bp) > encodeBufMaxCap {
		return
	}
	encodeBufPool.Put(bp)
}

// cloneBytes returns a copy of src in a freshly allocated, right-sized slice. A
// nil src yields a nil result so that "absent" stays distinguishable from
// "present but empty".
func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
