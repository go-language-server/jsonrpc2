// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import (
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"go.lsp.dev/jsonrpc2/conformance"
)

// FuzzRoundTrip asserts encode/decode stability for valid messages: decoding a
// message, re-encoding it, and decoding the result must yield a structurally
// equivalent message. Byte equality is deliberately not required because the
// encoder canonicalizes the envelope (fixed member order, params:null collapsed
// to absent, whitespace removed), so the fixed point is reached after one
// encode rather than on the original bytes.
//
// The corpus is restricted to inputs the strict single-message decoder accepts;
// arbitrary bytes are the job of [FuzzScan]. Inputs the decoder rejects are
// skipped, so this fuzz only ever exercises the encode(decode(x)) identity on
// the message shapes the package actually produces and consumes.
func FuzzRoundTrip(f *testing.F) {
	for _, v := range conformance.Valid() {
		f.Add([]byte(v.Wire))
	}
	// Response shapes the encoder must round-trip: success with structured,
	// scalar, array, and explicit-null results, plus an error with data.
	f.Add([]byte(`{"jsonrpc":"2.0","result":{"ok":true},"id":1}`))
	f.Add([]byte(`{"jsonrpc":"2.0","result":[1,2,3],"id":"x"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","result":null,"id":1}`))
	f.Add([]byte(`{"jsonrpc":"2.0","result":42,"id":1}`))
	f.Add([]byte(`{"jsonrpc":"2.0","error":{"code":-32000,"message":"boom","data":{"k":1}},"id":1}`))
	f.Add([]byte(`{"jsonrpc":"2.0","error":{"code":-32601,"message":"x"},"id":null}`))
	// Request shapes: notification, string id, escaped method, no params.
	f.Add([]byte(`{"jsonrpc":"2.0","method":"a/b","params":{"x":1},"id":"req-1"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"note"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"esc\n\"name","id":-42}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		m1, err := DecodeMessage(data)
		if err != nil {
			// Not a single valid message; FuzzScan owns the arbitrary-bytes
			// no-panic guarantee, so there is nothing to round-trip here.
			t.Skip()
		}

		enc, err := EncodeMessage(m1)
		if err != nil {
			t.Fatalf("EncodeMessage(%#v) error: %v", m1, err)
		}

		m2, err := DecodeMessage(enc)
		if err != nil {
			t.Fatalf("re-decode of %q (from %q) failed: %v", enc, data, err)
		}

		if !messagesEquivalent(t, m1, m2) {
			t.Fatalf("round-trip not stable\n  in:   %q\n  enc:  %q\n  m1:   %#v\n  m2:   %#v", data, enc, m1, m2)
		}

		// The encoder is a fixed point: encoding the re-decoded message must
		// reproduce the same bytes, so the canonical form is genuinely stable.
		enc2, err := EncodeMessage(m2)
		if err != nil {
			t.Fatalf("EncodeMessage(m2) error: %v", err)
		}
		if string(enc) != string(enc2) {
			t.Fatalf("encode not idempotent\n  enc:  %q\n  enc2: %q", enc, enc2)
		}
	})
}

// messagesEquivalent reports whether two decoded messages carry the same kind,
// identifier, method, and payload. Raw payload spans are compared structurally
// (via JSON-compaction) so that semantically equal but differently spaced bytes
// match; identifiers and methods are compared exactly.
func messagesEquivalent(t *testing.T, a, b Message) bool {
	t.Helper()
	if msgKind(a) != msgKind(b) {
		return false
	}
	switch ma := a.(type) {
	case *Call:
		mb := b.(*Call)
		return idEqual(ma.ID(), mb.ID()) &&
			ma.Method() == mb.Method() &&
			rawEquivalent(t, ma.Params(), mb.Params())
	case *Notification:
		mb := b.(*Notification)
		return ma.Method() == mb.Method() &&
			rawEquivalent(t, ma.Params(), mb.Params())
	case *Response:
		mb := b.(*Response)
		if !idEqual(ma.ID(), mb.ID()) {
			return false
		}
		if (ma.Err() == nil) != (mb.Err() == nil) {
			return false
		}
		if ma.Err() != nil {
			ea, eb := ma.Err().(*Error), mb.Err().(*Error)
			return ea.Code == eb.Code && ea.Message == eb.Message &&
				rawEquivalent(t, ea.Data, eb.Data)
		}
		return rawEquivalent(t, ma.Result(), mb.Result())
	default:
		return false
	}
}

// rawEquivalent compares two raw payload spans for byte equality, treating a
// nil and an empty span as equal. Byte equality (not JSON-structural equality)
// is the right comparison here: both spans are produced by decoding canonically
// encoded envelopes (the encoder copies params/result/data verbatim and is
// deterministic), so a stable round-trip yields byte-identical spans. Using
// JSON-structural equality would also wrongly reject the documented
// opaque-container passthrough (for example params {""}), whose interior is not
// valid JSON yet round-trips verbatim and byte-stably.
func rawEquivalent(_ *testing.T, a, b RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	return string(a) == string(b)
}

// idEqual compares two identifiers for exact equality, including their kind, so
// that a numeric id and a string id with the same textual form do not match.
func idEqual(a, b ID) bool {
	return gocmp.Equal(a, b, gocmp.AllowUnexported(ID{}))
}
