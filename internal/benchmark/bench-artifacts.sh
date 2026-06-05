#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat <<'EOF'
Usage: internal/benchmark/bench-artifacts.sh [options] [-- extra go test flags]

Run the benchmark module in reproducible module mode and capture raw benchmark
plus optional pprof artifacts.

Options:
  --bench REGEX       Benchmark regex passed to go test -bench (default: .)
  --run REGEX         Test regex passed to go test -run (default: ^$)
  --count N           Count passed to go test -count (default: 10)
  --out DIR           Artifact directory (default: internal/benchmark/artifacts/<timestamp>)
  --no-benchmem       Omit -benchmem
  --no-profiles       Do not write cpu.pprof and mem.pprof
  -h, --help          Show this help

Environment:
  GOFLAGS             Preserved except any inherited -mod/--mod flag is removed;
                      the script appends -mod=mod.
  GOWORK              Forced to off for benchmark-module isolation.
  GOPROXY             Left unchanged; set GOPROXY=off for pre-warmed offline runs.
EOF
}

bench='.'
run='^$'
count='10'
out=''
benchmem=1
profiles=1
extra_args=()

while (($#)); do
	case "$1" in
	--bench)
		bench=${2:?missing value for --bench}
		shift 2
		;;
	--bench=*)
		bench=${1#--bench=}
		shift
		;;
	--run)
		run=${2:?missing value for --run}
		shift 2
		;;
	--run=*)
		run=${1#--run=}
		shift
		;;
	--count)
		count=${2:?missing value for --count}
		shift 2
		;;
	--count=*)
		count=${1#--count=}
		shift
		;;
	--out)
		out=${2:?missing value for --out}
		shift 2
		;;
	--out=*)
		out=${1#--out=}
		shift
		;;
	--no-benchmem)
		benchmem=0
		shift
		;;
	--no-profiles)
		profiles=0
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	--)
		shift
		extra_args=("$@")
		break
		;;
	*)
		printf 'unknown argument: %s\n\n' "$1" >&2
		usage >&2
		exit 2
		;;
	esac
done

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "${script_dir}/../.." && pwd)
timestamp=$(date -u +%Y%m%dT%H%M%SZ)

if [[ -z "$out" ]]; then
	out="${script_dir}/artifacts/${timestamp}"
elif [[ "$out" != /* ]]; then
	out="${repo_root}/${out}"
fi

mkdir -p "$out"

strip_mod_go_flags() {
	local skip_next=0
	local flag
	local kept=()

	for flag in ${GOFLAGS:-}; do
		if ((skip_next)); then
			skip_next=0
			continue
		fi
		case "$flag" in
		-mod | --mod)
			skip_next=1
			;;
		-mod=* | --mod=*)
			;;
		*)
			kept+=("$flag")
			;;
		esac
	done

	printf '%s\n' "${kept[*]:-}"
}

normalized_goflags=$(strip_mod_go_flags)
if [[ -n "$normalized_goflags" ]]; then
	export GOFLAGS="${normalized_goflags} -mod=mod"
else
	export GOFLAGS="-mod=mod"
fi
export GOWORK=off

cmd=(go -C "$script_dir" test -run "$run" -bench "$bench" -count "$count")
if ((benchmem)); then
	cmd+=(-benchmem)
fi
if ((profiles)); then
	cmd+=(-cpuprofile "$out/cpu.pprof" -memprofile "$out/mem.pprof")
fi
cmd+=("${extra_args[@]}" .)

{
	printf 'repo_root=%s\n' "$repo_root"
	printf 'benchmark_module=%s\n' "$script_dir"
	printf 'artifact_dir=%s\n' "$out"
	printf 'created_at_utc=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	printf '\n[git]\n'
	git -C "$repo_root" rev-parse --show-toplevel
	git -C "$repo_root" rev-parse HEAD
	git -C "$repo_root" status --short --branch
	printf '\n[go]\n'
	go version
	go env GOFLAGS GOWORK GOPROXY GOMODCACHE GOOS GOARCH
} >"$out/env.txt"

{
	printf 'GOFLAGS=%q GOWORK=%q ' "$GOFLAGS" "$GOWORK"
	printf '%q ' "${cmd[@]}"
	printf '\n'
} >"$out/command.txt"

printf 'writing benchmark artifacts to %s\n' "$out"
"${cmd[@]}" | tee "$out/bench.txt"
