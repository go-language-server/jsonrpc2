// Copyright 2020 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

// +build gojay

package jsonrpc2

import (
	"github.com/francoispqt/gojay"
)

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
func (r *request) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, Version)
	enc.ObjectKeyOmitEmpty(keyID, r.ID)
	enc.StringKey(keyMethod, r.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, r.Params)
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *request) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *request) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
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
func (r *request) NKeys() int { return 4 }

// compile time check whether the request implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*request)(nil)
	_ gojay.UnmarshalerJSONObject = (*request)(nil)
)

// MarshalJSONObject implements gojay's MarshalerJSONObject.
func (r *response) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, Version)
	enc.ObjectKeyOmitEmpty(keyID, r.ID)
	enc.ObjectKeyOmitEmpty(keyError, r.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyResult, r.Result)
}

// IsNil implements gojay.MarshalerJSONObject.
func (r *response) IsNil() bool { return r == nil }

// UnmarshalJSONObject implements gojay.UnmarshalerJSONObject.
func (r *response) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
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
func (r *response) NKeys() int { return 4 }

// compile time check whether the response implements a gojay.MarshalerJSONObject and gojay.UnmarshalerJSONObject interfaces.
var (
	_ gojay.MarshalerJSONObject   = (*response)(nil)
	_ gojay.UnmarshalerJSONObject = (*response)(nil)
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
