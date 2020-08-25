// Copyright 2019 The Go Language Server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build gojay

package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/francoispqt/gojay"
)

// RawMessage mimic json.RawMessage.
//
// RawMessage is a raw encoded JSON value.
// It implements Marshaler and Unmarshaler and can
// be used to delay JSON decoding or precompute a JSON encoding.
type RawMessage gojay.EmbeddedJSON

// compile time check whether the RawMessage implements a json.Marshaler and json.Unmarshaler interfaces.
var (
	_ json.Marshaler   = (*RawMessage)(nil)
	_ json.Unmarshaler = (*RawMessage)(nil)
)

func (m RawMessage) String() string {
	if m == nil {
		return ""
	}

	return string(m)
}

// MarshalJSON implements json.Marshaler.
//
// The returns m as the JSON encoding of m.
func (m RawMessage) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte{110, 117, 108, 108}, nil // null
	}

	return m, nil
}

// UnmarshalJSON implements json.Unmarshaler.
//
// The sets *m to a copy of data.
func (m *RawMessage) UnmarshalJSON(data []byte) error {
	if m == nil {
		return errors.New("jsonrpc2.RawMessage: UnmarshalJSON on nil pointer")
	}

	*m = append((*m)[:0], data...)

	return nil
}

// MarshalJSONObject implements gojay.MarshalerJSONObject.
func (r *version) MarshalJSONObject(enc *gojay.Encoder) {
	enc.String(Version)
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *version) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *version) UnmarshalJSONObject(dec *gojay.Decoder, _ string) error {
	return dec.String(&versionStr)
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *version) NKeys() int { return 0 }

// compile time check whether the WireRequest implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*version)(nil)
	_ gojay.UnmarshalerJSONObject = (*version)(nil)
)

// MarshalJSONObject implements gojay.MarshalerJSONObject.
func (r *WireRequest) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, Version)
	enc.StringKeyOmitEmpty(keyID, fmt.Sprint(r.ID))
	enc.StringKey(keyMethod, r.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, (*gojay.EmbeddedJSON)(r.Params))
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *WireRequest) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *WireRequest) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&versionStr)
	case keyID:
		if r.ID == nil {
			r.ID = &ID{}
		}
		s := fmt.Sprint(r.ID)
		return dec.String(&s)
	case keyMethod:
		return dec.String(&r.Method)
	case keyParams:
		if r.Params == nil {
			r.Params = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(r.Params))
	}
	return nil
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *WireRequest) NKeys() int { return 4 }

// compile time check whether the WireRequest implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*WireRequest)(nil)
	_ gojay.UnmarshalerJSONObject = (*WireRequest)(nil)
)

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (r *WireResponse) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, Version)
	enc.StringKeyOmitEmpty(keyID, fmt.Sprint(r.ID))
	enc.ObjectKeyOmitEmpty(keyError, r.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyResult, (*gojay.EmbeddedJSON)(r.Result))
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *WireResponse) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject
func (r *WireResponse) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&versionStr)
	case keyID:
		if r.ID == nil {
			r.ID = &ID{}
		}
		s := fmt.Sprint(r.ID)
		return dec.String(&s)
	case keyError:
		if r.Error == nil {
			r.Error = &Error{}
		}
		return dec.Object(r.Error)
	case keyResult:
		if r.Result == nil {
			r.Result = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(r.Result))
	}
	return nil
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *WireResponse) NKeys() int { return 4 }

// compile time check whether the WireResponse implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*WireResponse)(nil)
	_ gojay.UnmarshalerJSONObject = (*WireResponse)(nil)
)

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (r *Combined) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, Version)
	enc.StringKeyOmitEmpty(keyID, fmt.Sprint(r.ID))
	enc.StringKey(keyMethod, r.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, (*gojay.EmbeddedJSON)(r.Params))
	enc.ObjectKeyOmitEmpty(keyError, r.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyResult, (*gojay.EmbeddedJSON)(r.Result))
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *Combined) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (r *Combined) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&versionStr)
	case keyID:
		if r.ID == nil {
			r.ID = &ID{}
		}
		s := fmt.Sprint(r.ID)
		return dec.String(&s)
	case keyMethod:
		return dec.String(&r.Method)
	case keyParams:
		if r.Params == nil {
			r.Params = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(r.Params))
	case keyError:
		if r.Error == nil {
			r.Error = &Error{}
		}
		return dec.Object(r.Error)
	case keyResult:
		if r.Result == nil {
			r.Result = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(r.Result))
	}
	return nil
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *Combined) NKeys() int { return 6 }

// compile time check whether the request implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*Combined)(nil)
	_ gojay.UnmarshalerJSONObject = (*Combined)(nil)
)
