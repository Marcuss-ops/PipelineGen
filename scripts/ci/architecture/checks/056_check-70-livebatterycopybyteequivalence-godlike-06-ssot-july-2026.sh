# ── Check 70: LiveBatteryCopyByteEquivalence (godlike/06 SSOT, July 2026) ──
# Per docs/operations/stock-e2e-runbook.md §10.8, the source script
# (scripts/stock_pipeline_live_test.sh) and the registered copy
# (scripts/tests/stock_pipeline_live_test.sh) MUST be byte-identical at every
# commit. Drift detection is enforced here at pre-CI time using cmp -s
# (POSIX-portable, works on macOS/BSD/CI Linux). When they diverge:
#   1. An operator edited the copy directly (forbidden -- see §10.2).
#   2. The source was committed without a `cp -p` regen of the copy.
# Either way the registered copy is stale; CI fails fast.
#
# M2 FIX (2026-07-12, code-reviewer verdict): prior version used GNU-specific
# `sha256sum` for diagnostic hashes. On macOS/BSD operators running the script
# directly (outside the CI Linux container), sha256sum is absent. The
# portable shim below selects `sha256sum` if present, else `shasum -a 256`.
# Mirrors the portability pattern already used by Check 33 (envTimestampIsImmutable
# block) in this same script.
#
# Allowlist: NONE. SSOT has one source. Drift equals mismatch equals fail.
hash_of() {
    local f="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$f" 2>/dev/null | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$f" 2>/dev/null | awk '{print $1}'
    else
        echo "no-hash-tool-available"
    fi
}
echo "=== Check 70: LiveBatteryCopyByteEquivalence (godlike/06 SSOT, \u00a710.8) ==="
src_path="${REPO_ROOT}/scripts/stock_pipeline_live_test.sh"
copy_path="${REPO_ROOT}/scripts/tests/stock_pipeline_live_test.sh"
if [ ! -f "${src_path}" ]; then
    echo "INFO: source script absent at ${src_path} (skipping byte-equivalence check; not registered)"
elif [ ! -f "${copy_path}" ]; then
    echo "FAIL: registered copy absent at ${copy_path} but source present -- godlike/06 SSOT lockstep requires both (\u00a710.2 canonical paths)"
    echo "Fix: cp -p scripts/stock_pipeline_live_test.sh scripts/tests/stock_pipeline_live_test.sh"
    exit 1
else
    if cmp -s "${src_path}" "${copy_path}"; then
        echo "OK: source is byte-equivalent to registered copy"
    else
        src_sha=$(hash_of "${src_path}")
        copy_sha=$(hash_of "${copy_path}")
        echo "FAIL: source vs registered copy byte-divergence (godlike/06 SSOT \u00a710.8 lockstep broken)"
        echo "  source:    ${src_path}  (sha256: ${src_sha})"
        echo "  registered: ${copy_path}  (sha256: ${copy_sha})"
        echo ""
        echo "Fix: regenerate the registered copy from the source via the canonical"
        echo "      cp -p command (\u00a710.2 canonical paths):"
        echo "        cp -p scripts/stock_pipeline_live_test.sh scripts/tests/stock_pipeline_live_test.sh"
        echo "      An SSOT edit landed on the SOURCE without the regen step; commit"
        echo "      the cp -p regeneration in the SAME PR (godlike/06 lockstep discipline)."
        exit 1
    fi
fi

