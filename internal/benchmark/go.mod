module go.lsp.dev/jsonrpc2/internal/benchmark

go 1.26

replace go.lsp.dev/jsonrpc2 => ../..

replace go.lsp.dev/jsonrpc2/codec/goccy => ../../codec/goccy

replace go.lsp.dev/jsonrpc2/codec/sonic => ../../codec/sonic

require (
	github.com/creachadair/jrpc2 v1.3.5
	github.com/go-json-experiment/json v0.0.0-20260601182631-00ed12fed2a6
	github.com/google/go-cmp v0.7.0
	github.com/segmentio/encoding v0.5.4
	go.lsp.dev/jsonrpc2 v0.0.0-00010101000000-000000000000
	go.lsp.dev/jsonrpc2/codec/goccy v0.0.0-00010101000000-000000000000
	go.lsp.dev/jsonrpc2/codec/sonic v0.0.0-00010101000000-000000000000
)

require (
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/bytedance/sonic v1.15.2 // indirect
	github.com/bytedance/sonic/loader v0.5.1 // indirect
	github.com/cloudwego/base64x v0.1.7 // indirect
	github.com/creachadair/mds v0.26.1 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/klauspost/cpuid/v2 v2.2.9 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/arch v0.0.0-20210923205945-b76863e36670 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
)
