// SPDX-License-Identifier: BSD-3-Clause
// SPDX-FileCopyrightText: Copyright 2021 The Go Language Server Authors

// +build !gojay

package jsonrpc2

import (
	json "github.com/goccy/go-json"
)

// RawMessage is a raw encoded JSON value.
// It implements Marshaler and Unmarshaler and can
// be used to delay JSON decoding or precompute a JSON encoding.
type RawMessage = json.RawMessage
