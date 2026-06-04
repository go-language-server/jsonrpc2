// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

// MessageViewKind classifies a borrowed [MessageView].
type MessageViewKind uint8

const (
	// MessageViewInvalid is the zero value and is not returned for a successful
	// scan.
	MessageViewInvalid MessageViewKind = iota
	// MessageViewCall is a request that carries a non-null identifier and expects
	// a response.
	MessageViewCall
	// MessageViewNotification is a request with no identifier or a null
	// identifier.
	MessageViewNotification
	// MessageViewResponseResult is a response carrying a result member. A JSON
	// null result is represented by the borrowed bytes "null".
	MessageViewResponseResult
	// MessageViewResponseError is a response carrying an error object.
	MessageViewResponseError
)

// MessageViewResponse is a short alias for a successful response view.
const MessageViewResponse = MessageViewResponseResult

const (
	messageViewInvalidName      = "invalid"
	messageViewCallName         = "call"
	messageViewNotificationName = "notification"
	messageViewResponseName     = "response"
	messageViewErrorName        = "error"
)

// String returns a stable diagnostic name for k.
func (k MessageViewKind) String() string {
	switch k {
	case MessageViewInvalid:
		return messageViewInvalidName
	case MessageViewCall:
		return messageViewCallName
	case MessageViewNotification:
		return messageViewNotificationName
	case MessageViewResponseResult:
		return messageViewResponseName
	case MessageViewResponseError:
		return messageViewErrorName
	default:
		return messageViewInvalidName
	}
}

// MessageView is a zero-copy view of a single JSON-RPC message.
//
// Every byte slice in MessageView aliases the frame passed to [ScanMessageView]
// or [ScanMessageViewWithMethods]. The view is valid only while that frame
// remains valid and unmodified. Callers that need to retain data must copy the
// specific fields they keep.
type MessageView struct {
	Kind MessageViewKind

	// ID is a parsed borrowed view of the "id" member. For notifications and
	// null IDs it is the zero value.
	ID IDView

	// Method is the borrowed method body with surrounding quotes removed. When
	// MethodEscaped is true, these are raw escaped body bytes; use MethodString
	// for the decoded slow path and MethodID/MethodTable only for unescaped names.
	Method        []byte
	MethodRaw     []byte
	MethodEscaped bool
	MethodID      MethodID

	// Params and Result are borrowed raw JSON value spans. Params is nil when the
	// request omits params or explicitly sets params to null. Result preserves a
	// null result as the borrowed bytes "null".
	Params []byte
	Result []byte

	// Error is populated when Kind is MessageViewResponseError.
	Error ErrorView
}

// MethodString decodes the method name as a Go string. It is intended for slow
// paths and diagnostics; hot dispatch should use Method, MethodID, or a
// [MethodTable].
func (v *MessageView) MethodString() (string, bool) {
	if len(v.MethodRaw) == 0 {
		return "", false
	}
	return unquoteJSONString(v.MethodRaw)
}

// IDView is a zero-copy view of a JSON-RPC identifier.
//
// StringBytes exposes the borrowed string body with surrounding quotes removed.
// When StringEscaped is true, those bytes are the raw escaped body, not decoded
// text; decode on demand with [IDView.StringValue].
type IDView struct {
	num     int64
	raw     []byte
	str     []byte
	kind    idKind
	escaped bool
}

// IsValid reports whether the identifier is set (number or string).
func (id IDView) IsValid() bool { return id.kind != idNone }

// IsNumber reports whether the identifier holds an integer value.
func (id IDView) IsNumber() bool { return id.kind == idNumber }

// IsString reports whether the identifier holds a string value.
func (id IDView) IsString() bool { return id.kind == idString }

// Number returns the integer value of the identifier and whether it is a number.
func (id IDView) Number() (int64, bool) { return id.num, id.kind == idNumber }

// StringBytes returns the borrowed string body and whether the ID is a string.
// When StringEscaped is true, the returned bytes are raw escaped body bytes;
// use StringValue for the decoded slow path.
func (id IDView) StringBytes() ([]byte, bool) { return id.str, id.kind == idString }

// StringEscaped reports whether StringBytes carries raw escaped bytes rather
// than decoded string bytes.
func (id IDView) StringEscaped() bool { return id.kind == idString && id.escaped }

// StringValue decodes the identifier string. It may allocate for escaped IDs;
// hot paths should prefer [IDView.StringBytes] when possible.
func (id IDView) StringValue() (string, bool) {
	if id.kind != idString {
		return "", false
	}
	return unquoteJSONString(id.raw)
}

// Raw returns the borrowed raw JSON span for the identifier.
func (id IDView) Raw() []byte { return id.raw }

// ErrorView is a zero-copy view of a JSON-RPC error object.
type ErrorView struct {
	Code Code

	// Message is the borrowed message body with surrounding quotes removed. When
	// MessageEscaped is true, these are raw escaped bytes.
	Message        []byte
	MessageRaw     []byte
	MessageEscaped bool

	Data []byte
}

// MessageString decodes the error message as a Go string. It may allocate for
// escaped messages; hot paths should prefer Message when MessageEscaped is false.
func (e *ErrorView) MessageString() (string, bool) {
	if len(e.MessageRaw) == 0 {
		return "", false
	}
	return unquoteJSONString(e.MessageRaw)
}

// ScanMessageView scans a single JSON-RPC message and returns a borrowed view of
// its recognized fields. It performs no copies on the common no-escape path.
func ScanMessageView(frame []byte) (MessageView, error) {
	return ScanMessageViewWithMethods(frame, nil)
}

// ScanMessageViewWithMethods is like [ScanMessageView], but it also resolves an
// unescaped method body through methods and stores the resulting token in
// MessageView.MethodID.
func ScanMessageViewWithMethods(frame []byte, methods *MethodTable) (MessageView, error) {
	i := skipSpace(frame, 0)
	if i < len(frame) && frame[i] == '[' {
		return MessageView{}, ErrInvalidRequest
	}

	var f fields
	end, ok := scanObject(frame, &f)
	if !ok {
		return MessageView{}, ErrParse
	}
	if skipSpace(frame, end) != len(frame) {
		return MessageView{}, ErrParse
	}

	return f.toMessageView(methods)
}

func (f *fields) toMessageView(methods *MethodTable) (MessageView, error) {
	switch {
	case f.hasMethod && !f.hasResult && !f.hasError:
		return f.toRequestView(methods)
	case !f.hasMethod:
		return f.toResponseView()
	default:
		return MessageView{}, ErrInvalidRequest
	}
}

func (f *fields) toRequestView(methods *MethodTable) (MessageView, error) {
	if !f.validVersion() {
		return MessageView{}, ErrInvalidRequest
	}

	method, methodEscaped, ok := jsonStringBody(f.method)
	if !ok {
		return MessageView{}, ErrInvalidRequest
	}

	var id IDView
	if f.hasID {
		var idok bool
		id, idok = parseIDView(f.id)
		if !idok {
			return MessageView{}, ErrInvalidRequest
		}
	}

	// "params":null is treated as absent, matching DecodeMessage.
	var params []byte
	if f.hasParams && !isNullLiteral(f.params) {
		params = f.params
	}

	kind := MessageViewNotification
	if f.hasID && id.IsValid() {
		kind = MessageViewCall
	}

	view := MessageView{
		Kind:          kind,
		ID:            id,
		Method:        method,
		MethodRaw:     f.method,
		MethodEscaped: methodEscaped,
		Params:        params,
	}
	if !methodEscaped || methods == nil {
		view.Method = method
	}
	if !methodEscaped && methods != nil {
		view.MethodID = methods.Lookup(method)
	}
	return view, nil
}

func (f *fields) toResponseView() (MessageView, error) {
	if !f.validVersion() {
		return MessageView{}, ErrInvalidRequest
	}
	if f.hasResult && f.hasError {
		return MessageView{}, ErrInvalidRequest
	}

	var id IDView
	if f.hasID {
		var idok bool
		id, idok = parseIDView(f.id)
		if !idok {
			return MessageView{}, ErrInvalidRequest
		}
	}

	if f.hasError {
		if isNullLiteral(f.errobj) {
			return MessageView{}, ErrInvalidRequest
		}
		errView, ok := parseErrorView(f.errobj)
		if !ok {
			return MessageView{}, ErrInvalidRequest
		}
		return MessageView{Kind: MessageViewResponseError, ID: id, Error: errView}, nil
	}

	if !f.hasResult {
		return MessageView{}, ErrInvalidRequest
	}
	return MessageView{Kind: MessageViewResponseResult, ID: id, Result: f.result}, nil
}

func parseIDView(span []byte) (IDView, bool) {
	if len(span) == 0 {
		return IDView{}, false
	}
	switch span[0] {
	case 'n':
		if isNullLiteral(span) {
			return IDView{raw: span}, true
		}
		return IDView{}, false
	case '"':
		body, escaped, ok := jsonStringBody(span)
		if !ok {
			return IDView{}, false
		}
		return IDView{raw: span, str: body, kind: idString, escaped: escaped}, true
	default:
		n, ok := parseInt64Bytes(span)
		if !ok {
			return IDView{}, false
		}
		return IDView{num: n, raw: span, kind: idNumber}, true
	}
}

func parseErrorView(span []byte) (ErrorView, bool) {
	var codeSpan, msgSpan, dataSpan []byte
	if !scanErrorObject(span, &codeSpan, &msgSpan, &dataSpan) {
		return ErrorView{}, false
	}

	var out ErrorView
	if codeSpan != nil {
		n, ok := parseInt64Bytes(codeSpan)
		if !ok || n < -1<<31 || n >= 1<<31 {
			return ErrorView{}, false
		}
		out.Code = Code(n)
	}
	if msgSpan != nil {
		msg, escaped, ok := jsonStringBody(msgSpan)
		if !ok {
			return ErrorView{}, false
		}
		out.Message = msg
		out.MessageRaw = msgSpan
		out.MessageEscaped = escaped
	}
	out.Data = dataSpan
	return out, true
}

// jsonStringBody validates a JSON string span enough for borrowed view parsing
// and returns the unquoted body bytes. If escaped is true, the returned body is
// the raw escaped body, not decoded text.
func jsonStringBody(span []byte) (body []byte, escaped, ok bool) {
	if len(span) < 2 || span[0] != '"' || span[len(span)-1] != '"' {
		return nil, false, false
	}
	body = span[1 : len(span)-1]
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '"':
			return nil, false, false
		case '\\':
			escaped = true
			i++
			if i >= len(body) {
				return nil, false, false
			}
			switch body[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				if _, ok := readHex4(body[i:]); !ok {
					return nil, false, false
				}
				i += 4
			default:
				return nil, false, false
			}
		}
	}
	return body, escaped, true
}
