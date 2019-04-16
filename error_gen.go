// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"github.com/francoispqt/gojay"
)

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (v *Error) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case "code":
		return dec.Int64((*int64)(&v.Code))
	case "message":
		return dec.String(&v.Message)
	}
	return nil
}

// NKeys returns the number of keys to unmarshal
func (v *Error) NKeys() int { return 2 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (v *Error) MarshalJSONObject(enc *gojay.Encoder) {
	enc.IntKey("code", int(v.Code))
	enc.StringKey("message", v.Message)
}

// IsNil returns wether the structure is nil value or not
func (v *Error) IsNil() bool { return v == nil }
