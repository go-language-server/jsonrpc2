# Comparative JSON-RPC 2.0 Benchmarks — POST hot-path optimization

This document records the comparative benchmark numbers for three Go
JSON-RPC 2.0 implementations, captured with the harness in this module
(`adapters.go`, `bench_test.go`). The numbers are reported exactly as measured.

> **Update — hot-path allocation optimization.** The original baseline in this
> file was the *pre-optimization* state (void round-trip **18 allocs/op** on the
> root `BenchmarkVoidRoundTrip`). A subsequent best-effort optimization pass cut
> the void round-trip to **10 allocs/op** on the root bench (≈ **12 allocs/op**
> through this harness, which additionally decodes the response into a
> `RawMessage`) and ≈ **3000 ns/op** (from ≈ 4850 ns/op), preserving the §3.3
> single-copy single-owner ownership invariant and keeping `go test -race
> -count=2 .` green. The four kept optimizations are documented in the
> [Optimization log](#optimization-log-hot-path-allocations) below, and every
> table in this file now reflects the **AFTER** numbers. The `Decode` /
> `ParseRequests` numbers are unchanged because those standalone entry points
> were intentionally left at their documented allocation floors (see the
> [decode floor note](#decode-allocation-floor-ac-p2--ac-p4-status)).

> **Update — next-layer optimization pass (this pass).** A further pass cut the
> root `BenchmarkVoidRoundTrip` from **10 → 6 allocs/op** and **572 → 408 B/op**
> (arm64, M3 Max, ≈ 2900 ns/op, no ns regression), and added a new synchronous-
> client mode. Every change was profiled first and gated on `go test -race
> -count=2 .` Two changes were applied, one was rejected on measurement, and one
> was rejected on safety — all documented below so the record is honest, not
> flattering.
>
> - **Kept #1 — concrete non-interface write path.** `conn.write` boxed a
>   `callWire`/`responseWire` value into the `Message` interface on every send,
>   forcing a heap allocation (confirmed by escape analysis). A `frameWriter`
>   internal seam frames the envelope from concrete fields, so the built-in
>   framers write without the box. Foreign `Stream`s keep the boxed fallback. **−2
>   allocs/op.**
> - **Kept #2 — merge (not pool) the per-request structs.** The releaser and
>   replied flag were folded into `incomingRequest` as value fields: three heap
>   objects become one GC-managed struct. This is a *merge*, not a `sync.Pool` —
>   zero ownership/lifetime change, so a synchronous handler may still retain its
>   request context, and there is no use-after-recycle hazard. (Pooling was
>   explicitly **not** done: it would buy only one more alloc, is the sole
>   use-after-free source, and `-race` cannot detect a `sync.Pool` use-after-
>   recycle because `Pool.Get`/`Put` synchronize internally.) **−2 allocs/op.**
> - **New — `SyncClient` (A1c caller-pumps-the-reader).** A distinct synchronous-
>   client mode that owns its read loop instead of running a background reader, so
>   a `Call` collapses the third goroutine hop. **~2030 ns/op vs the `Conn` round
>   trip's ~2900 ns** on the same `net.Pipe`+NDJSON transport. Reported as a
>   separate `jsonrpc2/sync` row with mode disclosure (see
>   [Synchronous-client mode](#synchronous-client-mode-a1c)); it is **not** a
>   head-to-head claim against `channel.Direct` and **not** a speedup of the
>   bidirectional `Conn`.
> - **Rejected on measurement — atomic shutdown fast path (A2).** An atomic
>   "shutting down" latch was implemented to skip the `stateMu` acquisition on the
>   read-only `shuttingDown` check in the write path. A clean A2-off vs A2-on
>   isolation (same code state, `-count=8`) was **statistically indistinguishable**
>   (~3679–3786 vs ~3656–3899 ns at P4/P12). The parallel path is scheduling-bound
>   (the CPU profile is 56% `pthread_cond_wait` + 26% `cond_signal`); the `stateMu`
>   mutex *delay* is parked-goroutine wait, not wall-clock. With zero measured
>   benefit and a real cost (a second source of truth for shutdown state that a
>   future maintainer must keep in sync), the latch was **reverted**. The
>   `TestCloseRacesConcurrentWrites` liveness stress test it motivated was **kept**
>   (it exercises Close racing concurrent writes on the locked path; `-race` cannot
>   catch an idle/close hang, so the liveness assertion is the gate). *Finding for
>   the record: the synchronization model is not the bottleneck on `net.Pipe`; the
>   slot-table and incoming map updates fundamentally need the lock.*
> - **Rejected on safety — `unsafe` borrowed decode.** Aliasing the read buffer to
>   drop the method-copy + `*Call` alloc on the inline-sync path was rejected. The
>   "copy on `Async`" escape invariant is incomplete: a synchronous handler that
>   does **not** call `Async` but retains `req.Params()`/`req.Method()` and returns
>   normally has no signal to trigger a copy, so the next frame overwrites the
>   borrowed bytes — an undetectable use-after-free (`-race` cannot see it). It
>   would be safe only with an explicit *public* params-lifetime contract change,
>   which buys ~1 alloc (the alloc target was already met without it). Not taken;
>   the public `DecodeMessage` keeps owning its bytes. The remaining `replier`
>   closure (~1 alloc) is likewise documented as the **server-dispatch alloc
>   floor**, consistent with the standalone decode-floor decision: removable only
>   by an interface change to `Replier` that damages every handler call site for
>   no ns gain.

> **Update — Round 6 Phase 0 baseline + common-row harness correction
> (2026-06-12).** Raw artifact:
> `internal/benchmark/artifacts/20260612T031259Z-r6-baseline` (root + full
> internal suite `-benchmem -count=10`, Go `go1.26.4 darwin/arm64`, Apple M3
> Max, HEAD `7147e05`,
> `GOEXPERIMENT=runtimefreegc,sizespecializedmalloc,runtimesecret`).
> **Harness integrity correction:** Round 5's `a28452c` switched the shared
> `newJSONRPC2Adapter` to `NewChannelStreamPair`, so every `jsonrpc2/common`
> row silently measured the channel transport while jrpc2/mcp common rows
> stayed on net.Pipe — the documented same-transport contract did not hold.
> Fixed in `5a0a824` (`newJSONRPC2CommonAdapter`, net.Pipe + NDJSON); the
> corrected common baseline is
> `internal-benchmark-common-corrected.txt` (void `2.910 us`/336 B/6 allocs,
> notify `1.034 us`/3 allocs, params large `9.514 us`/9 allocs). Round 5's
> recorded common-row deltas (e.g. "common void -41.58%") were channel-backed
> and are superseded. **A second harness hazard recorded for the protocol:**
> the nested benchmark module vendors `go.lsp.dev/jsonrpc2`, so plain
> `go test` builds run `-mod=vendor` against the vendored snapshot, not the
> working tree; every Round 6 spike run uses `GOWORK=off GOFLAGS=-mod=mod`
> (the same mode `bench-artifacts.sh` forces). Phase-0 numbers are valid
> because vendor == HEAD at capture. **Phase-0 anchors (arm64):** root
> `BenchmarkVoidRoundTrip` `2.914 us +/-1%`/308 B/4 allocs; channel-native
> void `1.972 us`/640 B/11 allocs; notify native `681 ns`/4 allocs; params
> large native `7.255 us`/9.36 KiB/13 allocs. Alloc census matches the
> Round-6 plan exactly (replier closure 31.5%, `&Call{}` box 30.2%,
> `incomingRequest` 28.2%, method string 9.8% of root-void objects).
> **R6-C adjudication:** params-large CPU is ~87.6% scheduling, GC tax ~6-8%
> (clone garbage: `cloneBytes` = 93.7% of alloc_space), codec < 2%, memmove
> < 0.5% — the params-codec lever (R6-E) stays closed and no new asm is
> justified (no compute-bound component >= 5% on any claim row).

> **Update — Round 6 R6-B channel ownership-handoff kill-test: KEEP
> (2026-06-12).** Raw artifact:
> `internal/benchmark/artifacts/20260612T041514Z-r6-b-ownership-kill-test`
> (`GOWORK=off GOFLAGS=-mod=mod go test -run '^$' -bench
> '<channel-native rows>' -benchmem -count=10`, vs the Phase-0 anchors above).
> The kept form replaces clone-on-queue with pooled-frame ownership transfer:
> frames are `sync.Pool`-recycled `*frameBuf` boxes composed by the sender,
> handed through the data channel, and recycled by the receiver at its next
> read ("valid until next read", the documented frameStream contract);
> `writeMu` is retained as a sender breakwater in front of the channel send.
> Gate result on the channel-native family: `RoundTripVoid` ns neutral
> (`1.973 us +/-3%` -> `1.977 us +/-1%`, p=0.362) with **11 -> 9 allocs/op,
> 640 -> 546 B/op**; `RoundTripVoidParallel` **P4 -11.31%, P12 -7.86%**;
> `RoundTripParams` small **-4.60%**, medium **-6.96%**, large **-20.83% ns,
> -51.18% B/op** (9.36 KiB -> 4.57 KiB), all params rows -2 allocs;
> `Notify` ns neutral, **4 -> 3 allocs**; `Batch` n1 -1.87%, n4 -0.58%,
> n16 -3.10%; geomean **-5.98%**; no channel row regressed. Two rejected
> forms are recorded with the artifact: a per-direction free-list channel
> (P12 +42.59%) and the pool without the sender mutex (P12 +21.93%) — both
> died of select-lock contention (`runtime.sellock` 34.5% of P12 CPU), which
> is why the breakwater mutex stays. One `-race` finding during the spike:
> the frame's length must be read before the channel send, because ownership
> transfers at the send and a fast receiver can recycle the box before the
> sender returns. Large-row note: B alone already clears the R6-C >= 10%
> large-row gate on the channel family, confirming the copy-bound thesis via
> the GC-pressure mechanism the Phase-0 profile predicted.

> **Update — Round 5 Phase 4 final docs and linux/amd64 refresh (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T113339Z-g011-linux-amd64-final`.
> The final linux/amd64 run used an isolated remote temp workspace containing
> both HEAD baseline and the current working tree, `go1.26.4 linux/amd64`, Intel
> Xeon Platinum 8481C, and `-count=10`. Internal affected rows pass the final
> claim check: jsonrpc2 is fastest by sec/op mean on every affected native/common
> row. Representative native rows: void `3.366 us` vs jrpc2 `13.103 us` and mcp
> `34.080 us`; P4 `3.868 us`; P12 `3.905 us`; params large `12.948 us` vs
> jrpc2 `96.631 us` and mcp `78.726 us`; notify `1.004 us`. Root linux rerun
> (`root-affected-rerun-benchstat-vs-head.txt`) shows no stable high-inflight
> slowdown and reduces all root affected rows from 6 to 4 allocs/op. The first
> forward-order root run is retained as evidence of load/order sensitivity, but
> the reverse-order rerun is the regression-gate source. README and the claim
> sections below now disclose the channel transport's queued-frame copy cost:
> harness small/void/notify rows allocate more than the old net.Pipe native row,
> while wall-clock and cross-library ranking improve.
> Final cleanup/review gate: `20260610T122535Z-g013-cleanup-report` found no
> slop debt, and `20260610T122546Z-g013-final-verification` passed focused
> regressions, `go test -race -count=2 .`, `go test ./...`, nested benchmark
> compile, root/nested `go vet`, and `git diff --check`. Independent final
> review returned code-reviewer **APPROVE** and architect **CLEAR** after fixing
> the earlier Conn scanner-fallback and channel-stream post-close blockers.

> **Update — Round 5 Phase 3 combined regression gate (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T111717Z-combined-regression-gate`.
> Verdict: **PASS; no survivor was reverted.** Root affected rows pass the
> combined gate: `BenchmarkVoidRoundTrip` moved `2.957 us +/-3%` ->
> `2.885 us +/-1%` (-2.45%) with `408 -> 324 B/op` and `6 -> 4
> allocs/op`; `ConnPipelinedVoidRoundTrip/Inflight1` improved -4.15%;
> Inflight8/64/256 were statistically neutral by benchstat. Internal affected
> jsonrpc2 rows all improved by wall-clock vs Phase 0: native void
> `3.047 us +/-1%` -> `1.763 us +/-1%` (-42.13%), common void -41.58%,
> params rows -30.69% to -62.57%, and notify rows -38.46% to -63.48%.
> AC-P3 passes: `internal-affected-fastest-check.txt` shows jsonrpc2 fastest
> by sec/op mean on every final affected apples-to-apples row/family
> (native/common void, P4/P12, params small/medium/large, and notify).
> Disclosure for G011 docs: `NewChannelStreamPair` preserves queued-frame
> ownership by copying, so small/void/notify internal rows allocate more than
> the old net.Pipe native rows (`RoundTripVoid` `436 -> 656 B/op`,
> `8 -> 11 allocs/op`; `Notify` `228 -> 276 B/op`, `3 -> 4 allocs/op`).
> The registered plan gates wall-clock only; allocation increases here are a
> claim-table disclosure, not a G010 revert trigger. Verification passed
> `go test -race -count=2 .`, `go test ./...`, nested benchmark compile, and
> `git diff --check`.

> **Update — Round 5 Phase 2 survivor implementation (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T111230Z-phase2-survivors`. Phase 2 implemented only graduated survivors. **L4** remains the
> G006 direct-unmarshal path and now has a `64 KiB` result-size fallback so large
> responses use the owned `DecodeMessage` path instead of unmarshaling on the
> read goroutine. **L1** graduated as `NewChannelStreamPair(capacity)`, a
> bounded in-memory encoded-frame stream pair with explicit `WriteFrame` copy
> ownership and concrete `frameWriter` helpers for `Conn`. The internal native
> benchmark adapter now uses `NewChannelStreamPair(1)` for `jsonrpc2/native`.
> Native-row latency evidence is mixed under local load: the first re-anchor run
> improved the Phase 0 net.Pipe native artifact (`3.047 us +/-1%` -> `1.783 us
> +/-4%`, -41.47%), while the final exact-row smoke averaged **3008.6 ns/op**
> versus the Phase 0 **3044.6 ns/op** mean. Memory cost increases in both runs
> (`436 -> 656 B/op`, `8 -> 11 allocs/op`) because the bounded channel transport
> copies queued frames to preserve ownership. Treat G010 as the binding combined
> regression gate before refreshing claim tables. **Not shipped:** killed
> L2/L5/L6/L7, killed L3 borrowed decode, and the L3
> replier-removal candidate (kept only as API-design evidence). Added tests cover
> channel round trip, queued-frame ownership, close-unblocks-write, direct
> unmarshal, and oversized-result fallback. Full claim table refresh remains in
> G011.

> **Update — Round 5 L6 inline-batch kill-test (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T110547Z-l6-inline-batch-kill-test`
> (`cd internal/benchmark && go test -run '^$' -bench
> '^BenchmarkBatch/(n1|n4|n16)/jsonrpc2/native$' -benchmem -count=10 .`,
> Go `go1.26.4` `darwin/arm64`, Apple M3 Max, HEAD
> `d97bdc76c86bf5bf91db07aa9e3368b027508a47`,
> `GOEXPERIMENT=runtimefreegc,sizespecializedmalloc,runtimesecret`). The
> disposable spike ran synchronous batch members inline and intentionally hid
> the `Async` release token, so Async spill was explicitly **not** preserved in
> this throwaway shape. The sync-only speed gate failed: `BenchmarkBatch/n4/
> jsonrpc2/native` moved only `10.37 us +/-1%` -> `10.24 us +/-1%` (-1.18%)
> and n16 moved only `35.91 us +/-1%` -> `35.21 us +/-1%` (-1.96%), both
> below the >=8% keep gate. B/op and allocs/op were unchanged on n1/n4/n16.
> Existing synchronous batch tests passed under the spike, then the spike was
> removed. Verdict: **KILL L6**; do not spend Phase 2 complexity on the
> successor-dispatch Async-spill design in this plan.

> **Update — Round 5 L3 v2 API component kill-tests (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T110026Z-l3-component-kill-tests`
> (`go test -run '^$' -bench ... -benchmem -count=10`, Go `go1.26.4`
> `darwin/arm64`, Apple M3 Max, HEAD
> `d97bdc76c86bf5bf91db07aa9e3368b027508a47`,
> `GOEXPERIMENT=runtimefreegc,sizespecializedmalloc,runtimesecret`). Two
> disposable component spikes were run on top of the L4-kept tree and then
> removed. **L3a borrowed decode: KILL.** The unsafe `toRequest` alias probe
> intentionally broke `DecodeMessage`'s owned-buffer contract, dropped root
> `BenchmarkVoidRoundTrip` only **4 -> 3 allocs/op** (-1, not the required -2),
> and left the large row's B/op and allocs unchanged (`12.46 KiB`, `11
> allocs/op`) despite a noisy `RoundTripParams/large/jsonrpc2/native` ns/op
> improvement (`16.07 us +/-12%` -> `13.64 us +/-37%`). **L3b replier
> removal: KEEP as a candidate signal only.** A noncapturing typed-state replier
> proxy proved the captured reply closure is a one-allocation lever: root void
> round trip moved **4 -> 3 allocs/op** with statistically neutral ns/op
> (`4.742 us +/-10%` -> `4.503 us +/-6%`), but B/op regressed **324 -> 340**
> because reply state moved into `incomingRequest`, and the large row stayed at
> `11 allocs/op`. The proxy changes reply context semantics, so it was removed;
> Phase 2 must use this evidence to choose direct-return vs a real typed
> `Replier` API before shipping any L3 API break.

> **Update — Round 5 L4 direct-unmarshal kill-test (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T105401Z-l4-direct-unmarshal-kill-test`
> (`go test -run '^$' -bench ... -benchmem -count=10`, Go `go1.26.4`
> `darwin/arm64`, Apple M3 Max, HEAD
> `d97bdc76c86bf5bf91db07aa9e3368b027508a47`,
> `GOEXPERIMENT=runtimefreegc,sizespecializedmalloc,runtimesecret`). The
> spike ports the `PipelineClient` borrowed-response waiter precedent to
> bidirectional `Conn`: waiters retain the caller result destination, canonical
> numeric result responses are scanned from the borrowed frame, and the read
> goroutine unmarshals the result before the next frame can invalidate it.
> Noncanonical responses, error responses, non-frame streams, and batches keep
> the existing `DecodeMessage` fallback. Gate result: **KEEP L4**. Root
> `BenchmarkVoidRoundTrip` dropped **6 -> 4 allocs/op** and **408 -> 324 B/op**;
> `benchstat` reports `2.977 us +/-2%` -> `2.934 us +/-4%` (**-1.46%**,
> `p=0.035`), with mean **2960.3 -> 2907.1 ns/op** (-1.80%). Guard rows
> passed: `ConnPipelinedVoidRoundTrip/Inflight64` was neutral/improved
> (`306.0 us +/-5%` -> `304.4 us +/-5%`, mean -1.13%, allocs **451 -> 322**)
> and `RoundTripParams/large/jsonrpc2/native` regressed only **+1.20%** by
> benchstat (+1.24% mean), below the +5% guard. The kept implementation adds
> `TestConnReadNextDirectUnmarshalResult`; verification passed `go test ./...`,
> the targeted concurrency/leak/close tests, nested benchmark compile, and
> `git diff --check`.

> **Update — Round 5 L7 full-semantics direct-mode kill-test (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T104901Z-l7-direct-mode-kill-test`
> (`go test -run '^$' -bench ... -benchmem -count=10`, Go `go1.26.4
> darwin/arm64`, Apple M3 Max, HEAD
> `d97bdc76c86bf5bf91db07aa9e3368b027508a47`,
> `GOEXPERIMENT=runtimefreegc,sizespecializedmalloc,runtimesecret`). The
> existing benchmark-local `directStream` row already provides the required
> full-semantics direct-mode spike: it bypasses `net.Pipe` but still uses the
> ordinary bidirectional `Conn`, handler, replier, encode, decode, and read
> goroutine paths. It measured **1925.3 ns/op mean** (`benchstat`: `1.936 us
> ±4%`, `901 B/op`, `15 allocs/op`), above the pre-registered <1000 ns gate.
> Root `BenchmarkSyncClientVoidRoundTrip` was **2045.4 ns/op mean**, and the
> `BenchmarkNDJSONStreamRoundTrip` stream-only floor was roughly **256.6 ns/op
> mean** but is not full RPC semantics. Reentrancy/deadlock observation: the
> direct row preserves the normal `Conn` Async/reentrancy contract because only
> the byte transport is replaced. Verdict: **KILL L7**; no production or
> disposable source file was needed.

> **Update — Round 5 L5 scanString IndexByte kill-test (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T103650Z-l5-scanstring-kill-test`
> (`go test -run '^$' -bench ... -benchmem -count=10`, Go `go1.26.4
> darwin/arm64`, Apple M3 Max, HEAD
> `d97bdc76c86bf5bf91db07aa9e3368b027508a47`,
> `GOEXPERIMENT=runtimefreegc,sizespecializedmalloc,runtimesecret`). The best
> spike kept the original scalar `scanString` for ordinary keys/short strings
> and routed only `scanContainer` string spans through a 256-byte length-gated
> `bytes.IndexByte` scanner with backward backslash parity and an escaped-quote
> fallback. Parser-only large rows were excellent
> (`DecodeEnvelope/LargeParams64KiB` **-87.41%**, `LargeParams1MiB`
> **-90.83%**), and the escape-dense probe did not regress. However the
> pre-registered full-RPC gate failed: `BenchmarkRoundTripParams/large/
> jsonrpc2/native` was statistically neutral/regressive (`8.449 us ±1%` ->
> `8.462 us ±2%`, summary mean +1.44%, not the required >=5% win). Small
> parser guards also regressed (`DecodeEnvelope/Call` +3.12%, `StringID`
> +2.13%). Verdict: **KILL L5 for this plan**; do not ship the IndexByte
> scanner unless a future workload targets parser-only large-frame decoding
> with an explicit small-row/code-layout mitigation. The temporary code was
> removed after artifact capture.

> **Update — Round 5 L2 spin-before-park kill-test (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T103048Z-l2-spin-kill-test`
> (`go test -run '^$' -bench ... -benchmem -count=10`, Go `go1.26.4
> darwin/arm64`, Apple M3 Max, HEAD
> `d97bdc76c86bf5bf91db07aa9e3368b027508a47`,
> `GOEXPERIMENT=runtimefreegc,sizespecializedmalloc,runtimesecret`). A
> temporary `Conn.Call` non-blocking receive spin with
> `JSONRPC2_L2_SPIN_ITERS=4096` achieved the hit-rate criterion
> (`BenchmarkVoidRoundTrip` `benchstat`: `99.91%` spin-hit), but **did not**
> meet the latency gate: root void round-trip was statistically unchanged
> (`2.918 us ±2%` baseline vs `2.915 us ±3%` spike; summary mean +0.45%,
> not the required >=8% win). The high-inflight guard failed badly:
> `BenchmarkConnPipelinedVoidRoundTrip/Inflight64` regressed **+38.24%**
> (`323.1 us ±9%` -> `446.7 us ±34%`). Verdict: **KILL L2** and do not
> implement spin-before-park in production; the temporary code was removed after
> artifact capture.

> **Update — Round 5 L1 channel-transport kill-test (2026-06-10).**
> Raw artifact:
> `internal/benchmark/artifacts/20260610T102705Z-l1-channel-kill-test`
> (`cd internal/benchmark && go test -run '^$' -bench
> '^BenchmarkL1HandoffProbe$' -benchmem -count=10 .`, Go `go1.26.4
> darwin/arm64`, Apple M3 Max, HEAD
> `d97bdc76c86bf5bf91db07aa9e3368b027508a47`,
> `GOEXPERIMENT=runtimefreegc,sizespecializedmalloc,runtimesecret`). The
> conservative buffered ownership handoff row was **441.7 ns/op mean**
> (`benchstat`: `441.3n ±1%`, `0 B/op`, `0 allocs/op`), below the
> pre-registered ~800 ns gate; the clone fallback row was **498.6 ns/op mean**
> (`96 B/op`, `2 allocs/op`). The `net.Pipe` no-JSON control measured
> **1208.9 ns/op mean**. Verdict: **KEEP L1 as a survivor candidate**, but only
> with an explicit frame-ownership or copy boundary; the disposable probe file
> was removed after artifact capture, so production code is unchanged at this
> stage.

> **Update — stdio-shaped transport probe (2026-06-06).** The Phase 5 transport
> corpus now includes a full-duplex `os.Pipe` pair, so LSP/MCP-style stdio
> workloads are measured separately from `net.Pipe`, Unix sockets, and TCP. Raw
> artifact:
> `internal/benchmark/artifacts/20260606T002700Z-ospipe-transport` (`--bench
> 'Benchmark(TransportOSPipe|WritevFrameProbe)' --count 3 --no-profiles --
> -benchtime=20x`, Go `go1.26.4 darwin/arm64`, Apple M3 Max, committed HEAD captured in
> `env.txt`). The smoke result is not a
> final performance claim, but it preserves the decision signal: `os.Pipe`
> keeps `writes/call` pinned at `1.000` through inflight `{1,8,64,256}` while
> reads coalesce down to roughly `0.02-0.09 reads/call` at high inflight; the
> `net.Buffers` probe on `os.Pipe` falls back to multiple writes and shows
> `2 allocs/op`, so it is not a stdio default candidate without stronger
> evidence.

> **Update — envelope selector dependency bakeoff (2026-06-06).** Phase 6 now
> has a benchmark-only selector probe for `fastjson`, `jsonparser`, and `gjson`
> against this package's borrowed `ScanMessageView` path. Raw artifact:
> `internal/benchmark/artifacts/20260606T003400Z-envelope-selector-probe`
> (`--bench 'BenchmarkEnvelopeSelectorProbe' --count 5 --no-profiles --
> -benchtime=1000x`, committed HEAD captured in `env.txt`). This is not a
> production dependency adoption: it measures top-level single-message envelope
> classification/extraction only, excludes batch arrays, and keeps the new
> dependencies inside the nested benchmark module.

> **Update — pipelined-client fast-path probe (2026-06-06).** Phase 3 now has
> an apples-to-apples root benchmark for generic `Conn` versus the client-only
> `PipelineClient` over the same `net.Pipe` + NDJSON harness and inflight
> `{1,8,64,256}`. Raw artifact:
> `internal/benchmark/artifacts/20260606T012124Z-root-pipeline-fastpath-vs-conn`
> (`go test -run '^$' -bench
> 'Benchmark(ConnPipelinedVoidRoundTrip|PipelineClientVoidRoundTrip)$'
> -benchtime=200x -benchmem -count=10 .`, Go `go1.26.4 darwin/arm64`,
> Apple M3 Max, clean committed HEAD captured in `env.txt`). The client-only
> path matches generated numeric IDs in dense slots, scans canonical success
> responses without the full message classifier, and falls back to the borrowed
> view scanner for errors/non-canonical frames. Benchstat showed ns/op wins at
> inflight `1` (`-12.0%`), `8` (`-8.6%`), and `64` (`-21.0%`), with inflight
> `256` statistically neutral; allocation counts dropped from
> `6/57/451/1810` to `4/41/322/1296 allocs/op`, and bytes fell by roughly
> `18%` geomean. Quote this only with the mode disclosure: `PipelineClient` is
> client-driven and does not dispatch server-initiated requests; `Conn`/`Peer`
> remain the bidirectional mode.

> **Update — current-head pipelined-client evidence (2026-06-06).** The final
> current-head pipeline evidence supersedes the earlier `20260606T012124Z` probe
> for quoted numbers. Arm64 raw artifact:
> `internal/benchmark/artifacts/20260606T025048Z-root-pipeline-lazy-base-current-head`
> (`go test -run '^$' -bench
> 'Benchmark(ConnPipelinedVoidRoundTrip|PipelineClientVoidRoundTrip)$' -benchmem
> -count=10 -cpuprofile ... -memprofile ... .`, clean HEAD
> `40f2a9ed855cd4ba2810ab70b1b9da7b068f5036`, Go `go1.26.4
> darwin/arm64`, Apple M3 Max). Benchstat: inflight `1` −7.48% ns/op,
> inflight `8` neutral (`p=0.063`), inflight `64` −13.13%, inflight
> `256` neutral (`p=0.123`), geomean −5.17%; bytes fell −18.50% geomean
> and allocs fell −29.67% geomean (`6/57/450/1804` → `4/41/321/1290`).
> Linux/amd64 raw artifact:
> `internal/benchmark/artifacts/20260606T025258Z-linux-amd64-root-pipeline-lazy-base-current-head`
> (remote host `debian-13-trixie-mnx1`, Go `go1.26.3 linux/amd64`, Intel
> Xeon Platinum 8481C, clean same HEAD). Benchstat: inflight `1` −9.95%,
> `8` −8.79%, `64` −5.98%, `256` +3.45%, geomean −5.46%; bytes and
> allocs still fall at every inflight level. Therefore quote `PipelineClient` as
> a client-only geomean latency win and all-level allocation/byte win, with an
> explicit high-inflight latency caveat, **not** as a strict latency win for
> every inflight level on every host.

> **Update — final-head queued-writer pipelined-client evidence (2026-06-06).**
> The reusable queued writer supersedes the earlier high-inflight caveat for
> `PipelineClient`. It drains concurrent call bursts through one writer goroutine
> instead of letting every caller contend on the stream write mutex, while keeping
> the single-call path direct. Arm64 raw artifact:
> `internal/benchmark/artifacts/20260606T050739Z-darwin-arm64-pipeline-final-head`
> (`go test -run '^$' -bench
> 'Benchmark(ConnPipelinedVoidRoundTrip|PipelineClientVoidRoundTrip)$' -benchmem
> -count=10 .`, final publication source HEAD captured in `env.txt`, Go
> `go1.26.4 darwin/arm64`, Apple M3 Max). The artifact's
> `benchstat-normalized.txt` is the exact source of truth; the rounded summary is
> strict latency wins at inflight `1/8/64/256` (roughly `5%`, `17%`, `30%`, and
> `21%`) with allocation-count reductions at every level (`6/57/451/1810` →
> `4/43/331/1323`). Bytes/op fall by geomean, with `Inflight64`/`Inflight256`
> B/op increases.
> Linux/amd64 raw artifact:
> `internal/benchmark/artifacts/20260606T050943Z-linux-amd64-pipeline-final-head`
> (remote host `debian-13-trixie-mnx1.asia-northeast1-c.gaudiy-platform`, Go
> `go1.26.3 linux/amd64`, Intel Xeon Platinum 8481C, same clean source archive
> under `/tmp`). Its `benchstat-normalized.txt` is the exact source of truth; the
> rounded summary is strict latency wins at inflight `1/8/64/256` (roughly `8%`,
> `20%`, `30%`, and `23%`) with allocation-count reductions at every level
> (`6/57/450/1806` → `4/42/330/1315`). Bytes/op fall by geomean, with
> `Inflight64`/`Inflight256` B/op increases. Quote `PipelineClient` as a
> client-only strict latency and allocation-count win across inflight
> `{1,8,64,256}` for these two hosts; still report bytes/op per row because the
> queued writer intentionally trades a few high-inflight bytes for lower wall
> time.

> **Update — borrowed-view decode status (2026-06-06).** The historical
> borrowed-view decode artifact is
> `internal/benchmark/artifacts/20260606T040136Z-borrowed-view-decode-current-head`
> (`go test -run '^$' -bench
> 'BenchmarkDecode(Envelope|ViewEnvelope|RequestViewsEnvelope|AppendRequestViewsEnvelope)$'
> -benchmem -count=10 .`, clean parser/test HEAD
> `445ff15a8b73a6c4b18c76454a1f7d535eca3be6`, Go `go1.26.4
> darwin/arm64`, Apple M3 Max). The zero-copy view paths eliminate allocations
> on the comparable rows, but they do **not** satisfy the plan's universal
> borrowed-decode speed gate. `ScanMessageView` is slower than `DecodeMessage` on
> the common small rows (`Call` +15.72%, `StringID` +20.73%, `Notification`
> +27.56%, `Response` +41.09%) and on invalid rows (`InvalidJSON` +70.96%,
> `InvalidJSONRPC` +136.11%); it wins large-payload rows (`LargeParams64KiB`
> -10.68%, `LargeParams1MiB` -8.61%). `AppendRequestViews` posts a small
> geomean ns/op win (-1.77%) and zero allocations, but still loses `Call`,
> `StringID`, `Notification`, `NestedMethodInParams`, duplicate-field, and
> invalid rows. Therefore AC-P3's fallback applies: borrowed views remain
> experimental, parser-only surfaces and are **not** promoted as the default
> replacement for `DecodeMessage` or `ParseRequests`. Correctness is covered by
> `TestScanMessageView_DifferentialCorpus`, which compares the borrowed scanner
> with the owned decode/parser oracles over malformed, escaped, duplicate, batch,
> and large-payload corpus rows.

## Libraries under test

| Label | Module | Notes |
|-------|--------|-------|
| `jsonrpc2`  | `go.lsp.dev/jsonrpc2` (this repo, via replace) | The library being benchmarked. |
| `jrpc2` | `github.com/creachadair/jrpc2` v1.3.5 | Mature, widely used; the reference for `BenchmarkRoundTrip` / `BenchmarkParseRequests`. |
| `mcp`   | vendored `mcpref/` (Go MCP SDK's jsonrpc2, gopls-derived) | Vendored at `internal/benchmark/mcpref`. |

## Synchronous-client mode (A1c)

`SyncClient` owns its read loop (no client-side background reader); each `Call`
writes the request and reads its own response on the caller's goroutine,
collapsing the dedicated-reader-to-`Call` hand-off (the third goroutine hop) that
the concurrent `Conn` pays. Measured on `net.Pipe` + NDJSON against an ordinary
`Conn` server (arm64, M3 Max, root benchmark, `-count=8`):

| Path | ns/op | B/op | allocs/op |
|------|------:|-----:|----------:|
| `BenchmarkVoidRoundTrip` (`Conn`, concurrent) | ~2900 | 408 | 6 |
| `BenchmarkSyncClientVoidRoundTrip` | **~2030** | 408 | 6 |

**Mode disclosure (do not quote the number without it).** `SyncClient` removes the
*client's* reader goroutine. It cannot receive server-initiated requests and
serializes its calls (one outstanding at a time). The win is therefore an
execution-model change, reported as a distinct `jsonrpc2/sync` row exactly as the
batch rows are excluded from the strict apples-to-apples claim. It is **not**
comparable head-to-head with jrpc2's `channel.Direct` (which keeps a client-side
reader), and a lower number here does **not** mean the bidirectional `Conn` got
faster. The feasibility premise in the planning doc ("reuse the `Async`
successor-reader handoff") was incorrect — that handoff moves the single reader
role between goroutines and is not a caller-pump primitive — so `SyncClient` is a
separate, additive type that never starts a background reader, leaving the
existing `Conn` untouched.

## Environment

| Field | Value |
|-------|-------|
| GOOS / GOARCH | `darwin` / `arm64` |
| CPU | Apple M3 Max |
| Go toolchain | `go1.26.4` |
| Aggregation | `benchstat` over `-count=8` (median ± p-range) |
| Command | `go -C internal/benchmark test -run=^$ -bench=. -benchmem -count=8 ./... | tee internal/benchmark/bench.txt` |

> **`fastest` CLAIM arch is amd64 — now measured.** The detailed tables in the
> later sections are local `darwin/arm64` (Apple Silicon). The headline "fastest"
> claim is anchored to `amd64`, and has been **measured directly** on an amd64
> server — see [amd64 results](#amd64-results-the-fastest-claim-arch) immediately
> below. The arm64 tables remain a truthful secondary baseline.

> **amd64 source of truth: a direct amd64 server measurement.** The full
> comparative harness (`go -C internal/benchmark test -run='^$' -bench=. -benchmem
> -count=10 ./...`, storing `bench_amd64.txt`) was run directly on an `amd64`/linux
> server — Intel Xeon Platinum 8481C, Debian 13, `go1.26.4` — see
> [amd64 results](#amd64-results-the-fastest-claim-arch) below. That measured run,
> not this developer machine, is the source of truth for the amd64 "fastest"
> claim. The rival dependencies (jrpc2, mds, segmentio/encoding, x/sync) are
> resolved via `go mod download`; they live only in `internal/benchmark/go.mod`
> and never enter the root module graph. The tables in this document remain the
> local `arm64` baseline and are labeled as such.

## amd64 results (the `fastest` claim arch)

Measured directly on an isolated linux/amd64 server workspace created from the
current working tree. **`jsonrpc2` is fastest by sec/op mean on every affected
apples-to-apples native/common row** in `internal-affected-fastest-check.txt`.
Batch remains excluded from the strict claim because batch mechanics differ by
library.

| Field | Value |
|-------|-------|
| GOOS / GOARCH | `linux` / `amd64` |
| CPU | Intel Xeon Platinum 8481C @ 2.70 GHz (GCE c3, 44 vCPU) |
| OS / kernel | Debian 13 (trixie), Linux 6.12 |
| Go toolchain | `go1.26.4` |
| Aggregation | `benchstat` over `-count=10` |
| Raw data | `internal/benchmark/artifacts/20260610T113339Z-g011-linux-amd64-final/` |

### Root Conn affected rows vs HEAD baseline

The ordinary root benchmarks measure `Conn` over stream transports, independent
of the comparative harness's channel-native adapter. The stable reverse-order
rerun is the regression-gate source; the earlier forward-order run is retained in
the artifact as load/order-sensitivity evidence.

| Row | HEAD baseline | Final | Result |
|---|---:|---:|---|
| `BenchmarkVoidRoundTrip` | 4.827 us / 409 B / 6 allocs | **4.744 us / 325 B / 4 allocs** | sec/op neutral, allocs lower |
| `ConnPipelined/Inflight1` | 4.878 us / 409 B / 6 allocs | **4.849 us / 324.5 B / 4 allocs** | sec/op neutral, allocs lower |
| `ConnPipelined/Inflight8` | 58.51 us / 3.722 KiB / 57 allocs | **57.61 us / 3.063 KiB / 41 allocs** | -1.54%, allocs lower |
| `ConnPipelined/Inflight64` | 533.5 us / 29.89 KiB / 450 allocs | **530.9 us / 24.58 KiB / 322 allocs** | sec/op neutral, allocs lower |
| `ConnPipelined/Inflight256` | 2.058 ms / 122.7 KiB / 1807 allocs | **2.016 ms / 100.7 KiB / 1291 allocs** | sec/op neutral, allocs lower |

### Comparative affected rows (native transport)

| Workload | jsonrpc2 | jrpc2 | mcp | Winner |
|----------|------:|------:|------:|--------|
| Void round trip | **3.366 us / 657 B / 11 allocs** | 13.103 us / 4478 B / 100 | 34.080 us / 67980 B / 46 | jsonrpc2 |
| Parallel P4 | **3.868 us / 658 B / 11 allocs** | 13.224 us / 4486 B / 100 | 31.237 us / 67896 B / 44 | jsonrpc2 |
| Parallel P12 | **3.905 us / 658 B / 11 allocs** | 13.827 us / 4488 B / 100 | 29.927 us / 67835 B / 44 | jsonrpc2 |
| Params small | **3.553 us / 809 B / 13 allocs** | 14.293 us / 4883 B / 108 | 33.902 us / 68279 B / 50 | jsonrpc2 |
| Params medium | **3.984 us / 1210 B / 13 allocs** | 18.889 us / 5678 B / 109 | 36.558 us / 69484 B / 50 | jsonrpc2 |
| Params large | **12.948 us / 9608 B / 13 allocs** | 96.631 us / 22530 B / 109 | 78.726 us / 97636 B / 53 | jsonrpc2 |
| Notify | **1.004 us / 276 B / 4 allocs** | 4.255 us / 2072 B / 42 | 12.224 us / 33831 B / 20 | jsonrpc2 |

### Comparative affected rows (common transport)

| Workload | jsonrpc2 | jrpc2 | mcp | Winner |
|----------|------:|------:|------:|--------|
| Void round trip | **3.333 us / 657 B / 11 allocs** | 18.867 us / 4572 B / 102 | 33.993 us / 67973 B / 46 | jsonrpc2 |
| Parallel P4 | **3.863 us / 657 B / 11 allocs** | 21.192 us / 4583 B / 102 | 30.503 us / 67888 B / 44 | jsonrpc2 |
| Parallel P12 | **3.832 us / 658 B / 11 allocs** | 21.241 us / 4583 B / 102 | 29.743 us / 67832 B / 44 | jsonrpc2 |
| Params small | **3.537 us / 809 B / 13 allocs** | 21.114 us / 4974 B / 109 | 34.595 us / 68278 B / 50 | jsonrpc2 |
| Params medium | **3.993 us / 1210 B / 13 allocs** | 26.891 us / 5762 B / 110 | 36.348 us / 69487 B / 50 | jsonrpc2 |
| Params large | **12.890 us / 9608 B / 13 allocs** | 131.386 us / 28050 B / 111 | 79.128 us / 97618 B / 53 | jsonrpc2 |
| Notify | **1.021 us / 276 B / 4 allocs** | 5.596 us / 2119 B / 43 | 12.395 us / 33831 B / 20 | jsonrpc2 |

### amd64 verdict

- **Wall-clock claim:** MET on all affected apples-to-apples rows; jsonrpc2 is
  fastest in both native and common families.
- **Root allocation floor:** MET; ordinary `Conn` root rows drop from 6 to 4
  allocs/op and reduce B/op across inflight levels.
- **Disclosure:** harness small/void/notify rows allocate more than the old
  net.Pipe-native baseline because `NewChannelStreamPair` copies queued frames
  to preserve ownership. This is intentionally reported, not hidden.

## Transport families (Phase 6 §6 integrity protocol)

Two transport families are measured so the comparison cannot be gamed by giving
one implementation a hidden shortcut:

- **`native`** — the fastest in-memory transport each library natively offers.
  - `jsonrpc2`: `NewConn` over `NewChannelStreamPair(1)`, a bounded in-memory
    encoded-frame channel stream. It still encodes JSON, frames messages, scans
    frames, decodes JSON, and copies queued frames to preserve caller buffer
    ownership.
  - `jrpc2`: `server.NewLocal` (`channel.Direct`), which passes message buffers
    **in memory with no framing and no encoding**. This is jrpc2's fastest
    transport and remains less work than jsonrpc2's encoded-frame channel stream.
  - `mcp`: `mcpref.NewConnection` with an NDJSON `Reader`/`Writer` (implemented
    in `adapters.go`) over `net.Pipe` — its only in-memory option.
- **`common`** — all three libraries over the **same** `net.Pipe` pair with
  NDJSON-style framing (one goroutine per endpoint): `jsonrpc2` via
  `NewNDJSONStream`, `jrpc2` via `channel.RawJSON`, `mcp` via the NDJSON
  Reader/Writer. No library gets an in-memory channel here.

Every adapter answers a single no-op `"void"` method (matching jrpc2's
`BenchmarkRoundTrip`) and each adapter constructor runs a sanity void
call+notify round-trip so a rigged instant no-op cannot falsify timings.
`equiv_test.go` further asserts (via `go-cmp`) that all three decoders extract
the **same** method name and params bytes from identical input, proving the
pure-decode benchmarks compare equivalent work.

The channel stream's queued-frame copy is visible in the harness allocation
counts. That tradeoff is accepted because it preserves the package's single-owner
buffer contract while removing `net.Pipe` rendezvous latency.

### Faithfulness of the harness (why these numbers are not an artifact)

- **`jsonrpc2` calibration.** The root repository's own
  `BenchmarkVoidRoundTrip` remains the ordinary bidirectional `Conn` benchmark
  over stream transports. It is the allocation-floor calibration for production
  `Conn`: Round 5 reaches **4 allocs/op** and lower B/op on both darwin/arm64 and
  linux/amd64. The comparative harness's `jsonrpc2/native` adapter is now
  deliberately different: it uses `NewChannelStreamPair(1)` so native in-memory
  transport claims compare each library's fastest in-memory option. This is not
  a hidden shortcut; the channel stream still encodes, frames, scans, decodes,
  and copies queued frames for ownership. The `common` family keeps jsonrpc2 on
  `net.Pipe` + NDJSON when a same-transport comparison is required.
- **`jrpc2` is measured through its own canonical setup.** `jrpc2/native` is
  literally jrpc2's `BenchmarkRoundTrip` configuration: `server.NewLocal` with
  `ServerOptions{DisableBuiltin:true, Concurrency:1}` (the `C01-B` case). The
  decode rows call `jrpc2.ParseRequests` verbatim on identical bytes. No
  distorting wrapper sits between the timer and jrpc2's public API.
- **`mcp` is measured through its public API.** The mcp adapter calls
  `mcpref.NewConnection` / `Call` / `Notify` / `DecodeMessage` / `EncodeMessage`
  directly; the only harness code is the NDJSON `Reader`/`Writer`, constructed
  once per connection (not per call).

This is what makes "jsonrpc2 wins even though jrpc2 was handed its zero-framing
`channel.Direct` transport on the native path" a defensible claim rather than a
wrapper artifact.

## Headline: current darwin/arm64 affected rows

These rows are the local Apple M3 Max combined-gate evidence from
`internal/benchmark/artifacts/20260610T111717Z-combined-regression-gate`.
The linux/amd64 tables above are the claim-arch source of truth.

| Workload | jsonrpc2 | jrpc2 | mcp | Winner |
|---|---:|---:|---:|---|
| Void round trip, native | **1.766 us / 656 B / 11 allocs** | 7.735 us / 4469 B / 100 | 17.257 us / 67820 B / 46 | jsonrpc2 |
| Void round trip, common | **1.783 us / 656 B / 11 allocs** | 9.845 us / 4565 B / 102 | 18.806 us / 67819 B / 46 | jsonrpc2 |
| Parallel P4, native | **2.900 us / 657 B / 11 allocs** | 6.479 us / 4479 B / 100 | 20.594 us / 67869 B / 45 | jsonrpc2 |
| Parallel P12, native | **3.311 us / 657 B / 11 allocs** | 8.041 us / 4485 B / 100 | 20.841 us / 67839 B / 45 | jsonrpc2 |
| Params small, native | **2.019 us / 808 B / 13 allocs** | 9.858 us / 4878 B / 108 | 22.047 us / 68272 B / 51 | jsonrpc2 |
| Params medium, native | **2.294 us / 1209 B / 13 allocs** | 12.494 us / 5673 B / 109 | 23.123 us / 69523 B / 51 | jsonrpc2 |
| Params large, native | **6.929 us / 9602 B / 13 allocs** | 63.748 us / 22600 B / 109 | 46.583 us / 98149 B / 54 | jsonrpc2 |
| Notify, native | **0.605 us / 276 B / 4 allocs** | 2.612 us / 2068 B / 42 | 7.110 us / 33801 B / 20 | jsonrpc2 |

Root `BenchmarkVoidRoundTrip` on this host is **2.885 us / 324 B / 4 allocs**;
`ConnPipelinedVoidRoundTrip` high-inflight rows were statistically neutral in
the combined gate while B/op and allocs/op fell.

## Round-trip with params, parallel, notify, and batch

The current affected params/parallel/notify tables are covered above for both
linux/amd64 and darwin/arm64. Batch rows were not a Round 5 survivor and remain
excluded from strict apples-to-apples claims because each library exposes a
different batch API/transport shape. Historical batch evidence remains useful for
workload sizing, but it is not part of the final Round 5 claim table.

## AC-P2 anchor: pure DECODE on identical bytes (NO transport)

Inputs mirror `jrpc2` `BenchmarkParseRequests` (`Minimal`, `Medium`, `Batch`).
`jsonrpc2` uses `DecodeMessage` for single messages and `ParseRequests` for the
batch; `jrpc2` uses `ParseRequests` for all; `mcp` uses `DecodeMessage`
(single-message only — it cannot parse a batch array, so the `Batch/mcp` row is
absent by design).

| Input | jsonrpc2 ns / B / allocs | jrpc2 ns / B / allocs | mcp ns / B / allocs | Winner |
|-------|----------------------|-----------------------|---------------------|--------|
| Minimal | 99.3 / 88 / 2  | 1719 / 1504 / 31 | 4617 / 33034 / 8 | jsonrpc2 |
| Medium  | 226.1 / 192 / 3 | 3000 / 1824 / 36 | 5166 / 33147 / 9 | jsonrpc2 |
| Batch   | 1094 / 1032 / 25 | 9562 / 7256 / 144 | n/a | jsonrpc2 |

### ParseRequests-only (jsonrpc2 vs jrpc2, batch-capable entry point on all inputs)

| Input | jsonrpc2 ns / B / allocs | jrpc2 ns / B / allocs | Winner |
|-------|----------------------|-----------------------|--------|
| Minimal | 117.7 / 128 / 4 | 1751 / 1504 / 31 | jsonrpc2 |
| Medium  | 257.4 / 232 / 5 | 2986 / 1824 / 36 | jsonrpc2 |
| Batch   | 1117 / 1032 / 25 | 9575 / 7256 / 144 | jsonrpc2 |

### Decode allocation floor (AC-P2 / AC-P4 status)

These standalone-decode numbers are **unchanged** by the optimization pass: the
`DecodeMessage` / `ParseRequests` paths were deliberately not contorted to chase
≤ 1 alloc/op, because their callers own the returned message **indefinitely**, so
the §3.3 ownership invariant (a public `RawMessage` must never alias a pooled
buffer) forces at least one right-sized copy. The honest, achievable floors are:

- **`DecodeMessage` Minimal = 2 allocs** = the message struct (`*Call`) + the
  copied `method` string. Medium = 3 (adds the params copy). These cannot drop to
  ≤ 1 without either aliasing the caller's buffer (breaks ownership +
  `TestDecodeMessage_OwnsBuffer`) or changing the public return type. **AC-P4's
  decode ≤ 1 alloc/op target is therefore not met for the standalone API and is
  documented as infeasible without public-API damage**, per the global rule to
  state infeasibility rather than game it. (The *connection's* decode path is a
  different story — the round-trip wins decisively, see the headline.)
- **`ParseRequests` Minimal = 4 allocs** = the message struct + method copy + the
  returned `[]*ParsedMessage` slice + the `*ParsedMessage` element. The slice and
  element are mandated by the public return shape. **AC-P2's ≤ 1 alloc/op target
  is therefore not met and is documented as infeasible** without breaking the
  `ParseRequests` signature. AC-P2's *speed* target (≥ 40% lower ns/op vs jrpc2)
  is met by a wide margin (≈ 15x on Minimal: 118 ns vs 1751 ns).

No optimization was applied here precisely because the only way to hit the alloc
target would be to damage the public API or alias caller memory — both
prohibited. This is the sanctioned "skip + document the floor" outcome.

## Optimization log (hot-path allocations)

Best-effort, race-clean optimization pass on the connection hot path. Each
change was gated independently on `go test -race -count=2 .` (incl.
`TestConcurrencyStress`, `TestGoroutineLeak`, `TestSyncAsyncParity`,
`TestAsyncRunsConcurrently`, `TestDecodeMessage_OwnsBuffer`) and on the void
round-trip being no slower with allocs improved-or-equal before being kept. The
root `BenchmarkVoidRoundTrip` (nil-result, so 2 fewer allocs than the harness)
tracks the cumulative effect:

| Step | Change | root void RT allocs/op | root void RT ns/op |
|------|--------|-----------------------:|-------------------:|
| baseline | — | 18 | ~4850 |
| #4 | NDJSON `Write` appends the envelope straight into the writeMu-guarded compose buffer (drops the per-write owned `EncodeMessage` copy on both request and response writes) | 16 | ~4770 |
| #1 | Per-request context folded into `incomingRequest` (it *is* the `context.Context`), with lazy `Done()` delegating to a real `context.WithCancel(parent)` — removes the `cancelCtx` struct + `CancelFunc` closure from dispatch; zero cost on the common path that never inspects `Done` | 14 | ~4400 |
| #2 | Speculative-inline **sync** dispatch: a single (non-batch) handler runs inline on the read goroutine with no goroutine/channel; it spills to a fresh read goroutine only if it calls `Async`. Batch members keep their goroutine-per-member path. Removes the per-request `go func` + releaser channel from the sync hot path | 14* | ~2950 |
| #1b | Async token folded into `incomingRequest.Value(asyncKey{})` — removes the `context.WithValue` wrapper alloc | 13 | ~3000 |
| #3' | `readNext` returns a single `Request` for the non-batch path instead of a one-element `[]Request` slice | 12 | ~3030 |
| #2b | Releaser carries its handoff state in typed fields instead of a per-request closure (inline) / dedicated channel struct (only batch keeps the channel) | **10** | **~3010** |

\* #2's headline win is the ~33% ns/op drop from removing the goroutine handoff
latency; the allocs/op held at 14 because the goroutine stack and the channel are
not both counted as heap `allocs/op` until #2b folds the releaser. The optimizations
are listed in the order applied. PRESERVED throughout: the §3.3 single-copy
single-owner ownership invariant; `Async`/`AsyncHandler` opt-in semantics
(sync handlers observe wire order, async carry no mutual ordering once released);
the double-`Async` panic; handler-panic isolation; graceful drain on `Close`.

No benchmark gaming: there is no special-casing of the void method or `Foo.Bar`,
no skipped work the rivals perform; every change is a general algorithmic
improvement that applies to all messages (e.g. Notify also fell 12 → 5 allocs/op
from the same inline-dispatch and write-buffer changes).

### AC status (truthful)

- **AC-P1 (headline void round-trip): MET, decisively.** jsonrpc2 is ≥ 20% lower
  ns/op and ≤ the lower rival allocs/op vs *both* rivals — on linux/amd64 the
  final native void row is ~3.37 us / 11 harness allocs vs jrpc2's ~13.10 us /
  100 allocs and mcp's ~34.08 us / 46 allocs. The root `Conn` calibration is
  4 allocs/op.
- **AC-P2 (ParseRequests): speed MET (~15x faster than jrpc2); ≤ 1 alloc/op NOT
  MET (at 4)** — documented as infeasible without public-API damage (see the
  decode-floor note above).
- **AC-P3 (never slower than either rival on any benchmarked workload): MET** on
  every measured apples-to-apples workload (void RT, params S/M/L, notify, decode,
  encode); jsonrpc2 is the lowest on each.
- **AC-P4 (encode ≤ 1 alloc/op MET at 1; decode ≤ 1 alloc/op NOT MET at 2)** —
  encode meets the target; standalone decode's floor is 2 (struct + method copy)
  under the ownership invariant and is documented as infeasible without API damage.
- **AC-C3 (differential scanner fuzz): differential pass MET; 60s sustained soak
  NOT VERIFIED.** `FuzzScan` runs ~283k differential execs against an independent
  `encoding/json` oracle (`map[string]RawMessage`) and finds **no crasher and no
  divergence** in method/params/id/result classification or span extraction.
  However, it does **not** sustain a 60s soak: it executes its burst (~106k–283k
  execs depending on worker count) in the first ~3s, then the Go fuzzing engine
  stalls at **exactly 0/sec** for the remainder. This reproduces both isolated
  and at 4 workers, so it is not CPU contention. `GODEBUG=fuzzdebug=1` shows the
  flatline begins right after the engine **queues an input for minimization**
  (`DEBUG queueing input for minimization … keepCoverage:true`) and that this
  queued task only drains at shutdown, where it reports `input minimized …
  minimizing took: 0s`. That `0s` is the key qualifier: the worker is **not**
  CPU-bound inside minimization during the 0/sec window — the minimization itself
  runs instantly. The evidence therefore points to a stall **around the
  coordinator↔worker minimization handoff in `internal/fuzz`** (a queued
  minimization that is not serviced until shutdown), **not** to the scanner or the
  harness comparison logic, and **not** to slow minimization. This is strongly
  indicated but **not confirmed**: the confirming experiment (re-running with
  minimization disabled to see whether the soak is restored) was deliberately not
  run, because any fix is out of this module's scope anyway (it would require
  patching the toolchain or mutating the harness to dodge minimization, which
  would strip genuine differential coverage). The production scanner stays
  microsecond-fast on every pathological input probed (20k-deep nesting ≈ 48µs,
  200k-byte string, 50k keys) and is iterative with an explicit depth counter, so
  there is no scanner defect to root-cause; the stall is `FuzzScan`-specific (the
  higher-branch-count differential body trips the minimization path), whereas
  `FuzzRoundTrip` soaks (4.8M execs @ ~150k/sec) on the same engine/OS/session.
  **The literally-true claim is therefore: ~283k differential execs with no
  crasher or divergence; a sustained 60s soak is NOT verified (cause most likely
  in the Go fuzzing engine's minimization handoff, not the library; root cause
  indicated but unconfirmed).**
  Scanner robustness is independently established and does **not** rest on the
  soak: conformance vectors (AC-C1), the 283k differential execs that did run,
  `TestDecodeMessage_OwnsBuffer` (mutates every input byte, asserts params
  unchanged), the explicit pathological-depth/size tests, and `go test -race
  -count=2 .` all pass.

## Pure ENCODE on identical messages

`jsonrpc2.EncodeMessage` vs `mcp.EncodeMessage` on an equivalent `Foo.Bar` call with
the same ~50 B params. `jrpc2` exposes no public single-message encoder
(`EncodeMessage`/`AppendMessage` equivalent) — its wire encoding is internal to
the client/server — so the encode comparison is `jsonrpc2` vs `mcp` only.

| Library | ns/op | B/op | allocs/op | Winner |
|---------|------:|-----:|----------:|--------|
| **jsonrpc2** | **47.0** | **112** | **1** | jsonrpc2 |
| mcp  | 436.4 | 288 | 3 | — |

## Reproduction caveats

- **Offline module cache.** Benchmarks were resolved with
  `GOFLAGS=-mod=mod` and a `file://` proxy over the pre-warmed module cache.
  The cache was missing one metadata file, `creachadair/mds@v0.26.1.info`
  (the `.zip` and `.mod` were present); it was created as
  `{"Version":"v0.26.1"}` so the file proxy could resolve the version. With
  `internal/benchmark/go.mod` and `go.sum` now populated, subsequent
  `GOPROXY=off` builds and benchmarks work without it; only a fresh-cache
  `go mod tidy` would need that `.info` regenerated.
- **Root module verification.** `git diff go.mod` at the repository root equals
  the session-start snapshot (only the baseline change: `go 1.17 → go 1.26`,
  drop `segmentio/encoding`, add `go-json-experiment/json` + `go-cmp v0.6.0`).
  All benchmark dependencies (`jrpc2 v1.3.5`, `go-cmp v0.7.0`, `mds`, `x/sync`)
  live exclusively in the nested `internal/benchmark/go.mod`; `go list ./...` at
  the root does not include `benchmark` (it is a separate module). The root
  library was therefore neither modified nor pulled into a new dependency set.

## Summary

Round 5 final evidence now has two layers:

1. **Root `Conn` hot path:** direct-unmarshal response delivery reduces ordinary
   root `Conn` round trips to **4 allocs/op** and lower B/op across inflight
   levels. Darwin/arm64 high-inflight rows are neutral; the linux/amd64
   reverse-order rerun is neutral or improved on sec/op and lower on B/op plus
   allocs/op.
2. **Comparative harness claim rows:** `NewChannelStreamPair` removes the
   `net.Pipe` rendezvous from the `jsonrpc2/native` in-memory family while still
   preserving encode/frame/decode semantics and queued-frame ownership. On both
   linux/amd64 and darwin/arm64, jsonrpc2 is fastest by sec/op mean on every
   affected native/common apples-to-apples row: void, parallel void, params
   small/medium/large, and notify.

The final docs deliberately disclose the main tradeoff: channel-stream ownership
copies increase harness B/op and allocs/op for small/void/notify rows compared
with the old net.Pipe-native row. The linux/amd64 final void row is still
**3.366 us / 657 B / 11 allocs** versus jrpc2 **13.103 us / 4478 B / 100** and
mcp **34.080 us / 67980 B / 46**, so the cross-library claim remains clean.

> **Batch is excluded from the "lowest-cost on every workload" claim.** Batch
> mechanics differ per library: `jrpc2` issues a true single batch
> request/response via `Client.Batch`, `mcp` bursts N concurrent independent
> `Call`s, and `jsonrpc2` hand-frames the JSON-RPC array and reads the single
> response-array frame. Because those are not the same protocol operation, batch
> rows are **not** part of the strict apples-to-apples claim, even when jsonrpc2
> posts the lowest figures.

All quoted final numbers are artifact-scoped. Quote the artifact path, command,
Go version, GOOS/GOARCH, CPU, and transport family with any benchmark claim.

## Event-loop socket-server probe (netpoll/gnet, 2026-06-06)

This probe answers one narrow question: should `github.com/cloudwego/netpoll`
or `github.com/panjf2000/gnet/v2` be adopted as a production dependency for a
future high-connection socket-server mode? It does **not** justify a stdio, LSP
single-connection, MCP pipe, client-mode, or universal JSON-RPC speed claim.
Both packages are benchmark-only dependencies in the nested
`internal/benchmark` module; the root module does not import or require either
framework.

Artifacts:

- darwin/arm64: [`internal/benchmark/artifacts/20260606T071532Z-darwin-arm64-netpoll-gnet-server-probe`](./artifacts/20260606T071532Z-darwin-arm64-netpoll-gnet-server-probe)
- linux/amd64: [`internal/benchmark/artifacts/20260606T071707Z-linux-amd64-netpoll-gnet-server-probe`](./artifacts/20260606T071707Z-linux-amd64-netpoll-gnet-server-probe)

Raw benchmark commands and environment metadata are in each artifact's
`env.txt`, `command-tests.txt`, `command-bench.txt`, `raw.txt`, `tests.txt`,
`cpu.out`, `mem.out`, `pprof-top.txt`, and benchstat comparison files. The
linux/amd64 run used an Intel Xeon Platinum 8481C host with `go1.26.3`; the
local darwin/arm64 run used Apple M3 Max with `go1.26.4`.

### Decision

| Candidate | Decision | Evidence summary |
|-----------|----------|------------------|
| cloudwego/netpoll | **Reject for production adoption now; keep benchmark-only.** | linux/amd64 improved the TCP Conn100 latency row, but did not beat `StdNetRaw` on the required high-connection TCP Conn1000 p99 row and increased allocation counts. Benchstat for linux/amd64 reports `SocketServerLatency/TCP/Conn1000` p99 as statistically unchanged versus `StdNetRaw`, while `allocs/op` rose about 22%; close-during-inflight was about 7.6% slower and allocated about 60% more. |
| panjf2000/gnet/v2 | **Reject for production adoption now; keep benchmark-only.** | gnet reduced allocations, but linux/amd64 TCP Conn1000 was materially slower: benchstat reports about +227% `sec/op` and about +380% p99 versus `StdNetRaw`. Backpressure and close-during-inflight rows were slower than `StdNetRaw` on linux/amd64. |

### Median latency rows from the captured summaries

| Host | Row | StdNetRaw median p99 | NetpollRaw median p99 | GnetRaw median p99 |
|------|-----|---------------------:|----------------------:|-------------------:|
| linux/amd64 | TCP Conn100 | 503,773 ns | 290,779 ns | 441,968 ns |
| linux/amd64 | TCP Conn1000 | 814,547 ns | 1,525,618 ns | 3,897,668 ns |
| darwin/arm64 | TCP Conn100 | 648,333 ns | 488,625 ns | 1,220,416 ns |
| darwin/arm64 | TCP Conn1000 | 5,013,791 ns | 4,373,584 ns | 5,191,542 ns |

### Correctness and scope notes

- `TestSocketServerProbeCorrectness` verifies success responses and malformed
  frames across `StdNetRaw`, `StdNetProd`, `NetpollRaw`, and `GnetRaw` on TCP and
  Unix sockets.
- `TestSocketServerBackpressureSlowReader` verifies that a slow-reader connection
  does not prevent a fresh fast connection from completing.
- `TestSocketServerCloseDuringInFlight` verifies that a close storm does not
  prevent a surviving request from succeeding.
- `TestSocketServerShutdownRejectsNewConnections` verifies that shutdown stops
  accepting new TCP connections.
- `StdNetProd` is only a production-shaped NDJSON control row. Equivalent-work
  comparisons use `StdNetRaw` versus `NetpollRaw`/`GnetRaw` because those rows
  parse the same request bytes and emit the same canonical response bytes.
- The backpressure benchmark is a smoke probe, not an exhaustive kernel-buffer or
  queue-bound saturation proof. The CPU profiles include client and harness cost;
  a later ADR would need server-isolated profiling before proposing an optional
  socket-server mode.

Conclusion: event-loop frameworks stay out of production dependencies for this
repository. A future ADR may reopen only an optional high-connection socket
server mode if a more isolated server workload demonstrates a clear p99/CPU
benefit without backpressure or allocation regressions.
