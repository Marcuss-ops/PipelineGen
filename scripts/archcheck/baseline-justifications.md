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
| `metadata.go:12` `import alias urlutil -> "<...>/pkg/urlutil"` | `internal/infrastructure/youtube/metadata.go` line 12 | pre-existing (faaf9226) | PR1 — `pkg/urlutil` is a leaf-only shared utility (AGENTS.md). Cannot be eliminated; ratchet stays at 1 for this entry forever. |
| `subtitles.go:13` `import alias ytcfg -> "<...>/internal/infrastructure/config"` | `internal/infrastructure/youtube/subtitles.go` line 13 | pre-existing (faaf9226) | PR1 — see `metadata.go:10` rationale. |
| `subtitles.go:14` `import alias textutil -> "<...>/pkg/textutil"` | `internal/infrastructure/youtube/subtitles.go` line 14 | pre-existing (faaf9226) | PR1 — `pkg/textutil` is a leaf-only shared utility (AGENTS.md). Cannot be eliminated; ratchet stays at 1 for this entry forever. |

**Consequence**: archcheck `--update` will not change these lines
(`--update` would only ADD new violations, never remove pre-existing ones).

## Other entries (unclassified)

The remaining ~250 entries in `baseline.json` are **NOT individually audited
in this sidecar** — only entries flagged during explicit audit cycles are
recorded in the curated section above. Future operators should NOT read the
absence of explanation here as a blanket ratchet-justification for any other
entry.

**Strong claim** (always true): every alias that points into `pkg/*` leaf-only
utilities follows AGENTS.md "Leaf-only — zero imports from internal/" and
remains acceptable for the lifetime of the codebase. These ARE blanket
ratchet-acceptable but are **not enumerated here**.

**Weak claim** (must be individually reviewed): a small fraction of the
remaining entries are cross-package aliasing from imported package-name
collisions (e.g. two `drive` packages from different paths, or `jobs` from
`internal/application/jobs` vs `internal/domain/job`). These cases need
per-entry review.

**Baseline refresh protocol**: when `go run ./scripts/archcheck --update`
regenerates `baseline.json`, every NEW entry (anything not previously in the
baseline) MUST be walked through by an operator and added to this sidecar
under its proper bucket before the regenerated baseline is committed.
The presence of an entry in `baseline.json` without a sidecar entry IS a
defect, not an implicit justification.

## Re-audit triggers (when to re-evaluate this sidecar)

- A `--update` of `scripts/archcheck/baseline.json` is run.
- A new capability package is migrated (PR1, PR2, PR3, PR4).
- An import alias in `internal/infrastructure/youtube/{metadata.go,
  subtitles.go, ytdlp.go}` is changed or removed.

## Verification of the 4 line numbers above

Line numbers in the table above were verified at commit `000c40c4` (HEAD
during the PR0 → baseline-audit cycle) via:

```
grep -nE '^\s*(ytcfg|urlutil|textutil|hashutil|slicer|process)' \
  internal/infrastructure/youtube/metadata.go \
  internal/infrastructure/youtube/subtitles.go
```

Result: `metadata.go:10 ytcfg`, `metadata.go:12 urlutil`, `subtitles.go:13
ytcfg`, `subtitles.go:14 textutil`. These line numbers are informational only
(archcheck's `stableAliases` strips them from the ratchet key per
`scripts/archcheck/main.go:71-77`), so a future drift does not break the
build — but the sidecar reflects them as cosmetic accuracy.
