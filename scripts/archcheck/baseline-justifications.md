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
- An import alias in any `metadata.go`/`subtitles.go`/`ytdlp.go`
  across `internal/infrastructure/youtube/` or
  `internal/application/youtube/` is changed or removed (note:
  `metadata.go` and `subtitles.go` moved to `internal/application/youtube/`
  between commit `4ca6b816` (last PR0 baseline audit) and the
  `--update` cycle that captured post-`9eb11bbe` state — so
  `ytdlp.go` is the only file remaining in
  `internal/infrastructure/youtube/` of these three).

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

## Re-evaluation for PR1.5 (2026-06-21, post-baseline-audit)

**Decision: keep all 4 entries in `baseline.json`.** The audit cycle
during the PR0 baseline audit (commits `000c40c4` + `97eab0bc`) closed
the original `FAIL: New alias detected` outputs. The four entries remain:

| File | Alias | Target | Ratchet status |
|---|---|---|---|
| `internal/infrastructure/youtube/metadata.go` | `ytcfg` | `internal/infrastructure/config` | stable while package persists |
| `internal/infrastructure/youtube/metadata.go` | `urlutil` | `pkg/urlutil` | permanent (leaf-only) |
| `internal/infrastructure/youtube/subtitles.go` | `ytcfg` | `internal/infrastructure/config` | stable while package persists |
| `internal/infrastructure/youtube/subtitles.go` | `textutil` | `pkg/textutil` | permanent (leaf-only) |

### Why NOT move to "pre-rename audit" status now

The user's earlier followup asked whether to relocate these entries
to this sidecar as 'pre-rename audit' before PR1.5 begins. Decision
is no-move, with the following rationale:

1. **PR1 scope is internal, not rename-driving.**
   `docs/roadmap/PR1_YOUTUBE_INFRASTRUCTURE.md` enumerates substeps
   PR1.1..PR1.9. PR1.5 ("Ridurre `application/youtube.Service`") is
   a thin-Service refactor; it replaces concrete deps with port
   interfaces in `internal/application/youtube/ports.go`. No substep
   renames `internal/infrastructure/youtube/`.

2. **Wave 15 PR4d-final has already established the package name.**
   The directory was first added in `faaf9226` on 2026-06-21 10:19:33.
   Wave 15 did not rename it during the migration; many code paths
   import it (e.g. `internal/app/composition.go`,
   `internal/app/module_youtube.go`, `internal/app/module_stock.go`).
   A rename now would create churn proportional to those references.

3. **The ratchet justification is structural, not transient.**
   `pkg/urlutil` and `pkg/textutil` are AGENTS.md leaf-only utilities
   and cannot be eliminated by any consolidation wave. The
   `internal/infrastructure/config` aliasing is also stable (all
   packages consume config by the same import path).

4. **Removing entries from `baseline.json` would re-FAIL archcheck.**
   Until `internal/infrastructure/youtube/{metadata.go, subtitles.go}`
   is renamed or eliminated, removing the ratchet allowances re-emits
   `FAIL: New alias detected` on every run. Trading green-ratchet for
   cosmetic-documentation is a loss.

### Trigger conditions for actual relocation

Move the 4 entries OUT of `baseline.json` AND append a new section
here titled "Removed aliases (post-PR{n} audit)" ONLY when one of the
following is true:

- A future consolidation wave (PR4 — composition root cleanup, or a
  later application/infrastructure merge) executes a real rename or
  elimination of `internal/infrastructure/youtube/{metadata.go,
  subtitles.go}`. In that case:
  1. Run `go run ./scripts/archcheck --update` to regenerate the
     baseline. Entries drop off naturally if the underlying imports
     disappear.   2. If `--update` did not drop them (partial rename only), remove
     the entries from `baseline.json` manually via `str_replace`.
     Match the JSON-escaped arrow (`\u003e`) exactly as written —
     plain `>` will silently corrupt the file. If unsure, prefer
     `go run ./scripts/archcheck --update` which handles encoding.
  3. Append the new remove-section here with date + PR reference.

### Cosmetic re-affirmation (this commit)

This section is added to make the decision explicit and recoverable
across sessions; the next operator sees the rationale next to the
table above and need not re-do the analysis. The four `baseline.json`
entries remain unchanged.

#### Post-Audit Update (2026-06-21)

A subsequent `go run ./scripts/archcheck --update` revealed the
4 entries this section refers to no longer exist in `baseline.json`.
They were naturally dropped because the underlying source files
were relocated:

- `internal/infrastructure/youtube/metadata.go` -> split into
  `internal/application/youtube/metadata_enrich.go` and
  `metadata_persist.go`.
- `internal/infrastructure/youtube/subtitles.go` -> moved to
  `internal/application/youtube/subtitles.go`.

The **Decision (KEEP)** documented above was the correct call at
the time it was made (commit `9eb11bbe`). The files then moved
(organic trigger condition met), and `--update` correctly dropped
the now-stale aliases. **No manual removal was needed** — the
ratchet self-corrected.

The "stable while package persists" status in the table above
remains accurate at the moment of capture, but readers of this
section later (after PR1.5 stash pop or PR4 consolidation) should
re-walk the actual file system to confirm whether the package
still contains those imports.

See `## PR2 artlist infrastructure extraction` below for the
post-PR2 view.

## PR2 artlist infrastructure extraction (2026-06-21, post-`--update` audit)

**Audit findings**: Following the basher run that surfaced
pre-existing ratchet failures, `go run ./scripts/archcheck --update`
regenerated `scripts/archcheck/baseline.json` and captured the
following delta (pre-update SHA captured at
`/tmp/archcheck-audit/pr2-audit/baseline-before.json`):

- **5 NEW aliases** introduced by PR2 artlist infrastructure
  commits (`7a35dae2`, `433fe23a`, `00f1c6cc`, `4c6f3215`,
  `05db88b0`):

  | File | Alias | Target |
  |---|---|---|
  | `internal/infrastructure/artlist/downloader/downloader.go` | `artapp` | `internal/application/artlist` |
  | `internal/infrastructure/artlist/downloader/downloader.go` | `core_dl` | `internal/infrastructure/downloader` |
  | `internal/infrastructure/artlist/fallback/pexels.go` | `artapp` | `internal/application/artlist` |
  | `internal/infrastructure/artlist/fallback/pixabay.go` | `artapp` | `internal/application/artlist` |
  | `internal/infrastructure/artlist/scraper/scraper.go` | `artapp` | `internal/application/artlist` |

  All 5 are HexArch adapter-pattern aliases: infrastructure
  adapters importing application-layer port/domain types to
  implement interfaces.

- **2 NEW wrappers** in `internal/infrastructure/artlist/cache/cache.go`:
  `wrapInvalid` (line 329) and `wrapUnavailable` (line 330), both
  wrapping `errors.Join`. Standard cache-error short-circuit pattern;
  not a defect. Verified:
  `grep -nE '^func wrapInvalid|^func wrapUnavailable' internal/infrastructure/artlist/cache/cache.go`.

- **5 aliases REMOVED**: 1 was line-number drift on
  `scraper.go: artapp` (raw form replaced by stable form per
  `scripts/archcheck/main.go:71-77`), 4 were the pre-existing
  youtube entries (see "Re-evaluation for PR1.5" section above).

- **1 directory REMOVED**: `internal/upload` was already moved
  to `internal/infrastructure/drive/` per Wave 8 commit
  `09bcfdec` (currently in the rebased history); `--update`
  correctly dropped the stale directory entry.

**Decision**: All 5 NEW entries are accepted into the baseline.
Reasons:

1. **HexArch adapter pattern is canonical.** Infrastructure
   adapters must import application-layer port/domain types to
   implement them; this is the canonical HexArch direction and
   is not a layering violation. Per AGENTS.md `## Modular edit
   patterns` and `Pattern 8 — API package: thin transport only`
   inversion, infrastructure concrete deps are explicitly expected.
2. **`artapp` is a collision alias, fully audited.** Where
   application and infrastructure both need to be referenced from
   one file, `artapp` distinguishes the application-layer
   entrypoint. This is a **Weak claim** per the file's existing
   taxonomy — kept visible at per-entry audit level (not just
   the blanket `pkg/*` claim).
3. **`core_dl` reuses the canonical downloader.**
   `internal/infrastructure/downloader` is a leaf-only infra
   package; the alias is a stable composition rather than
   transient coupling.
4. **Wrappers are part of the cache contract.** Cache errors
   need distinct categorical markers (`wrapInvalid` vs
   `wrapUnavailable`); the wrapper audit is by-design nominal.

**Trigger conditions for actual relocation** (analogous to
PR1.5 above):

- Future consolidation of `internal/infrastructure/artlist/*`
  into a unified `internal/infrastructure/media/` or similar
  package.
- Direct rename of `artapp` to remove the application-vs-infra
  name collision (HexArch could go further with stricter naming).
- Removal of `core_dl` if the downloader itself is consolidated.

### Verification

The capture-time SHA of `scripts/archcheck/baseline.json`
post-update is `5d90d21d59061af7...` (durable in git history).
The pre-update snapshot was saved at
`/tmp/archcheck-audit/pr2-audit/baseline-before.json` for the
duration of this audit only — that path does NOT survive reboot
(per session contract). The audit facts worth preserving long-term:

- **5 aliases added** (PR2 artlist — admitted to baseline).
- **5 aliases removed** (1 line-number drift on
  `internal/infrastructure/artlist/scraper/scraper.go: artapp`
  replaced by stable form; 4 pre-existing youtube entries from
  PR0 audit cycle naturally dropped because their source files
  moved from `internal/infrastructure/youtube/` to
  `internal/application/youtube/`).
- **2 wrappers added** (`internal/infrastructure/artlist/cache/cache.go`
  lines 329, 330).
- **1 directory removed** (`internal/upload` — actually moved
  by Wave 8 commit `09bcfdec`, see also `architecture/migration.yaml`
  Wave 8 entry).

JSON validation: parses clean. Archcheck exits 0 (verified
post-update). `stash@{0}` (`WIP-PR1.5-bystander-2026-06-21-pre-rebase`)
NOT touched — pop deferred to dedicated PR1.5 follow-up session.
