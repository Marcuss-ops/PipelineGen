#!/usr/bin/env bash
# Self-test for scripts/ci/rust-fetch.sh. Uses a synthetic local release so it
# never touches the network and can run on any host.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FETCH="$ROOT/scripts/ci/rust-fetch.sh"
ASSETS=(pipelinegen-muscles visualner mediasampler semantic-engine)
ALIASES=(phrase_impact scene_intelligence)

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() { echo "test-rust-fetch: $*" >&2; exit 1; }

make_release() {
    local dir=$1 icu=$2 bin
    mkdir -p "$dir"
    for bin in "${ASSETS[@]}"; do
        printf '#!/bin/sh\necho %s\n' "$bin" > "$dir/$bin"
        chmod 0755 "$dir/$bin"
    done
    ( cd "$dir" && sha256sum "${ASSETS[@]}" > sha256sums.txt )
    printf '%s\n' "$icu" > "$dir/ICU_MAJOR"
    printf 'rustc 1.99.1 (58cf8f9c3 2026-10-13)\n' > "$dir/TOOLCHAIN"
}

fetch() { # <release_dir> <bin_dir> <expected_icu>
    RUST_VERSION=v9.9.9 \
    RUST_ARTIFACTS_DIR="$1" \
    RUST_ICU_MAJOR="$3" \
    RUST_BIN_DIR="$2" \
    RUST_FETCH_BASE_URL="http://127.0.0.1:9/unused" \
        bash "$FETCH"
}

expect_fail() {
    local desc=$1
    shift
    if "$@" >"$tmp/out" 2>"$tmp/err"; then
        fail "$desc: expected failure, got success"
    fi
}

# 1. Happy path: all six names installed, stamp written, aliases are copies.
release="$tmp/release-ok"
make_release "$release" 72
bin="$tmp/bin-ok"
fetch "$release" "$bin" 72
for name in "${ASSETS[@]}" "${ALIASES[@]}"; do
    [ -x "$bin/$name" ] || fail "missing executable $name"
done
grep -qx 'version=v9.9.9' "$bin/.rust-release" || fail "stamp is missing the version"
grep -qx 'icu_major=72' "$bin/.rust-release" || fail "stamp is missing the ICU major"
cmp -s "$bin/pipelinegen-muscles" "$bin/phrase_impact" || fail "phrase_impact is not a copy"
cmp -s "$bin/pipelinegen-muscles" "$bin/scene_intelligence" || fail "scene_intelligence is not a copy"

# 2. Tampered binary: checksum failure must not install anything.
release="$tmp/release-badsum"
make_release "$release" 72
printf 'tampered\n' >> "$release/visualner"
bin="$tmp/bin-badsum"
mkdir -p "$bin"
printf 'keep\n' > "$bin/keep"
expect_fail "checksum mismatch" fetch "$release" "$bin" 72
[ ! -e "$bin/pipelinegen-muscles" ] || fail "checksum failure installed a binary"
grep -qx 'keep' "$bin/keep" || fail "checksum failure damaged the existing bin dir"

# 3. ICU mismatch: the artifact ABI must match the runtime.
release="$tmp/release-icu"
make_release "$release" 72
bin="$tmp/bin-icu"
expect_fail "ICU mismatch" fetch "$release" "$bin" 70
[ ! -e "$bin/pipelinegen-muscles" ] || fail "ICU mismatch installed a binary"

# 4. Manifest that omits a binary must fail closed.
release="$tmp/release-manifest"
make_release "$release" 72
grep -v ' visualner$' "$release/sha256sums.txt" > "$release/sha256sums.txt.tmp"
mv "$release/sha256sums.txt.tmp" "$release/sha256sums.txt"
bin="$tmp/bin-manifest"
expect_fail "manifest missing an asset" fetch "$release" "$bin" 72
[ ! -e "$bin/pipelinegen-muscles" ] || fail "incomplete manifest installed a binary"

# 5. Missing asset must fail closed.
release="$tmp/release-missing"
make_release "$release" 72
rm -f "$release/mediasampler"
bin="$tmp/bin-missing"
expect_fail "missing asset" fetch "$release" "$bin" 72
[ ! -e "$bin/pipelinegen-muscles" ] || fail "missing asset installed a binary"

echo "✅ rust-fetch tests passed"
