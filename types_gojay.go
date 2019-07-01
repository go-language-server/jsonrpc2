// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build gojay

package jsonrpc2

import (
	"encoding/json"
	"errors"
	"unsafe"

	"github.com/francoispqt/gojay"
)

// RawMessage mimic json.RawMessage.
//
// RawMessage is a raw encoded JSON value.
// It implements Marshaler and Unmarshaler and can
// be used to delay JSON decoding or precompute a JSON encoding.
type RawMessage gojay.EmbeddedJSON

func (m RawMessage) String() string {
	if m == nil {
		return ""
	}

	return *(*string)(unsafe.Pointer(&m))
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

	*m = append((*m)[0:0], data...)

	return nil
}

var _ json.Marshaler = (*RawMessage)(nil)
var _ json.Unmarshaler = (*RawMessage)(nil)

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (r *wireRequest) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&r.JSONRPC)
	case keyID:
		s := r.ID.String()
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

// NKeys returns the number of keys to unmarshal
func (r *wireRequest) NKeys() int { return 4 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (r *wireRequest) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, r.JSONRPC)
	enc.StringKey(keyID, r.ID.String())
	enc.StringKey(keyMethod, r.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, (*gojay.EmbeddedJSON)(r.Params))
}

// IsNil returns wether the structure is nil value or not
func (r *wireRequest) IsNil() bool { return r == nil }

// compile time check whether the wireRequest implements a gojay.MarshalerJSONObject interface.
var _ gojay.MarshalerJSONObject = (*wireRequest)(nil)

// compile time check whether the wireRequest implements a gojay.UnmarshalerJSONObject interface.
var _ gojay.UnmarshalerJSONObject = (*wireRequest)(nil)

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (r *wireResponse) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&r.JSONRPC)
	case keyID:
		s := r.ID.String()
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

// NKeys returns the number of keys to unmarshal
func (r *wireResponse) NKeys() int { return 4 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (r *wireResponse) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, r.JSONRPC)
	enc.StringKey(keyID, r.ID.String())
	enc.ObjectKeyOmitEmpty(keyError, r.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyResult, (*gojay.EmbeddedJSON)(r.Result))
}

// IsNil returns wether the structure is nil value or not
func (r *wireResponse) IsNil() bool { return r == nil }

// compile time check whether the wireResponse implements a gojay.MarshalerJSONObject interface.
var _ gojay.MarshalerJSONObject = (*wireResponse)(nil)

// compile time check whether the wireResponse implements a gojay.UnmarshalerJSONObject interface.
var _ gojay.UnmarshalerJSONObject = (*wireResponse)(nil)

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (r *Combined) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&r.JSONRPC)
	case keyID:
		if r.ID == nil {
			r.ID = &ID{}
		}
		s := r.ID.String()
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

// NKeys returns the number of keys to unmarshal
func (r *Combined) NKeys() int { return 6 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (r *Combined) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, r.JSONRPC)
	enc.StringKeyOmitEmpty(keyID, r.ID.String())
	enc.StringKey(keyMethod, r.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, (*gojay.EmbeddedJSON)(r.Params))
	enc.ObjectKeyOmitEmpty(keyError, r.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyResult, (*gojay.EmbeddedJSON)(r.Result))
}

// IsNil returns wether the structure is nil value or not
func (r *Combined) IsNil() bool { return r == nil }

// compile time check whether the Combined implements a gojay.MarshalerJSONObject interface.
var _ gojay.MarshalerJSONObject = (*Combined)(nil)

// compile time check whether the Combined implements a gojay.UnmarshalerJSONObject interface.
var _ gojay.UnmarshalerJSONObject = (*Combined)(nil)

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (m *NotificationMessage) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&m.JSONRPC)
	case keyMethod:
		return dec.String(&m.Method)
	case keyParams:
		if m.Params == nil {
			m.Params = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(m.Params))
	}
	return nil
}

// NKeys returns the number of keys to unmarshal
func (m *NotificationMessage) NKeys() int { return 3 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (m *NotificationMessage) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, m.JSONRPC)
	enc.StringKey(keyMethod, m.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, (*gojay.EmbeddedJSON)(m.Params))
}

// IsNil returns wether the structure is nil value or not
func (m *NotificationMessage) IsNil() bool { return m == nil }

// compile time check whether the NotificationMessage implements a gojay.MarshalerJSONObject interface.
var _ gojay.MarshalerJSONObject = (*NotificationMessage)(nil)

// compile time check whether the NotificationMessage implements a gojay.UnmarshalerJSONObject interface.
var _ gojay.UnmarshalerJSONObject = (*NotificationMessage)(nil)
