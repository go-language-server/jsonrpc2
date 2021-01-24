// Copyright 2020 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

// +build gojay

package jsonrpc2

import (
	"github.com/francoispqt/gojay"
)

// RawMessage is a raw encoded JSON value.
// It can be used to delay JSON decoding or precompute a JSON encoding.
type RawMessage = gojay.EmbeddedJSON

var versionStr = string(Version)

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

// compile time check whether the version implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*version)(nil)
	_ gojay.UnmarshalerJSONObject = (*version)(nil)
)

// MarshalJSONObject implements gojay.MarshalerJSONObject.
func (r *ID) MarshalJSONObject(enc *gojay.Encoder) {
	switch {
	case r.number > 0:
		enc.Int64(r.number)
	case r.name != "":
		enc.String(r.name)
	}
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *ID) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *ID) UnmarshalJSONObject(dec *gojay.Decoder, _ string) error {
	if err := dec.DecodeInt64(&r.number); err == nil {
		return nil
	}
	return dec.DecodeString(&r.name)
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *ID) NKeys() int { return 0 }

// compile time check whether the ID implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*ID)(nil)
	_ gojay.UnmarshalerJSONObject = (*ID)(nil)
)

// MarshalJSONObject implements gojay.MarshalerJSONObject.
func (r *wireRequest) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, Version)
	enc.ObjectKeyOmitEmpty(keyID, r.ID)
	enc.StringKey(keyMethod, r.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, r.Params)
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *wireRequest) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *wireRequest) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&versionStr)
	case keyID:
		if r.ID == nil {
			r.ID = &ID{}
		}
		return dec.Object(r.ID)
	case keyMethod:
		return dec.String(&r.Method)
	case keyParams:
		if r.Params == nil {
			r.Params = &RawMessage{}
		}
		return dec.EmbeddedJSON(r.Params)
	}
	return nil
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *wireRequest) NKeys() int { return 4 }

// compile time check whether the wireRequest implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*wireRequest)(nil)
	_ gojay.UnmarshalerJSONObject = (*wireRequest)(nil)
)

// MarshalJSONObject implements gojay's MarshalerJSONObject.
func (r *wireResponse) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, Version)
	enc.ObjectKeyOmitEmpty(keyID, r.ID)
	enc.ObjectKeyOmitEmpty(keyError, r.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyResult, r.Result)
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *wireResponse) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *wireResponse) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&versionStr)
	case keyID:
		if r.ID == nil {
			r.ID = &ID{}
		}
		return dec.Object(r.ID)
	case keyError:
		return dec.Object(r.Error)
	case keyResult:
		return dec.EmbeddedJSON(r.Result)
	}
	return nil
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *wireResponse) NKeys() int { return 4 }

// compile time check whether the wireResponse implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*wireResponse)(nil)
	_ gojay.UnmarshalerJSONObject = (*wireResponse)(nil)
)

// MarshalJSONObject implements gojay's MarshalerJSONObject.
func (r *combined) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, Version)
	enc.ObjectKeyOmitEmpty(keyID, r.ID)
	enc.StringKey(keyMethod, r.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, r.Params)
	enc.ObjectKeyOmitEmpty(keyError, r.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyResult, r.Result)
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *combined) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject.
func (r *combined) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&versionStr)
	case keyID:
		if r.ID == nil {
			r.ID = &ID{}
		}
		return dec.Object(r.ID)
	case keyMethod:
		return dec.String(&r.Method)
	case keyParams:
		if r.Params == nil {
			r.Params = &RawMessage{}
		}
		return dec.EmbeddedJSON(r.Result)
	case keyError:
		return dec.Object(r.Error)
	case keyResult:
		return dec.EmbeddedJSON(r.Result)
	}
	return nil
}

// NKeys implements gojay.UnmarshalerJSONObject.
func (r *combined) NKeys() int { return 6 }

// compile time check whether the combined implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*combined)(nil)
	_ gojay.UnmarshalerJSONObject = (*combined)(nil)
)
