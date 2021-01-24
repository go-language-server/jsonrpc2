// Copyright 2020 The Go Language Server Authors.
// SPDX-License-Identifier: BSD-3-Clause

// +build !gojay

package jsonrpc2

import (
	json "github.com/goccy/go-json"
)

type RawMessage = json.RawMessage
