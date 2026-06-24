#!/usr/bin/env bash
# .commit-audit.sh — push the audit-driven changes straight to main.
# Reads the GitHub PAT from the GH_TOKEN env var; aborts otherwise.
#
# Stages ONLY the 6 files changed by the audit session:
#   - Makefile
#   - .github/dependabot.yml
#   - .github/workflows/ci.yml
#   - .github/CODEOWNERS
#   - .github/workflows/dependabot-reconcile.yml
#   - node-scraper/package.json
#
# Token hygiene: the PAT is embedded in the remote URL only for the
# duration of the push, then `git remote set-url` strips it.
#
# SECURITY: the PAT was shared in chat — ROTATE IT after a successful
# push at https://github.com/settings/tokens.

set -euo pipefail

REPO_DIR="/home/pierone/Pyt/PipelineGen"
GH_REPO="Marcuss-ops/PipelineGen"

if [ -z "${GH_TOKEN:-}" ]; then
    echo "ERROR: GH_TOKEN env var is empty." >&2
    echo '  export GH_TOKEN="github_pat_..."  &&  bash '".commit-audit.sh" >&2
    exit 1
fi

cd "$REPO_DIR"
echo "==> Working dir: $(pwd)"

# === Init / config ===============================================
if [ ! -d .git ]; then
    echo "==> Initializing repo on branch main"
    git init -b main
else
    current_branch=$(git rev-parse --abbrev-ref HEAD)
    echo "==> Repo already initialized; current branch: $current_branch"
    if [ "$current_branch" != "main" ]; then
        echo "==> Resetting local branch to main"
        git checkout -B main
    fi
fi

git config user.name  "PipelineGen Agent"
git config user.email "agent@pipelinegen.local"

# === Stage audit files ============================================
FILES=(
  "Makefile"
  ".github/dependabot.yml"
  ".github/workflows/ci.yml"
  ".github/CODEOWNERS"
  ".github/workflows/dependabot-reconcile.yml"
  "node-scraper/package.json"
)

missing=()
for f in "${FILES[@]}"; do
    if [ ! -f "$f" ]; then missing+=("$f"); fi
done
if [ ${#missing[@]} -ne 0 ]; then
    echo "ERROR: missing files:" >&2
    printf '  - %s\n' "${missing[@]}" >&2
    exit 1
fi

echo "==> Staging ${#FILES[@]} files"
git add "${FILES[@]}"

staged_count=$(git status --short | wc -l | tr -d ' ')
echo "==> git status --short ($staged_count entries, expected ${#FILES[@]}):"
git status --short
if [ "$staged_count" -ne "${#FILES[@]}" ]; then
    echo "ERROR: staged count mismatch. Aborting before commit." >&2
    git restore --staged . >/dev/null 2>&1 || true
    exit 1
fi

# === Commit =======================================================
echo "==> Committing"
git commit -m "ci(audit): dependabot v2 schema + go/node guard mirror (Tier 2-4)" \
            -m "Audit-driven fixes (June 2026, Tier 2-4).

- Dependabot (v2 schema): open-pull-requests renamed to
  open-pull-requests-limit on all four ecosystems. Group go-security
  switched to applies-to: security-updates; group go-minor-and-patch
  uses applies-to: version-updates with update-types
  [minor,patch]. Removed allow: [dependency-type: direct] so
  transitive security advisories are no longer silently filtered.
- CI: 'make go-version-check' after every actions/setup-go@v5
  (build/lint/vuln). New verify-node-scraper job with
  actions/setup-node@v4 + npm ci against /node-scraper.
- Makefile: 'GO ?= go' introduced; every 'go' invocation in a
  recipe now uses \$(GO) so overrides like 'make GO=/path/to/go'
  take effect. New 'node-version-check' target reads engines.node
  from node-scraper/package.json (anchored numeric extractor
  handles ^22, >=22, ranges, pre-release). New 'preflight'
  aggregator (read-only; docker compose config guarded by
  'command -v docker').
- node-scraper/package.json: engines.node: '22.x' added as single
  source of truth (matches the Dockerfile.scraper comment).
- .github/CODEOWNERS: pins @pierone on dependabot.yml and
  workflows/. CODEOWNERS alone does not enforce merge blocking;
  TBD ruleset / branch protection.
- .github/workflows/dependabot-reconcile.yml: new read-only weekly
  Monday 08:00 UTC audit. Lists Dependabot PRs, counts
  semver-major in titles, runs 'go mod tidy' + 'git diff --exit
  -- go.mod go.sum', opens a single dependency-labelled issue
  ONLY on anomaly. Never commits, never rebases, never modifies
  lockfile.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>"

# === Push ========================================================
REMOTE_WITH_TOKEN="https://x-access-token:${GH_TOKEN}@github.com/${GH_REPO}.git"
REMOTE_CLEAN="https://github.com/${GH_REPO}.git"

if git remote get-url origin >/dev/null 2>&1; then
    echo "==> Remote 'origin' already exists; rewriting URL with token"
    git remote set-url origin "$REMOTE_WITH_TOKEN"
else
    echo "==> Adding remote 'origin'"
    git remote add origin "$REMOTE_WITH_TOKEN"
fi

echo "==> Pushing to main"
git push -u origin main

echo "==> Stripping token from remote URL"
git remote set-url origin "$REMOTE_CLEAN"

# === Verify =======================================================
echo
echo "==> Final state:"
echo "--- git remote -v ---"
git remote -v
echo "--- git log --oneline -1 ---"
git log --oneline -1
echo "--- git show --stat HEAD (first 25 lines) ---"
git show --stat HEAD | head -25

echo
echo "==> DONE."
echo "==> Now: (a) close this shell OR run:  unset GH_TOKEN"
echo "==>       (b) ROTATE the PAT on GitHub (it leaked via chat):"
echo "==>           https://github.com/settings/tokens"
