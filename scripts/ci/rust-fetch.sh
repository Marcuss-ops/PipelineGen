#!/usr/bin/env bash
# rust-fetch.sh — install the pinned BrainRust release binaries into bin/.
#
# BrainRust owns the Rust toolchain, ICU linkage and the workspace test suite;
# PipelineGen consumes the checksum-verified release assets instead of building
# from the submodule. The script fails closed: every asset is verified, the
# artifact ICU major must match the runtime major, and nothing is installed
# unless every check passes.
#
# Required:
#   BRAINRUST_VERSION     release tag, e.g. v0.3.0
#   RUST_ICU_MAJOR        ICU major the binaries must link (e.g. 72)
# Optional:
#   RUST_FETCH_BASE_URL   default
#                         https://github.com/Marcuss-ops/BrainRust/releases/download
#   RUST_ARTIFACTS_DIR    checksum-verified copy of the same release assets (offline/CI cache)
#   RUST_BIN_DIR          default: bin
set -euo pipefail

ASSETS=(pipelinegen-muscles visualner mediasampler semantic-engine)
RELEASE_BINARIES=(pipelinegen-muscles visualner mediasampler semantic-engine)
METADATA=(sha256sums.txt ICU_MAJOR TOOLCHAIN BRAINRUST_RELEASE.json)

die() { echo "rust-fetch: $*" >&2; exit 1; }

# A checksum proves the bytes match the release, not that the release matches
# the current Go wire contract. v0.1.0 predates contextual_image_queries,
# composed_image_queries and important_segments; installing it silently made
# every live job fail at the first visualner scene. Fail closed before install.
verify_semantic_manifest() {
    local binary="$1"
    [ -x "$binary" ] || return 1
    local probe='{"operation":"analyze_script","language":"en","scenes":[{"scene_id":"contract","text":"Ada Lovelace was born in 1815 in London."}]}'
    local output
    output="$(printf '%s\n' "$probe" | "$binary" 2>/dev/null)" || return 1
    printf '%s' "$output" | python3 -c '
import json, sys
try:
    response = json.load(sys.stdin)
except (json.JSONDecodeError, UnicodeDecodeError):
    raise SystemExit(1)
manifest = response.get("manifest", response)
if not isinstance(manifest, dict):
    raise SystemExit(1)
schema = manifest.get("schema_version")
if schema != "semantic_manifest.v3":
    raise SystemExit(1)
scenes = manifest.get("scenes", [])
surfaces = [manifest, *scenes] if isinstance(scenes, list) else [manifest]
for field in ("contextual_image_queries", "composed_image_queries", "important_segments"):
    if not any(field in surface for surface in surfaces if isinstance(surface, dict)):
        raise SystemExit(1)
if not manifest.get("analyzers", {}).get("entity_index") or not isinstance(manifest.get("entities"), list):
    raise SystemExit(1)
' || return 1
    return 0
}

: "${BRAINRUST_VERSION:?BRAINRUST_VERSION is required (e.g. v0.3.0)}"
: "${RUST_ICU_MAJOR:?RUST_ICU_MAJOR is required (the libicu major the binaries must link)}"
RUST_FETCH_BASE_URL="${RUST_FETCH_BASE_URL:-https://github.com/Marcuss-ops/BrainRust/releases/download}"
RUST_BIN_DIR="${RUST_BIN_DIR:-bin}"

[[ "$RUST_ICU_MAJOR" =~ ^[0-9]+$ ]] || die "RUST_ICU_MAJOR must be numeric, got: $RUST_ICU_MAJOR"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required"
command -v ldconfig >/dev/null 2>&1 || die "ldconfig is required to verify the BrainRust ICU runtime ABI"
for soname in "libicuuc.so.$RUST_ICU_MAJOR" "libicui18n.so.$RUST_ICU_MAJOR"; do
    if ! ldconfig -p | awk -v expected="$soname" '$1 == expected { found = 1 } END { exit !found }'; then
        die "host runtime is missing $soname; install the libicu$RUST_ICU_MAJOR runtime package before fetching BrainRust binaries"
    fi
done

# Skip the download when this exact release is already installed and complete.
stamp="$RUST_BIN_DIR/.rust-release"
if [ -f "$stamp" ] \
    && grep -qx "version=$BRAINRUST_VERSION" "$stamp" \
    && grep -qx "icu_major=$RUST_ICU_MAJOR" "$stamp" \
    && [ -s "$RUST_BIN_DIR/BRAINRUST_RELEASE.json" ]; then
    installed=1
    for name in "${ASSETS[@]}"; do
        [ -x "$RUST_BIN_DIR/$name" ] || { installed=0; break; }
    done
    if [ "$installed" = 1 ]; then
        verify_semantic_manifest "$RUST_BIN_DIR/semantic-engine" \
            || die "installed BrainRust $BRAINRUST_VERSION semantic-engine does not emit the current manifest contract (semantic_manifest.v3 with entity_index and contextual/composed/important_segments); pin a matching release"
        python3 -c 'import json,sys; m=json.load(open(sys.argv[1])); assert m.get("version")==sys.argv[2] and m.get("mediaexec_schema")=="mediaexec.v2"' \
            "$RUST_BIN_DIR/BRAINRUST_RELEASE.json" "$BRAINRUST_VERSION" \
            || die "installed BrainRust $BRAINRUST_VERSION does not publish the mediaexec.v2 cut checksum contract; pin a matching release"
        echo "✅ BrainRust $BRAINRUST_VERSION already installed in $RUST_BIN_DIR (ICU $RUST_ICU_MAJOR)"
        exit 0
    fi
fi

tmp="$(mktemp -d)"
staging=""
cleanup() {
    rm -rf "$tmp"
    [ -z "$staging" ] || rm -rf "$staging"
}
trap cleanup EXIT

src="${RUST_ARTIFACTS_DIR:-}"
if [ -z "$src" ]; then
    command -v curl >/dev/null 2>&1 || die "curl is required to download the release assets"
    src="$tmp/release"
    mkdir -p "$src"
    base="${RUST_FETCH_BASE_URL%/}/${BRAINRUST_VERSION}"
    for asset in "${RELEASE_BINARIES[@]}" "${METADATA[@]}"; do
        if ! curl -fsSL --retry 3 --retry-delay 2 "$base/$asset" -o "$src/$asset"; then
            die "cannot download $base/$asset
  - publish the BrainRust release '$BRAINRUST_VERSION' (tag v* on BrainRust), or
  - point RUST_ARTIFACTS_DIR at a checksum-verifiable copy of the same release assets"
        fi
    done
fi

[ -d "$src" ] || die "RUST_ARTIFACTS_DIR is not a directory: $src"
for asset in "${RELEASE_BINARIES[@]}" "${METADATA[@]}"; do
    [ -f "$src/$asset" ] || die "missing release asset: $src/$asset"
done

# Every binary must be covered by sha256sums.txt: a truncated manifest must not
# silently skip a binary. Then let sha256sum verify the actual content.
manifest="$src/sha256sums.txt"
for asset in "${RELEASE_BINARIES[@]}" "${METADATA[@]:1}"; do
    awk -v name="$asset" '{ n = $2; sub(/^\*/, "", n) } n == name { found = 1 } END { exit found ? 0 : 1 }' \
        "$manifest" || die "sha256sums.txt does not cover $asset"
done
(
    cd "$src"
    sha256sum --check --strict sha256sums.txt >/dev/null
) || die "checksum verification failed for $src"

python3 - "$src/BRAINRUST_RELEASE.json" "$BRAINRUST_VERSION" "$RUST_ICU_MAJOR" "$src" <<'PY' || die "release manifest does not match the selected BrainRust version, ICU or binary digests"
import hashlib
import json
import pathlib
import sys

manifest_path, version, icu_major, directory = sys.argv[1:]
manifest = json.loads(pathlib.Path(manifest_path).read_text(encoding="utf-8"))
root = pathlib.Path(directory)
assert manifest.get("version") == version
assert manifest.get("semantic_schema") == "semantic_manifest.v3"
assert manifest.get("mediaexec_schema") == "mediaexec.v2"
assert str(manifest.get("icu_major")) == icu_major
assert manifest.get("git_sha") and len(manifest["git_sha"]) == 40
for name in ("pipelinegen-muscles", "visualner", "mediasampler", "semantic-engine"):
    actual = hashlib.sha256((root / name).read_bytes()).hexdigest()
    assert manifest.get("binaries", {}).get(name) == actual
PY

artifact_icu="$(tr -dc '0-9' < "$src/ICU_MAJOR")"
[ -n "$artifact_icu" ] || die "ICU_MAJOR in the release assets is empty"
[ "$artifact_icu" = "$RUST_ICU_MAJOR" ] || die "release links ICU $artifact_icu but this host expects ICU $RUST_ICU_MAJOR; install libicu$artifact_icu or consume a BrainRust release built for ICU $RUST_ICU_MAJOR"

mkdir -p "$RUST_BIN_DIR"
staging="$(mktemp -d "$RUST_BIN_DIR/.rust-fetch.XXXXXX")"
for asset in "${ASSETS[@]}"; do
    install -m 0755 "$src/$asset" "$staging/$asset"
done
install -m 0644 "$src/BRAINRUST_RELEASE.json" "$staging/BRAINRUST_RELEASE.json"
verify_semantic_manifest "$staging/semantic-engine" \
    || die "BrainRust $BRAINRUST_VERSION semantic-engine does not emit the current manifest contract (semantic_manifest.v3 with entity_index and contextual/composed/important_segments); update BRAINRUST_VERSION to a matching release"
{
    printf 'version=%s\n' "$BRAINRUST_VERSION"
    printf 'icu_major=%s\n' "$artifact_icu"
    printf 'git_sha=%s\n' "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["git_sha"])' "$src/BRAINRUST_RELEASE.json")"
    printf 'toolchain=%s\n' "$(tr '\n' ' ' < "$src/TOOLCHAIN" | sed 's/ *$//')"
} > "$staging/.rust-release"

# Install only after every check and every staged file succeeded.
for file in "${ASSETS[@]}" "BRAINRUST_RELEASE.json" ".rust-release"; do
    mv -f "$staging/$file" "$RUST_BIN_DIR/$file"
done
rmdir "$staging"
staging=""

echo "✅ BrainRust $BRAINRUST_VERSION installed in $RUST_BIN_DIR (ICU $artifact_icu)"
