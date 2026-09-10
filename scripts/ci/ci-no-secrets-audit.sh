#!/usr/bin/env bash
set -euo pipefail

# Fail-closed lightweight repository secret-shape audit used by verify-main.
# External scanners remain authoritative in CI; this fallback keeps the local
# gate functional when those binaries are not installed.
if command -v gitleaks >/dev/null 2>&1; then
  gitleaks detect --source . --no-git --redact --exit-code 1
  exit 0
fi

pattern="(VELOX_(ADMIN|WORKER)_TOKEN(:-|=)[a-f0-9]{64}\\b|\\bAKIA[0-9A-Z]{16}\\b|\\baws_secret_access_key[[:space:]]*=[[:space:]]*[\\\"']?[A-Za-z0-9/+=]{40}|\\bghp_[A-Za-z0-9]{36,}\\b|\\bgithub_pat_[A-Za-z0-9_]{22,}\\b|\\bxox[abpr]-[A-Za-z0-9-]{10,}\\b|-----BEGIN (RSA|EC|OPENSSH|DSA|PGP) PRIVATE KEY-----)"
files=$(mktemp)
trap 'rm -f "$files"' EXIT
git ls-files | grep -vE '^(\.env|\.env\.|config/youtube_cookies\.txt|cookies\.txt|secrets/)' >"$files" || true
if command -v rg >/dev/null 2>&1; then
  if xargs -a "$files" rg -n -e "$pattern" --color=never >/dev/null 2>&1; then
    echo "❌ no-secrets audit: secret-shaped content found" >&2
    xargs -a "$files" rg -n -e "$pattern" --color=never >&2 || true
    exit 1
  fi
else
  if xargs -a "$files" grep -nE -- "$pattern" >/dev/null 2>&1; then
    echo "❌ no-secrets audit: secret-shaped content found" >&2
    exit 1
  fi
fi
echo "✅ no-secrets audit: PASS"
