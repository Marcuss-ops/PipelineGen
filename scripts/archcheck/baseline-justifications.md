# scripts/archcheck/baseline.json — Ratchet justifications sidecar

This file is the canonical ratchet justification for each alias and wrapper
entry in `scripts/archcheck/baseline.json`.

Archcheck runs in **ratchet mode** (see `scripts/archcheck/main.go` Check 3):
it accepts only the violations captured in the baseline. New aliases added
after a `--update` regeneration trigger a `FAIL: New alias detected`,
blocking CI.

JSON has no native comments, so this sidecar documents WHY each non-trivial
entry exists, so that future operators can read the baseline and know what
to expect during audits (without having to re-do the git-archaeology every
time).

## Pre-existing aliases since Wave 15 PR4d-final landing (commit faaf9226)

### Group: `internal/infrastructure/youtube/{metadata.go, subtitles.go}`

**Audit findings (2026-06-21, fd8e3a43 → 6ebd1b43):**

- The directory `internal/infrastructure/youtube/` was first added in commit
  `faaf9226` ("feat: Wave 15 + PR4d-final landing") on 2026-06-21 10:19.
- The same commit refreshed `scripts/archcheck/baseline.json` via
  `go run ./scripts/archcheck --update`. The auto-generated baseline
  correctly captured `internal/infrastructure/youtube/ytdlp.go: ytcfg ->
  infrastructure/config` but **silently missed the imports in `metadata.go`
  and `subtitles.go`**.
- Root cause hypothesis: baseline refresh iteration occurred BEFORE the
  `metadata.go` / `subtitles.go` files were committed within the same
  commit hash (git tracks at the commit level but `--update` scans the
  working tree at one instant in time).
- archcheck ratchet then re-FAILed on these 4 aliases on every run since
  `faaf9226` — exit code 0 (ratchet allows baseline violations) but the
  output still showed the 4 `New alias detected` messages.

**Justified entries added to baseline.json:**

| Baseline entry | File / line | Owner / decision | Re-evaluation target |
|---|---|---|---|
| `metadata.go:10` `import alias ytcfg -> "<...>/internal/infrastructure/config"` | `internal/infrastructure/youtube/metadata.go` line 10 | pre-existing (faaf9226) | PR1 — YouTube infrastructure extraction consolidates/downloadstream utils out of `application/youtube` into `internal/infrastructure/youtube/`. After PR1 lands, the alias should still exist (this package already lives there) but the surrounding sibling package may be reduced. |
| `metadata.go:11` `import alias urlutil -> "<...>/pkg/urlutil"` | `internal/infrastructure/youtube/metadata.go` line 11 | pre-existing (faaf9226) | PR1 — `pkg/urlutil` is a leaf-only shared utility (AGENTS.md). Cannot be eliminated; ratchet stays at 1 for this entry forever. |
| `subtitles.go:13` `import alias ytcfg -> "<...>/internal/infrastructure/config"` | `internal/infrastructure/youtube/subtitles.go` line 13 | pre-existing (faaf9226) | PR1 — see `metadata.go:10` rationale. |
| `subtitles.go:14` `import alias textutil -> "<...>/pkg/textutil"` | `internal/infrastructure/youtube/subtitles.go` line 14 | pre-existing (faaf9226) | PR1 — `pkg/textutil` is a leaf-only shared utility (AGENTS.md). Cannot be eliminated; ratchet stays at 1 for this entry forever. |

**Consequence**: archcheck `--update` will not change these lines
(`--update` would only ADD new violations, never remove pre-existing ones).

## Other entries (no explanation added yet)

All other entries in `baseline.json` follow the AGENTS.md canonical ratchet
pattern. They are either:
- (a) cross-package aliasing required by Go's import rules (collisions on
  the package name from different modules), or
- (b) forever-acceptable leaf-only utilities (`pkg/*` aliases).

When a future operator needs to investigate a specific alias, they should
add an entry above this section documenting the finding.

## Re-audit triggers (when to re-evaluate this sidecar)

- A `--update` of `scripts/archcheck/baseline.json` is run.
- A new capability package is migrated (PR1, PR2, PR3, PR4).
- An import alias in `internal/infrastructure/youtube/{metadata.go,
  subtitles.go, ytdlp.go}` is changed or removed.
