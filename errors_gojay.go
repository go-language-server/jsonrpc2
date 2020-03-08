// Copyright 2019 The Go Language Server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build gojay

package jsonrpc2

import "github.com/francoispqt/gojay"

// MarshalJSONObject implements gojay.MarshalerJSONObject.
func (e *Error) MarshalJSONObject(enc *gojay.Encoder) {
	enc.IntKey(keyCode, int(e.Code))
	enc.StringKey(keyMessage, e.Message)
}

// IsNil implements gojay.MarshalerJSONObject.
func (e *Error) IsNil() bool { return e == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (e *Error) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyCode:
		return dec.Int64((*int64)(&e.Code))
	case keyMessage:
		return dec.String(&e.Message)
	}
	return nil
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (e *Error) NKeys() int { return 2 }

// compile time check whether the Error implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*Error)(nil)
	_ gojay.UnmarshalerJSONObject = (*Error)(nil)
)
