#!/usr/bin/env bash
set -euo pipefail

# Fail-closed lightweight repository secret-shape audit used by verify-main.
# External scanners remain authoritative in CI; this fallback keeps the local
# gate functional when those binaries are not installed.
# Scope of the gitleaks pass: the files that could actually be committed —
# tracked files plus untracked, non-ignored ones. The working tree also holds
# multi-gigabyte render artifacts (data/ ~12 GB), and a --source . walk read
# all of them: 11.83 GB in 233 s. Snapshotting the repository content keeps
# the same detection domain at ~100 MB, and `git ls-files -co
# --exclude-standard` keeps new, not-yet-added files in scope so a secret is
# still caught before it is committed.
if command -v gitleaks >/dev/null 2>&1; then
  config="$PWD/.gitleaks.toml"
  [ -f "$config" ] || config=""
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    snapshot="$(mktemp -d)"
    trap 'rm -rf "$snapshot"' EXIT
    # The allowlist is passed with --config, never copied into the snapshot:
    # gitleaks 8.30 suppresses findings when a .gitleaks.toml sits at the
    # scanned root (verified: same probe file, rc=0 with the config copied in,
    # rc=1 without it). Copying it would turn the gate into a silent no-op.
    git ls-files -co --exclude-standard -z \
      | grep -zvE '^\.gitleaks(\.toml|ignore)$' \
      | tar -c --null -T - 2>/dev/null | tar -x -C "$snapshot"
    if [ -n "$config" ]; then
      gitleaks detect --source "$snapshot" --no-git --redact --exit-code 1 --config "$config"
    else
      gitleaks detect --source "$snapshot" --no-git --redact --exit-code 1
    fi
  elif [ -n "$config" ]; then
    gitleaks detect --source . --no-git --redact --exit-code 1 --config "$config"
  else
    gitleaks detect --source . --no-git --redact --exit-code 1
  fi
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
