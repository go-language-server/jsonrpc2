// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build gojay

package jsonrpc2

import "github.com/francoispqt/gojay"

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (e *Error) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyCode:
		return dec.Int64((*int64)(&e.Code))
	case keyMessage:
		return dec.String(&e.Message)
	}
	return nil
}

// NKeys returns the number of keys to unmarshal
func (e *Error) NKeys() int { return 2 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (e *Error) MarshalJSONObject(enc *gojay.Encoder) {
	enc.IntKey(keyCode, int(e.Code))
	enc.StringKey(keyMessage, e.Message)
}

// IsNil returns wether the structure is nil value or not
func (e *Error) IsNil() bool { return e == nil }

// compile time check whether the Error implements a gojay.MarshalerJSONObject interface.
var _ gojay.MarshalerJSONObject = (*Error)(nil)

// compile time check whether the Error implements a gojay.UnmarshalerJSONObject interface.
var _ gojay.UnmarshalerJSONObject = (*Error)(nil)
