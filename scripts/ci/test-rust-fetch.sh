#!/usr/bin/env bash
# Self-test for scripts/ci/rust-fetch.sh. Uses a synthetic local release so it
# never touches the network and can run on any host.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FETCH="$ROOT/scripts/ci/rust-fetch.sh"
ASSETS=(pipelinegen-muscles visualner mediasampler semantic-engine)
RELEASE_BINARIES=(pipelinegen-muscles visualner mediasampler semantic-engine)

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() { echo "test-rust-fetch: $*" >&2; exit 1; }

make_release() {
    local dir=$1 icu=$2 bin
    mkdir -p "$dir"
    for bin in "${RELEASE_BINARIES[@]}"; do
        if [ "$bin" = "semantic-engine" ]; then
            cat > "$dir/$bin" <<'STUB'
#!/bin/sh
cat >/dev/null
printf '%s\n' '{"schema_version":"semantic_manifest.v2","contextual_image_queries":[],"composed_image_queries":[],"important_segments":[]}'
STUB
        else
            printf '#!/bin/sh\necho %s\n' "$bin" > "$dir/$bin"
        fi
        chmod 0755 "$dir/$bin"
    done
    printf '%s\n' "$icu" > "$dir/ICU_MAJOR"
    printf 'rustc 1.99.1 (58cf8f9c3 2026-10-13)\n' > "$dir/TOOLCHAIN"
    write_release_manifest "$dir" v9.9.9
}

write_release_manifest() {
    local dir=$1 version=$2
    python3 - "$dir" "$version" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
version = sys.argv[2]
binaries = {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in
            ("pipelinegen-muscles", "visualner", "mediasampler", "semantic-engine")}
manifest = {"version": version, "git_sha": "1" * 40, "semantic_schema": "semantic_manifest.v2",
            "mediaexec_schema": "mediaexec.v1", "image_plan_schema": "image_plan.v1",
            "icu_major": int((root / "ICU_MAJOR").read_text()), "binaries": binaries}
(root / "BRAINRUST_RELEASE.json").write_text(json.dumps(manifest) + "\n")
(root / "sha256sums.txt").write_text("")
PY
( cd "$dir" && sha256sum "${RELEASE_BINARIES[@]}" BRAINRUST_RELEASE.json TOOLCHAIN ICU_MAJOR > sha256sums.txt )
}

make_stale_contract() { # <release_dir>: valid checksums, pre-v2 semantic-engine
    local dir=$1
    printf '#!/bin/sh\necho semantic-engine\n' > "$dir/semantic-engine"
    chmod 0755 "$dir/semantic-engine"
    write_release_manifest "$dir" v9.9.9
}

fetch() { # <release_dir> <bin_dir> <expected_icu>
    BRAINRUST_VERSION=v9.9.9 \
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

# 1. Happy path: every asset installed and the stamp written.
release="$tmp/release-ok"
make_release "$release" 72
bin="$tmp/bin-ok"
fetch "$release" "$bin" 72
for name in "${ASSETS[@]}"; do
    [ -x "$bin/$name" ] || fail "missing executable $name"
done
grep -qx 'version=v9.9.9' "$bin/.rust-release" || fail "stamp is missing the version"
grep -qx 'icu_major=72' "$bin/.rust-release" || fail "stamp is missing the ICU major"

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

# 6. A checksum-valid release with a pre-v2 semantic-engine must fail closed.
release="$tmp/release-stale-contract"
make_release "$release" 72
make_stale_contract "$release"
bin="$tmp/bin-stale-contract"
expect_fail "stale semantic manifest contract" fetch "$release" "$bin" 72
[ ! -e "$bin/semantic-engine" ] || fail "stale contract installed a binary"

# 7. An installed binary whose stamp matches but whose contract is stale must
# fail closed instead of being silently skipped.
printf '#!/bin/sh\nexit 0\n' > "$tmp/bin-ok/semantic-engine"
chmod 0755 "$tmp/bin-ok/semantic-engine"
expect_fail "installed stale manifest contract" fetch "$tmp/release-ok" "$tmp/bin-ok" 72

echo "✅ rust-fetch tests passed"
