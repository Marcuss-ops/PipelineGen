#!/usr/bin/env bash
# build-semantic-engine.sh — build the BrainRust worktree's semantic-engine and
# stamp the exact checkout it came from.
#
# Why this exists: `make build-server` consumes the checksum-verified *release*
# (scripts/ci/rust-fetch.sh), so a change in the BrainRust worktree never reaches
# bin/semantic-engine through the deploy path. The result is measurable drift:
# on 2026-11-23 the binary was from 08:40, the manifest declared a4ab4cb and
# BrainRust HEAD was 53ea982 with planner changes still uncommitted. Every Go
# test then ran against a planner nobody had edited.
#
# Modes:
#   (default)  build --release -p semantic-engine, install it atomically in
#              bin/, and rewrite bin/BRAINRUST_RELEASE.json and bin/.rust-release
#              with the real git_sha, digest, dirty flag and build time.
#   --check    verify only: the manifest sha matches the binary, the manifest
#              git_sha matches BrainRust HEAD, and no Rust source is newer than
#              the installed binary. No cargo, no toolchain required.
#
# Overrides:
#   BRAINRUST_DIR      default: <repo>/../BrainRust
#   RUST_BIN_DIR       default: <repo>/bin
#   CARGO_BIN          default: cargo
#   RUST_PROFILE       default: release
#
# Exit codes: 0 fresh/success, 1 drift or build error, 2 usage.
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "$SCRIPT_DIR/../.." && pwd)"

BRAINRUST_DIR="${BRAINRUST_DIR:-$ROOT_DIR/../BrainRust}"
RUST_BIN_DIR="${RUST_BIN_DIR:-$ROOT_DIR/bin}"
CARGO_BIN="${CARGO_BIN:-cargo}"
RUST_PROFILE="${RUST_PROFILE:-release}"
MANIFEST="$RUST_BIN_DIR/BRAINRUST_RELEASE.json"
STAMP="$RUST_BIN_DIR/.rust-release"
BINARY="$RUST_BIN_DIR/semantic-engine"

MODE="build"
ALLOW_DIRTY=0
for arg in "$@"; do
  case "$arg" in
    --check)       MODE="check" ;;
    --allow-dirty) ALLOW_DIRTY=1 ;;
    -h|--help)     sed -n '2,32p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) printf 'unknown argument: %s\n' "$arg" >&2; exit 2 ;;
  esac
done

log() { printf '[semantic-engine] %s\n' "$*"; }
die() { printf '[semantic-engine] ERROR: %s\n' "$*" >&2; exit 1; }

[ -d "$BRAINRUST_DIR" ] || die "BrainRust checkout not found at $BRAINRUST_DIR (set BRAINRUST_DIR)"
[ -d "$BRAINRUST_DIR/.git" ] || [ -f "$BRAINRUST_DIR/.git" ] \
  || die "$BRAINRUST_DIR is not a git checkout"

brainrust_head() {
  git -C "$BRAINRUST_DIR" rev-parse HEAD 2>/dev/null || true
}

rust_dirty() {
  [ -n "$(git -C "$BRAINRUST_DIR" status --porcelain -- '*.rs' '*.toml' 2>/dev/null || true)" ]
}

# Any Rust source newer than the installed binary means the binary cannot be
# attributed to the current worktree, even when HEAD matches. Test/bench/example
# sources are pruned: they are not compiled into the installed binary, so a
# newer test file is not evidence of binary drift (2026-11-23: a modified
# mediasampler test made the gate red while the binary was still HEAD-fresh).
newer_rust_source() {
  local reference="$1"
  find "$BRAINRUST_DIR" \
    -path "$BRAINRUST_DIR/target" -prune -o \
    \( -path "*/tests/*" -o -path "*/benches/*" -o -path "*/examples/*" \
       -o -name 'tests.rs' -o -name 'test_*.rs' -o -name '*_test.rs' \) -prune -o \
    \( -name '*.rs' -o -name '*.toml' \) -newer "$reference" -print -quit 2>/dev/null
}

manifest_value() {
  python3 - "$MANIFEST" "$1" <<'PY'
import json, sys
from pathlib import Path
manifest = json.loads(Path(sys.argv[1]).read_text())
value = manifest
for part in sys.argv[2].split("."):
    value = value.get(part) if isinstance(value, dict) else None
print("" if value is None else value)
PY
}

check_freshness() {
  [ -s "$MANIFEST" ] || die "missing $MANIFEST; run 'make build-rust-local'"
  [ -x "$BINARY" ] || die "missing $BINARY; run 'make build-rust-local'"

  local head declared_sha actual_sha declared_commit
  head="$(brainrust_head)"
  [ -n "$head" ] || die "cannot resolve BrainRust HEAD in $BRAINRUST_DIR"
  declared_commit="$(manifest_value git_sha)"
  declared_sha="$(manifest_value binaries.semantic-engine)"
  actual_sha="$(sha256sum "$BINARY" | cut -d' ' -f1)"

  [ "$declared_commit" = "$head" ] \
    || die "manifest declares BrainRust $declared_commit but HEAD is $head; run 'make build-rust-local'"
  [ "$declared_sha" = "$actual_sha" ] \
    || die "manifest digest $declared_sha does not match $BINARY ($actual_sha); run 'make build-rust-local'"

  local newer
  newer="$(newer_rust_source "$BINARY")"
  if [ -n "$newer" ]; then
    if [ "$ALLOW_DIRTY" = "1" ]; then
      log "WARNING: Rust source newer than the binary: $newer (allowed)"
    else
      die "Rust source newer than the binary: $newer; rebuild with 'make build-rust-local'"
    fi
  fi
  log "fresh: BrainRust $head, sha256 $actual_sha"
}

build_and_install() {
  command -v "$CARGO_BIN" >/dev/null 2>&1 \
    || die "cargo not found ($CARGO_BIN); install the pinned Rust toolchain or use 'make rust-fetch'"
  local head artifact
  head="$(brainrust_head)"
  [ -n "$head" ] || die "cannot resolve BrainRust HEAD in $BRAINRUST_DIR"

  log "build: cargo build --profile $RUST_PROFILE -p semantic-engine --locked"
  ( cd "$BRAINRUST_DIR" && "$CARGO_BIN" build --profile "$RUST_PROFILE" -p semantic-engine --locked )
  artifact="$BRAINRUST_DIR/target/$RUST_PROFILE/semantic-engine"
  [ -x "$artifact" ] || die "cargo did not produce $artifact"

  mkdir -p "$RUST_BIN_DIR"
  local staging="$BINARY.tmp.$$"
  install -m 0755 "$artifact" "$staging"
  mv -f "$staging" "$BINARY"

  local sha dirty=0
  sha="$(sha256sum "$BINARY" | cut -d' ' -f1)"
  rust_dirty && dirty=1

  python3 - "$MANIFEST" "$sha" "$head" "$dirty" <<'PY'
import datetime, json, sys
from pathlib import Path

path = Path(sys.argv[1])
manifest = json.loads(path.read_text()) if path.exists() else {}
manifest.setdefault("binaries", {})["semantic-engine"] = sys.argv[2]
manifest["git_sha"] = sys.argv[3]
manifest["source"] = "worktree"
manifest["worktree_dirty"] = sys.argv[4] == "1"
manifest["built_at"] = (
    datetime.datetime.now(datetime.timezone.utc)
    .replace(microsecond=0)
    .isoformat()
    .replace("+00:00", "Z")
)
path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
PY

  local version icu toolchain
  version="$(manifest_value version)"
  [ -n "$version" ] || version="${BRAINRUST_VERSION:-worktree}"
  icu="$(manifest_value icu_major)"
  [ -n "$icu" ] || icu="${RUST_ICU_MAJOR:-72}"
  toolchain="$(manifest_value toolchain)"
  if [ -f "$STAMP" ]; then
    icu="$(awk -F= '$1=="icu_major"{print $2}' "$STAMP" | tail -1)"
    toolchain="$(awk -F= '$1=="toolchain"{sub(/^toolchain=/,""); print}' "$STAMP" | tail -1)"
  fi
  {
    printf 'version=%s\n' "$version"
    printf 'icu_major=%s\n' "$icu"
    printf 'git_sha=%s\n' "$head"
    printf 'toolchain=%s\n' "$toolchain"
    printf 'source=worktree\n'
    printf 'worktree_dirty=%s\n' "$([ "$dirty" = "1" ] && echo true || echo false)"
  } > "$STAMP"

  log "installed: $BINARY ($sha)"
  log "manifest: BrainRust $head, worktree_dirty=$([ "$dirty" = "1" ] && echo true || echo false)"
}

if [ "$MODE" = "check" ]; then
  check_freshness
else
  build_and_install
  check_freshness
  log "done: verify with 'make verify-rust-binary'"
fi
