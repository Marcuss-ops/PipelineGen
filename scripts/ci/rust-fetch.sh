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
#   RUST_VERSION          release tag, e.g. v0.1.0
#   RUST_ICU_MAJOR        ICU major the binaries must link (e.g. 72)
# Optional:
#   RUST_FETCH_BASE_URL   default
#                         https://github.com/Marcuss-ops/BrainRust/releases/download
#   RUST_ARTIFACTS_DIR    checksum-verified copy of the same release assets (offline/CI cache)
#   RUST_BIN_DIR          default: bin
set -euo pipefail

ASSETS=(pipelinegen-muscles visualner mediasampler semantic-engine)
METADATA=(sha256sums.txt ICU_MAJOR TOOLCHAIN)
ALIASES=(phrase_impact scene_intelligence)

die() { echo "rust-fetch: $*" >&2; exit 1; }

: "${RUST_VERSION:?RUST_VERSION is required (e.g. v0.1.0)}"
: "${RUST_ICU_MAJOR:?RUST_ICU_MAJOR is required (the libicu major the binaries must link)}"
RUST_FETCH_BASE_URL="${RUST_FETCH_BASE_URL:-https://github.com/Marcuss-ops/BrainRust/releases/download}"
RUST_BIN_DIR="${RUST_BIN_DIR:-bin}"

[[ "$RUST_ICU_MAJOR" =~ ^[0-9]+$ ]] || die "RUST_ICU_MAJOR must be numeric, got: $RUST_ICU_MAJOR"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required"

# Skip the download when this exact release is already installed and complete.
stamp="$RUST_BIN_DIR/.rust-release"
if [ -f "$stamp" ] \
    && grep -qx "version=$RUST_VERSION" "$stamp" \
    && grep -qx "icu_major=$RUST_ICU_MAJOR" "$stamp"; then
    installed=1
    for name in "${ASSETS[@]}" "${ALIASES[@]}"; do
        [ -x "$RUST_BIN_DIR/$name" ] || { installed=0; break; }
    done
    if [ "$installed" = 1 ]; then
        echo "✅ BrainRust $RUST_VERSION already installed in $RUST_BIN_DIR (ICU $RUST_ICU_MAJOR)"
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
    base="${RUST_FETCH_BASE_URL%/}/${RUST_VERSION}"
    for asset in "${ASSETS[@]}" "${METADATA[@]}"; do
        if ! curl -fsSL --retry 3 --retry-delay 2 "$base/$asset" -o "$src/$asset"; then
            die "cannot download $base/$asset
  - publish the BrainRust release '$RUST_VERSION' (tag v* on BrainRust), or
  - point RUST_ARTIFACTS_DIR at a checksum-verifiable copy of the same release assets"
        fi
    done
fi

[ -d "$src" ] || die "RUST_ARTIFACTS_DIR is not a directory: $src"
for asset in "${ASSETS[@]}" "${METADATA[@]}"; do
    [ -f "$src/$asset" ] || die "missing release asset: $src/$asset"
done

# Every binary must be covered by sha256sums.txt: a truncated manifest must not
# silently skip a binary. Then let sha256sum verify the actual content.
manifest="$src/sha256sums.txt"
for asset in "${ASSETS[@]}"; do
    awk -v name="$asset" '{ n = $2; sub(/^\*/, "", n) } n == name { found = 1 } END { exit found ? 0 : 1 }' \
        "$manifest" || die "sha256sums.txt does not cover $asset"
done
(
    cd "$src"
    sha256sum --check --strict sha256sums.txt >/dev/null
) || die "checksum verification failed for $src"

artifact_icu="$(tr -dc '0-9' < "$src/ICU_MAJOR")"
[ -n "$artifact_icu" ] || die "ICU_MAJOR in the release assets is empty"
[ "$artifact_icu" = "$RUST_ICU_MAJOR" ] || die "release links ICU $artifact_icu but this host expects ICU $RUST_ICU_MAJOR; install libicu$artifact_icu or consume a BrainRust release built for ICU $RUST_ICU_MAJOR"

mkdir -p "$RUST_BIN_DIR"
staging="$(mktemp -d "$RUST_BIN_DIR/.rust-fetch.XXXXXX")"
for asset in "${ASSETS[@]}"; do
    install -m 0755 "$src/$asset" "$staging/$asset"
done
for alias in "${ALIASES[@]}"; do
    install -m 0755 "$src/pipelinegen-muscles" "$staging/$alias"
done
{
    printf 'version=%s\n' "$RUST_VERSION"
    printf 'icu_major=%s\n' "$artifact_icu"
    printf 'toolchain=%s\n' "$(tr '\n' ' ' < "$src/TOOLCHAIN" | sed 's/ *$//')"
} > "$staging/.rust-release"

# Install only after every check and every staged file succeeded.
for file in "${ASSETS[@]}" "${ALIASES[@]}" ".rust-release"; do
    mv -f "$staging/$file" "$RUST_BIN_DIR/$file"
done
rmdir "$staging"
staging=""

echo "✅ BrainRust $RUST_VERSION installed in $RUST_BIN_DIR (ICU $artifact_icu)"
