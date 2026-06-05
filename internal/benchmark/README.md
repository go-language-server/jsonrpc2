# Benchmark governance

This directory is a separate Go module used only for comparative benchmarks.
It intentionally keeps rival-library dependencies out of the root
`go.lsp.dev/jsonrpc2` module graph:

- `github.com/creachadair/jrpc2`
- `github.com/segmentio/encoding`
- the transitive packages needed by those benchmark references

The benchmark module is **not vendored**. Run it in explicit module mode so a
root or CI-level `vendor/` directory cannot change the dependency graph that is
being measured.

## Reproducible artifact workflow

Use `bench-artifacts.sh` from the repository root:

```sh
internal/benchmark/bench-artifacts.sh \
  --bench 'Benchmark(RoundTripVoid|Decode|Encode)' \
  --count 10
```

The script writes a timestamped directory under
`internal/benchmark/artifacts/` containing:

- `bench.txt` — raw `go test -bench` output.
- `command.txt` — the exact benchmark command after normalization.
- `env.txt` — git, Go, `GOFLAGS`, `GOWORK`, and module-cache context.
- `cpu.pprof` and `mem.pprof` — pprof files, unless `--no-profiles` is used.

The artifact directory is ignored by git. Copy the relevant raw outputs into
`RESULTS.md` only after reviewing the environment and caveats.

To inspect a captured CPU profile:

```sh
go tool pprof internal/benchmark/artifacts/<run>/cpu.pprof
```

## Module-mode contract

`bench-artifacts.sh` normalizes the Go environment before running benchmarks:

- removes any inherited `-mod` / `--mod` entries from `GOFLAGS`;
- sets `GOFLAGS="<remaining flags> -mod=mod"`;
- sets `GOWORK=off`.

This prevents a root `vendor/` directory, CI vendor-fetch step, or developer
workspace from silently changing the nested benchmark module. If you need a
fully offline run, pre-warm the module cache and pass `GOPROXY=off`; the script
will still keep the benchmark in module mode.

Quick smoke check:

```sh
GOFLAGS=-mod=vendor GOPROXY=off \
  internal/benchmark/bench-artifacts.sh \
  --bench 'BenchmarkDecode/Minimal/ours' \
  --count 1 \
  --no-profiles
```

That command should pass even though the same benchmark module fails when run
directly with `GOFLAGS=-mod=vendor`.

