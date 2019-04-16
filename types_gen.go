// Copyright 2019 The go-language-server Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"

	"github.com/francoispqt/gojay"
)

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (v *Request) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case "jsonrpc":
		return dec.String(&v.JSONRPC)
	case "id":
		s := v.ID.String()
		return dec.String(&s)
	case "method":
		return dec.String(&v.Method)
	case "params":
		if v.Params == nil {
			v.Params = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(v.Params))
	}
	return nil
}

// NKeys returns the number of keys to unmarshal
func (v *Request) NKeys() int { return 4 }

// MarshalJSONObject implements gojay's MarshalerJSONObject
func (v *Request) MarshalJSONObject(enc *gojay.Encoder) {
	enc.StringKey("jsonrpc", v.JSONRPC)
	enc.StringKey("id", v.ID.String())
	enc.StringKey("method", v.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty("params", (*gojay.EmbeddedJSON)(v.Params))
}

// IsNil returns wether the structure is nil value or not
func (v *Request) IsNil() bool { return v == nil }

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (v *Response) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case "jsonrpc":
		return dec.String(&v.JSONRPC)
	case "id":
		s := v.ID.String()
		return dec.String(&s)
	case "error":
		if v.Error == nil {
			v.Error = &Error{}
		}
		return dec.Object(v.Error)
	case "result":
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
	enc.StringKey("jsonrpc", v.JSONRPC)
	enc.StringKey("id", v.ID.String())
	enc.ObjectKeyOmitEmpty("error", v.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty("result", (*gojay.EmbeddedJSON)(v.Result))
}

// IsNil returns wether the structure is nil value or not
func (v *Response) IsNil() bool { return v == nil }

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (v *Combined) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case "jsonrpc":
		return dec.String(&v.JSONRPC)
	case "id":
		if v.ID == nil {
			v.ID = &ID{}
		}
		s := v.ID.String()
		return dec.String(&s)
	case "method":
		return dec.String(&v.Method)
	case "params":
		if v.Params == nil {
			v.Params = &json.RawMessage{}
		}
		return dec.EmbeddedJSON((*gojay.EmbeddedJSON)(v.Params))
	case "error":
		if v.Error == nil {
			v.Error = &Error{}
		}
		return dec.Object(v.Error)
	case "result":
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
	enc.StringKey("jsonrpc", v.JSONRPC)
	enc.StringKeyOmitEmpty("id", v.ID.String())
	enc.StringKey("method", v.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty("params", (*gojay.EmbeddedJSON)(v.Params))
	enc.ObjectKeyOmitEmpty("error", v.Error)
	enc.AddEmbeddedJSONKeyOmitEmpty("result", (*gojay.EmbeddedJSON)(v.Result))
}

// IsNil returns wether the structure is nil value or not
func (v *Combined) IsNil() bool { return v == nil }

// UnmarshalJSONObject implements gojay's UnmarshalerJSONObject
func (v *NotificationMessage) UnmarshalJSONObject(dec *gojay.Decoder, k string) error {
	switch k {
	case "jsonrpc":
		return dec.String(&v.JSONRPC)
	case "method":
		return dec.String(&v.Method)
	case "params":
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
	enc.StringKey("jsonrpc", v.JSONRPC)
	enc.StringKey("method", v.Method)
	enc.AddEmbeddedJSONKeyOmitEmpty("params", (*gojay.EmbeddedJSON)(v.Params))
}

// IsNil returns wether the structure is nil value or not
func (v *NotificationMessage) IsNil() bool { return v == nil }
