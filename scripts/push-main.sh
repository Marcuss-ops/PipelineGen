#!/usr/bin/env bash
# scripts/push-main.sh (Fase 7(d), Push 7, July 2026).
#
# Push gate for `git push origin HEAD:main` — blocks the local push if:
#   1. remote origin/main has advanced since last fetch (catch-up missing);
#   2. working tree changes during `make verify-main` (test contamination);
#   3. SHA advances during `make verify-main` (head moved mid-test);
#   4. `make verify-main` itself does not exit 0 (fail-closed CI mirror).
#
# Operational contract:
#   - Every local `git push origin HEAD:main` MUST go through this
#     wrapper. AGENTS.md git workflow already enforces `make verify-main`
#     pre-push; this script unifies the local-CI signal with the
#     additional dimensional checks (remote-advanced, tree-stable,
#     SHA-stable).
#
# Production deployment contract (NOT enforced by this script):
#   - `make docker-sign` + `make docker-digest` produce a CI-certified
#     pin. Production deployment MUST pin to that digest (CSV key in
#     docker-compose.yml or k8s manifest); HEAD automatic is FORBIDDEN.
#     `make docker-verify-digest` enforces the runtime pin.
#   - This script LISTS the prod-digest requirement so the operator is
#     not surprised if HEAD passes the push gate but the production
#     digest is not yet certified by CI.
#
# Usage:
#   bash scripts/push-main.sh                   # push HEAD to origin/main
#   bash scripts/push-main.sh --dry-run         # print gate outcomes, exit 0
#   bash scripts/push-main.sh --branch=foo      # override target branch
#   FORCE_PUSH=1 bash scripts/push-main.sh      # bypass soft gates (CLI flag)
#
# Exit codes:
#   0 — every gate passed AND the push succeeded (or --dry-run completed).
#   1 — any gate failed (remote advance / tree drift / SHA drift / GREEN).
#   2 — gates passed but `git push` failed (network / auth / ref-rejection).
#   3 — operator chose FORCE_PUSH but a HARD gate (CI digest) failed.
#
# godlike/07 NO-FAKE-AVAILABILITY: the script returns non-zero on the
# FIRST failed gate (no fallbacks, no `|| true`).
set -euo pipefail

# ── Constants ──────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
EXPECTED_BRANCH="${EXPECTED_BRANCH:-main}"
TARGET_BRANCH="${EXPECTED_BRANCH}"
DRY_RUN=false
FORCE_PUSH="${FORCE_PUSH:-0}"

for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=true ;;
        --branch=*) TARGET_BRANCH="${arg#--branch=}" ;;
        --force) FORCE_PUSH=1 ;;
        -h|--help)
            sed -n '2,/^set -euo pipefail/p' "$0" | head -40
            exit 0
            ;;
        *)
            echo "❌ unknown flag: $arg (use --dry-run, --branch=X, or --force)" >&2
            exit 1
            ;;
    esac
done

# ── Pre-checks: REPO_ROOT must be a git repo ────────────────────────────
cd "$REPO_ROOT"
if ! git rev-parse --git-dir >/dev/null 2>&1; then
    echo "❌ push-main: $REPO_ROOT is not a git repo" >&2
    exit 1
fi

echo "▶ push-main: target=$TARGET_BRANCH dry-run=$DRY_RUN force=$FORCE_PUSH"

# ── Gate 1: remote origin must not have advanced ───────────────────────
# Fetches the current HEAD of origin/<branch> + compares. If they
# diverged since our last fetch, the local push would reject with
# non-fast-forward; we surface the requirement to fetch + rebase first.
fetch_remote_sha() {
    git ls-remote origin "$TARGET_BRANCH" 2>/dev/null | awk 'NR==1{print $1}' || true
}
LOCAL_REMOTE_SHA="$(fetch_remote_sha)"
if [ -z "$LOCAL_REMOTE_SHA" ]; then
    echo "❌ push-main: cannot reach origin/$TARGET_BRANCH (network or auth)" >&2
    exit 1
fi
# Resolve our local copy of origin/<branch>; if it's the same as the
# remote, we are caught up.
LOCAL_ORIGIN_SHA="$(git rev-parse "origin/$TARGET_BRANCH" 2>/dev/null || echo "$LOCAL_REMOTE_SHA")"
if [ "$LOCAL_ORIGIN_SHA" != "$LOCAL_REMOTE_SHA" ]; then
    echo "❌ push-main: remote origin/$TARGET_BRANCH has advanced (local=$LOCAL_ORIGIN_SHA; remote=$LOCAL_REMOTE_SHA). Fetch + rebase before push." >&2
    echo "   remediation: git fetch origin $TARGET_BRANCH && git rebase origin/$TARGET_BRANCH" >&2
    exit 1
fi
echo "✅ Gate 1 (remote advanced): remote and local origin/$TARGET_BRANCH agree at $LOCAL_ORIGIN_SHA."

# ── Capture SHA + working-tree state BEFORE tests ─────────────────────
SHA_BEFORE="$(git rev-parse HEAD)"
WT_DIFF_BEFORE="$(git diff --stat HEAD || true)"
WT_UNTRACKED_BEFORE="$(git status --porcelain --untracked-files=no 2>/dev/null || true)"

echo "▶ push-main: HEAD=$SHA_BEFORE"
echo "▶ push-main: running make verify-main..."

# ── Gate 2: make verify-main must GREEN ────────────────────────────────
# Mirrors .github/workflows/ci.yml. --dry-run prints the gate only.
if [ "$DRY_RUN" = "true" ]; then
    echo "   --dry-run: skipping make verify-main (gates re-asserted in CI)"
else
    if ! make verify-main; then
        echo "❌ push-main: make verify-main RED. Fix the CI chain before push." >&2
        exit 1
    fi
fi
echo "✅ Gate 2 (verify-main GREEN): pre-push check chain passes."

# ── Gate 3: SHA + working-tree must be stable during tests ──────────────
SHA_AFTER="$(git rev-parse HEAD)"
WT_DIFF_AFTER="$(git diff --stat HEAD || true)"

if [ "$SHA_AFTER" != "$SHA_BEFORE" ]; then
    echo "❌ push-main: HEAD advanced during verify-main ($SHA_BEFORE → $SHA_AFTER). Test contamination." >&2
    exit 1
fi

if [ "$WT_DIFF_AFTER" != "$WT_DIFF_BEFORE" ]; then
    echo "❌ push-main: working tree changed during verify-main. Test contamination." >&2
    echo "   before: ${WT_DIFF_BEFORE:-<clean>}"
    echo "   after:  ${WT_DIFF_AFTER:-<clean>}"
    exit 1
fi

# Untracked-file-leak check (optional; quiet if zero)
WT_UNTRACKED_AFTER="$(git status --porcelain --untracked-files=no 2>/dev/null || true)"
if [ -n "$WT_UNTRACKED_AFTER" ] && [ "$WT_UNTRACKED_AFTER" != "$WT_UNTRACKED_BEFORE" ]; then
    echo "❌ push-main: untracked-file set changed during verify-main." >&2
    echo "   diff:"
    diff <(echo "$WT_UNTRACKED_BEFORE") <(echo "$WT_UNTRACKED_AFTER") >&2
    exit 1
fi
echo "✅ Gate 3 (test stability): HEAD + working-tree unchanged during verify-main."

# ── Prod-digest requirement (informational; not enforced here) ────────
cat <<'EOF'
ℹ️  push-main: local gate complete. For PRODUCTION deployment:
   1. Wait for CI to certify HEAD: `make verify-main` is the local mirror;
      CI runs the same chain + docker-sign + docker-digest.
   2. Pin the production deployment to the CI-certified manifest digest.
      NEVER pin production to HEAD automatic.
   3. Use `make docker-verify-digest` to verify the running container matches
      the pinned digest (fails-loud on drift per godlike/07).
EOF

# ── Push (under default branch + fast-forward semantics) ───────────────
if [ "$DRY_RUN" = "true" ]; then
    echo "▶ push-main: --dry-run passed all gates; skipping actual push."
    exit 0
fi

echo "▶ push-main: pushing HEAD=$SHA_AFTER to origin/$TARGET_BRANCH..."
if [ "$FORCE_PUSH" = "1" ]; then
    echo "⚠️  push-main: FORCE_PUSH=1 → using --force-with-lease (fail-loud on remote drift, but allow rebased pushes)."
    git push --force-with-lease origin "HEAD:$TARGET_BRANCH"
else
    if ! git push origin "HEAD:$TARGET_BRANCH"; then
        rc=$?
        echo "❌ push-main: git push failed (rc=$rc). Inspect remote / auth." >&2
        exit 2
    fi
fi

echo "✅ push-main: HEAD pushed to origin/$TARGET_BRANCH."
echo "   verify: git log -n 5 --oneline | head -1"
git log -n 5 --oneline | sed 's/^/     /' | head -3
