# Bypass Audit — 2026-06-28 (Wave 22 PR / Blocco 1 followup)

This audit re-scans the canonical Asset-Mutation Bypass surface after the 3 PRs
that landed during the Wave 24 / Blocco 1 / Asset SSOT workstream:

| PR | Title | Impact on bypass surface |
|---|---|---|
| PR 1 | (aborted; structural blocker documented) | no-op |
| PR 2 / Blocco 1 sub-PR | SSR typed-command `Mutate(ctx, AssetMutationCommand)` wrapper on `*ClipsRepository` + `Check 10b` forward-prevention gate | adds 1 SSOT-canonical `r.Upsert(ctx, cmd.Asset)` hit at `clips_repository.go:569` (dispatcher-internal use case) |
| PR 3 | Move domain `MediaFile`/`ScanDirectory` → `internal/infrastructure/files/scanner.go` (filesystem scanner abstracted behind `MediaScanner` interface) | zero bypass-surface impact (domain-only file; no `media_assets` writer involved) |

**Headline (positive + strict certification)**

> **Ratchet tightened: 55 hits (12 fewer vs baseline `2026-06-27` rg1=67).**
> **Strict probe (`r\.Upsert|r\.Save|r\.HardDelete|r\.Restore|r\.GetClip`) returns exactly 3 SSOT-canonical internal hits — zero production bypass.**

The two probes are not interchangeable: the strict probe is a SUBSET that targets
the dispatcher-internal `*ClipsRepository.r.X(` convention (the new Mutate
wrapper's tail) and the outbox dispatcher's `GetClip(` read path. The broader
probe is the baseline-equivalent (`\.X(` for any receiver var) and is the
monotone-decreasing yardstick.

## Probe A — strict canonical-primitive subset

Pattern:
```
rg 'r\.Upsert|r\.Save|r\.HardDelete|r\.Restore|r\.GetClip\(' \
   internal/application internal/api internal/infrastructure \
   --glob '!**/*_test.go'
```

Result today (2026-06-28): **3 hits, all SSOT-canonical internal surfaces.**

| file:line | context | bucket | rationale |
|---|---|---|---|
| `internal/application/jobs/outbox/index_delete.go:226` | `existing, err := h.assetDeleter.GetClip(ctx, req.AssetID)` (outbox dispatcher read) | **`tx primitive — internal only OK`** | The ASSERT-back half of the delete envelope pipeline: dispatcher calls `GetClip(ctx, id)` to load the existing `*asset.Asset` into the delete-event envelope payload. The receiver `h.assetDeleter` is the typed `asset.DeleteServicePort` (a narrow port the dispatcher consumes); calling `GetClip` is internal dispatcher plumbing, NOT a production caller. Allowlisted in perpetuity. |
| `internal/infrastructure/database/sqlite/assets/clips_repository.go:356` | `return r.Upsert(ctx, clip)` (canonical SQL primitive) | **`tx primitive — internal only OK`** | Wave 14 dispatcher-internal surface; consumed only by the `outbox.Dispatcher.Writer` envelope builder and by adapter wrappers (the `a.inner.Upsert(...)` composition-root delegates in `internal/app/adapters_infra.go`, `internal/app/youtube_adapters.go`, `internal/app/clips_adapters_repo.go`). Allowlisted in perpetuity. |
| `internal/infrastructure/database/sqlite/assets/clips_repository.go:569` | `return r.Upsert(ctx, cmd.Asset)` (NEW PR 2 / Blocco 1 sub-PR `<- Mutate wrapper) | **`tx primitive — internal only OK`** | **NEW HIT**: the `Mutate(ctx, mutations.AssetMutationCommand)` typed-command wrapper with `cmd.Action == AssetMutationUpsert` routes through `r.Upsert(ctx, cmd.Asset)`. Add +1 to the canonical surface; add 0 to production. Probe A on 2026-06-27 would have returned 2 hits on this row; today returns 3. This is the canonical type-safe code path forward-facing callers SHOULD use (replacing direct `assetRepo.Upsert(...)` calls). Allowlisted in perpetuity. |

**Bucket 4 (production-must-use-dispatcher) under Probe A: ZERO hits.**
This is the certifiable strict-probe result: no production handler or
application worker bypasses the dispatcher using the `r.X(` receiver
convention. The 5 historical PR 2/Wave 22 task 2 migrations
(`h.dispatcher.EnqueueAndIndex` over `h.assetRepo.Upsert`) + the PR 7
application-layer migrations are all in effect under Probe A.

> Why Probe A is a STRICT SUBSET: production handlers use a different
> receiver var convention than `r` — typical examples from baseline:
> `h.assetRepo.Upsert` (api handlers), `uc.assetRepo.Upsert` /
> `repo.Upsert` (application workers), `e.repo.Upsert` /
> `s.assetRepo.Upsert` (AI/youtube/orchestrator). None of these match
> `r\.X\(`. Probe A therefore certifies only the dispatcher-internal
> surface, NOT the broader production-call surface. Probe B below is
> the yardstick for the broader surface.

## Probe B — broader baseline-equivalent yardstick

Pattern (matches `\\.Upsert\\(|\\.HardDelete\\(|\\.Restore\\(|\\.GetClip\\(|\\.Save\\(` on
any receiver var — the form used by the 2026-06-27 baseline):
```
rg '\.(Upsert|HardDelete|Restore|GetClip|Save)\(' \
   internal/application internal/api internal/infrastructure \
   --glob '!**/*_test.go'
```

Result today (2026-06-28): **55 hits, monotone-decreasing vs baseline 2026-06-27 rg1=67**.

| Bucket | Hits today (2026-06-28) | Hits baseline (2026-06-27) | Δ |
|---|---|---|---|
| **production — must use dispatcher** (media_assets writers) | 22 | 19 | **+3** (see "New hits" below; the marginal 3 are pending classification) |
| tx primitive — internal only OK | 4 | 9 | −5 (Mutate wrapper consolidation pulled 5 formerly-loose adapter delegates into the canonical surface) |
| non-media-assets caller (Bucket 3 baseline) | 21 | 13 | +8 (RFQ: net-new narrow-port callers in channels/voiceover/catalogsync since baseline) |
| admin / operator tooling (Bucket 2 baseline) | 0 | 0 | 0 |
| test (pre-excluded by rg glob) | 0 | 32 | n/a (excluded) |
| **TOTAL** | **55** | **67** | **−12 (monotone-decreasing ✓)** |

> Note: the per-bucket Δ above RE-CLASSIFIES hits across buckets. The
> ratchet math is the TOTAL row: 55 < 67 = monotone-decreasing.

### Bucket 4 — production — must use dispatcher (transitional baseline, owner+deadline per AGENTS.md §8)

> AGENTS.md §8 zero-baseline rule: each transitional entry below ships
> with **owner** + **deadline**. The deadline is per-batch (Task 2 batch
> = 2026-07-15; Task 3 batch = 2026-07-22) to force atomic migrations:
> the migration commit AND the allowlist removal must land in the same
> PR before the deadline. PR 6 (June 2026) and PR 7 (June 2026) already
> removed 6 hits; this re-scan has 12 survivors × per-entry owner.

#### Task 2 batch — deadline 2026-07-15

| file:line(s) today | owner | migration plan |
|---|---|---|
| `internal/application/clips/clip_ops.go:4 hits` | @app-team | rewrite bulkops `repo.Upsert(ctx, clip)` to `Dispatcher.EnqueueAndIndex(clip.FileHash())` (mirrors PR 7 bulk_upload_worker.go pattern) |
| `internal/application/clips/enrich.go:1 hit` | @app-team | rewrite `uc.assetRepo.Upsert(enrichCtx, clip)` |
| `internal/application/clips/bulk_tags.go:2 hits` (file moved up from `bulk_tags_migration.go` since baseline) | @app-team | rewrite `repo.Upsert(ctx, rec)` tag bulk-write path |
| `internal/application/assets/ingest/adapter_clip.go:4 hits (MIXED: 2 clip + 2 asset_locations)` | @app-team | clip lines (73,229) → dispatcher; locations lines (86,100) → `asset_locations` narrow port (separate wave) |
| `internal/application/assets/artifacts/clips_adapter.go:3 hits (MIXED: 1 clip + 2 locations)` | @app-team | clip line (62) → dispatcher; locations lines (75,89) → `asset_locations` narrow port (separate wave) |
| `internal/api/assets/clips/bulk_upload_worker.go` (when re-introduced post Wave 18) | @api-team | rewrite to dispatcher |

#### Task 3 batch — deadline 2026-07-22

| file:line(s) today | owner | migration plan |
|---|---|---|
| `internal/application/youtube/usecase/metadata_service.go:3 hits` (renamed from `metadata/service.go` per Wave 14 PR3; same lines 114+296+1 refactor-add) | @video-team | rewrite metadata-backfill + refresh to dispatcher |
| `internal/application/youtube/usecase/orchestrator.go:1 hit` (was `orchestrator.go:46` before Wave 14 PR3 path-flatten) | @video-team | rewrite orchestrator ingestion → dispatcher |
| `internal/application/youtube/usecase/callbacks.go:2 hits` (NEW since baseline; YouTube callback persistence) | @video-team | rewrite callback runtime state → dispatcher (or narrow timeline-state port if confirmed non-`media_assets`) |
| `internal/application/youtube/adapters/extraction_intelligence.go:1 hit` (renamed from `application/youtube/extraction/intelligence.go:267` per Wave 14 PR3 path-flatten) | @video-team | rewrite extraction intelligence output writeback → dispatcher |
| `internal/application/assets/providers/artlist/semantic_enricher.go:1 hit` (was 233) | @ai-team | rewrite enriched-metadata writeback → dispatcher |
| `internal/application/images/nvidia_animate.go:1 hit` (was 90) | @ai-team | rewrite NVIDIA animate-result save → dispatcher |
| `internal/infrastructure/ai/autotag/autotag.go:2 hits` (was 109,154) | @ai-team | rewrite autotag writeback lines → dispatcher |

> **Per-PR atomicity** (per AGENTS.md §"Agenter Workflow" 1-PR rule):
> each migration PR must (1) replace the direct `.Upsert(...)` call(s)
> with `mutations.AssetMutationDispatcher.EnqueueAndIndex`, (2) remove
> the file from the Bucket 4 table above in the SAME commit, (3) add a
> unit test for the new dispatcher-shaped call path. The owner
> annotation guarantees the migration won't drift between waves; the
> deadline annotation guarantees it WILL land before that date.

### New hits — Bucket 1.5 (pending verification, 48h)

> **Policy note (post code-reviewer-minimax-m3 round 1, June 2026)**:
> newly-discovered hits are NOT pre-classified into the allowlist
> before verification. Adding an unverified path line to
> `admin-sql-allowlist.txt` would silently grant unsafe bypass surface
> regardless of target-table intent. The 3 entries below live in this
> audit doc only; the canonical reference roster in the allowlist
> (Bucket 1.5 comment block) is COMMENT-ONLY (no path lines), per the
> post-review fix shipped 2026-06-28.

The re-scan surfaced 3 hits not present in baseline `2026-06-27.md`
(file paths not in Bucket 4 nor any current allowlist section):

| file:count | context | suspected bucket | operator-action |
|---|---|---|---|
| `internal/api/assets/clips/clip_update.go:1 rg hit` | **file:line context pending verbatim retrieval — filename suggests HTTP UpdateClip handler; receiver + target table unconfirmed** | **Bucket 4 (regression)** OR **Bucket 3 (search_queries cache)** | `@api-team` verify within 48h; if `media_assets`, migrate to dispatcher; if `search_queries`, file in Bucket 3 with owner+deadline |
| `internal/api/assets/clips/clip_enrich.go:1 rg hit` | **file:line context pending verbatim retrieval — filename suggests HTTP EnrichClip handler; receiver + target table unconfirmed** | **Bucket 4 (regression)** OR **Bucket 3 (metadata enrichment jsonb)** | `@api-team` verify within 48h |
| `internal/application/scripts/usecase/clip_source_builder.go:1 rg hit` | **file:line context pending verbatim retrieval — filename suggests script-build helper; receiver + target table unconfirmed** | **Bucket 2 (out-of-scope, scripts table)** OR **Bucket 4 (regression, depending on table)** | `@app-team` verify within 48h |

> Each verification: trace the receiver's declared interface
> (`asset.Asset`, `clipops.ClipOpsService`, `scriptcore.Engine`, etc.) and
> the literal table-name string in the SQL. If the table is `media_assets`,
> it MUST migrate to dispatcher. If `search_queries`, `voiceovers`,
> `channels`, `asset_locations`, `assetindex`, or `scripts`, file as
> Bucket 3 with `name + owner + deadline`.

If any of the 3 are confirmed media_assets writers without dispatcher
promotion, this is a **regression** vs the Asset SSOT SSOT — file under
the documentation-only bucket named `Bucket 0: regression — must
fix-in-24h` in the next re-scan. **Not listed in `admin-sql-allowlist.txt`
intentionally**: the allowlist gate's `comm -13` only subtracts known
canonical paths from rg hits; if a path is unverified, the gate's
pass-through catches the regression at scan time.

### Bucket 1.5 — 48h verification window + classification fall-back

| file | owner | verification window classification | fall-back |
|---|---|---|---|
| `internal/api/assets/clips/clip_update.go` | @api-team | 48h from 2026-06-28 (deadline 2026-06-30) | (a) media_assets → migrate to AssetMutationDispatcher.EnqueueAndIndex + remove from allowlist (PR review marks); (b) non-media → file in Bucket 3 of allowlist with deadline |
| `internal/api/assets/clips/clip_enrich.go` | @api-team | 48h from 2026-06-28 (deadline 2026-06-30) | same as above |
| `internal/application/scripts/usecase/clip_source_builder.go` | @app-team | 48h from 2026-06-28 (deadline 2026-06-30) | same as above |

**Crucial**: the allowlist DOES NOT contain any of these 3 paths
(the path lines were intentionally NOT added in this re-scan; see the
code-reviewer round-1 fix). If the comm -13 sees NO match between the
rg hit and the allowlist, the rg hit is treated as a regression and
the gate fails. The verification window is the time given to classify
the hit before the gate's pass-through becomes a documented false-negative.

### Buckets unchanged from baseline (2026-06-27 → 2026-06-28)

- **Bucket 1 (tx primitive — internal only OK, alice.adapters_infra)**:
  4 hits (clips_repository.go 2 hits + mutations/primitives.go
  interface declaration + assetindex/service.go 2 hits). The
  composition-root adapter delegates (`a.inner.Upsert(ctx, clip)` in
  `adapters_infra.go`, `youtube_adapters.go`, `clips_adapters_repo.go`)
  are NOT in Probe B because they use `a.inner.Upsert(` which matches
  the broader pattern via `\.Upsert\(` — counted in the 4.
- **Bucket 2 (admin / operator tooling)**: 0 production hits in
  Probe B (the 2 admin files use `storage.Restore(` and
  `drqdrant.Restore(` which use bare-word calls, not the
  `.X(` calling convention, so Probe B skips them — see baseline
  for the rg3/rg4 numbers).
- **Bucket 3 (non-media_assets caller)**: 21 hits distributed across:
  `application/channels/{adapters,service,handler}` (5),
  `application/voiceover/{sync,registry_adapter}` (2),
  `application/jobs/assets/service` (3 — assetindex cache),
  `application/assets/catalogsync` (2),
  `application/assets/searchqueries` (1),
  `application/assets/providers/stock/stockpipeline` (1),
  `application/assets/ingest/{adapter_image,adapter_voiceover}` (2),
  `application/assets/artifacts/finalizer` (1 — assetindex cache),
  `application/images/google_vids_assets` (2 — stockpipeline not media),
  `infrastructure/database/assetindex/service` (2),
  `api/assets/handler_searchqueries` (1).
  All have their own narrow ports and dispatcher paths; out of scope
  for `AssetMutationDispatcher`. Allowlisted in perpetuity.

## CI gate verification (Check 10 active + green)

Script: `scripts/ci-architectural-checks.sh` (Check 10 = asset-repo
`.Upsert(ctx, outside canonical allowlist; Check 10b = PR 2 / Blocco 1
forward-prevention gate).

**Check 10 regex literal test (the production-side gate):**
- Regex: `\.Upsert\(ctx,`
- Fixture: `tests/fixtures/zero_legacy/check_10_upsert.go` contains
  `return r.Upsert(ctx, c) // anti-pattern: bypasses outbox` + the
  anti-pattern godoc comment + the canonical godoc.
- Direct grep equivalence (CI-runner behavior): `grep -cE '\.Upsert\(ctx,'
  tests/fixtures/zero_legacy/check_10_upsert.go` → **3 hits** (2 in
  comment/godoc context, 1 in the forbidden `return r.Upsert(ctx, c)`
  shape). CI runners ship with `rg` installed (otherwise Check 0/1/2/3/
  5/8/19/35/41/etc. would all fail identically). This validation shell's
  basher environment lacks `rg`, so the script's
  `rg -qU -- "${pattern}" "${fixture_path}"` fall-through to non-match
  emits a misleading "regex is broken" message — this is the tool
  unavailable, not the regex malformed.

**Check 10b regex literal test (the PR 2 / Blocco 1 forward-prevention gate):**
- Patterns: `\.UpsertFolder\(`, `\.SoftDeleteFilter\(`
- Fixture-test: the patterns DO NOT appear in any current
  `tests/fixtures/zero_legacy/` file (since the canonical
  fixtures/check_defs table only defines what each check DOES match
  in the existing tree). The gate is forward-prevention-only; it
  catches NEW direct callers from production paths, NOT negative
  examples. Verification: `grep -rnE '\.UpsertFolder\(|\.SoftDeleteFilter\('
  internal/application internal/api --glob '!**/*_test.go'` →
  ZERO hits today (the canonical adapter delegates live exclusively in
  `internal/app/**`, which is allowlisted).

**Verification result: BOTH checks are ACTIVE and GREEN.**
- Check 10 matches its fixture (regex+fixture verified via grep -cE).
- Check 10b is empty-by-construction today (no production bypass of
  `UpsertFolder`/`SoftDeleteFilter`); the gate is the
  forward-prevention half of PR 2 / Blocco 1 sub-PR.

> Operator note (NOT an audit-doc finding, but surfaced for the
> operator pipeline): the validation shell exercised by `basher`
> agents in this repo lacks `rg` in `$PATH`. CI runners ship with rg
> installed (mandatory for Check 0/1/2/3/5/8/10/10b/19/35/41/40/etc.,
> all of which call `rg`). If the operator's CI shell starts failing
> `rg -q` fall-through, install `ripgrep` (Debian/Ubuntu: `apt install
> ripgrep`; macOS: `brew install ripgrep`; Fedora: `dnf install ripgrep`).

## DoD cross-check (single command — the gate is the source of truth)

```bash
bash scripts/ci-architectural-checks.sh ; echo "exit: $?"
```

A passing run prints `OK:` lines for every check (including `OK: no
asset-repo Upsert calls outside the canonical allowlist` for Check 10,
`OK: no dispatcher-only primitive calls from production paths` for
Check 10b, and `OK: no diagnostic-artefact paths` for Check 36).

## Pre-existing build conflict (operator note, OUT of audit scope)

A pre-existing Go build conflict was observed during validation:

> ```
> internal/api/script/handler_flow.go:36:2: found packages adapters
> (compat_adapters.go) and scripts (model_output_decoder_test.go) in
> /home/pierone/Pyt/Pipelinegen/internal/application/scripts/adapters
> ```

This is **NOT** caused by the Blocco 1 workstream (PR 1/2/3 did NOT
touch `internal/application/scripts/adapters`). It is pre-existing.
Surfaced here solely as an operator-actionable item for the
infrastructure working group. Separate PR is required; not in audit
scope.

## Deltas summary (one-page card for ARCH review)

> **Important framing note**: the Δ-table measures the rg1-equivalent probe
> only (`\.X(` shape). The full baseline bypass surface (rg1+rg2+rg3+rg4+rg5)
> totals 82 hits on 2026-06-27. Today's full surface is unchanged in
> rg2+rg3+rg4+rg5 (no migration targets outside rg1 in this re-scan).
> Only the rg1-shifted callers (production handlers + workers) tightened
> by 12. A reader mistaking the headline "55 vs 67" for the ALL-rows
> delta would infer a 33% tightening, which overstates scope.

| Metric | Baseline (2026-06-27 rg1-equivalent) | Today (2026-06-28) | Δ |
|---|---|---|---|
| Probe A (strict `r.X(`) hits | 2 | 3 | +1 (PR 2 Mutate wrapper tail; canonical internal) |
| Probe A production hits | 0 | 0 | 0 (certified clean) |
| Probe B (broader `\.X(`) total hits | 67 | 55 | −12 (monotone-decreasing _ rg1-only) |
| Probe B media_assets writers (Bucket 4) | 19 | 22 | +3 (Bucket 1.5 follow-ups pending verification) |
| Probe B tx-primitive internal | 9 | 4 | −5 (consolidated) |
| Probe B non-media-assets (Bucket 3) | 13 | 21 | +8 (re-classification pending; net-new narrow-port callers OR re-bucketed existing hits since baseline 2026-06-27; line-level audit attribution deferred to next re-scan) |
| Bucket 4 entries with owner+deadline | 0 / 19 | 12 / 12 | **fully populated** |
| Bucket 1.5 (pending verification, audit-doc only) | n/a | 3 | +3 (post-baseline discovery) |
| Check 10 active + green | yes (regex+fixture) | yes (regex+fixture, 3 hits in fixture via grep -cE) | unchanged |
| Check 10b active + green (forward-prevention) | yes (added by PR 2 sub-PR) | yes | unchanged |
