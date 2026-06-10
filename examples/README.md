# jsonrpc2 API examples

These examples are small command packages that exercise public
`go.lsp.dev/jsonrpc2` APIs. They are covered by `go test ./examples/...` and can
also be run directly with `go run`.

| Example | What it demonstrates | Run |
| --- | --- | --- |
| `peer` | A bidirectional in-process peer pair using `NewChannelStreamPair`, `NewPeer`, `Call`, and `Notify`. | `go run ./examples/peer` |
| `serve` | A loopback server using `HandlerServer`, `Serve`, and a client `NewStream` over `net.Conn`. | `go run ./examples/serve` |
| `batch` | A raw-frame batch client using `NewBatchClient`, `AppendBatch`, calls, and notifications. | `go run ./examples/batch` |
