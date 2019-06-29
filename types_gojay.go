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
func (v *request) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&v.JSONRPC)
	case keyID:
		s := v.ID.String()
		return dec.String(&s)
	case keyMethod:
		return dec.String(&v.Method)
	case keyParams:
		if v.Params == nil {
			v.Params = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(v.Params))
	}
	return nil
}

// NKeys returns the number of keys to unmarshal
func (v *request) NKeys() int { return 4 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (v *request) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, v.JSONRPC)
	enc.StringKey(keyID, v.ID.String())
	enc.StringKey(keyMethod, v.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, (*gojay.EmbeddedJSON)(v.Params))
}

// IsNil returns wether the structure is nil value or not
func (v *request) IsNil() bool { return v == nil }

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (v *Response) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&v.JSONRPC)
	case keyID:
		s := v.ID.String()
		return dec.String(&s)
	case keyError:
		if v.Error == nil {
			v.Error = &Error{}
		}
		return dec.Object(v.Error)
	case keyResult:
		if v.Result == nil {
			v.Result = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(v.Result))
	}
	return nil
}

// NKeys returns the number of keys to unmarshal
func (v *Response) NKeys() int { return 4 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (v *Response) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, v.JSONRPC)
	enc.StringKey(keyID, v.ID.String())
	enc.ObjectKeyOmitEmpty(keyError, v.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyResult, (*gojay.EmbeddedJSON)(v.Result))
}

// IsNil returns wether the structure is nil value or not
func (v *Response) IsNil() bool { return v == nil }

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (v *Combined) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&v.JSONRPC)
	case keyID:
		if v.ID == nil {
			v.ID = &ID{}
		}
		s := v.ID.String()
		return dec.String(&s)
	case keyMethod:
		return dec.String(&v.Method)
	case keyParams:
		if v.Params == nil {
			v.Params = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(v.Params))
	case keyError:
		if v.Error == nil {
			v.Error = &Error{}
		}
		return dec.Object(v.Error)
	case keyResult:
		if v.Result == nil {
			v.Result = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(v.Result))
	}
	return nil
}

// NKeys returns the number of keys to unmarshal
func (v *Combined) NKeys() int { return 6 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (v *Combined) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, v.JSONRPC)
	enc.StringKeyOmitEmpty(keyID, v.ID.String())
	enc.StringKey(keyMethod, v.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, (*gojay.EmbeddedJSON)(v.Params))
	enc.ObjectKeyOmitEmpty(keyError, v.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyResult, (*gojay.EmbeddedJSON)(v.Result))
}

// IsNil returns wether the structure is nil value or not
func (v *Combined) IsNil() bool { return v == nil }

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (v *NotificationMessage) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case keyJSONRPC:
		return dec.String(&v.JSONRPC)
	case keyMethod:
		return dec.String(&v.Method)
	case keyParams:
		if v.Params == nil {
			v.Params = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(v.Params))
	}
	return nil
}

// NKeys returns the number of keys to unmarshal
func (v *NotificationMessage) NKeys() int { return 3 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (v *NotificationMessage) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey(keyJSONRPC, v.JSONRPC)
	enc.StringKey(keyMethod, v.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty(keyParams, (*gojay.EmbeddedJSON)(v.Params))
}

// IsNil returns wether the structure is nil value or not
func (v *NotificationMessage) IsNil() bool { return v == nil }
