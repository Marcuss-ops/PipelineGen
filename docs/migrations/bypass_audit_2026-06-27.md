# Bypass Audit — 2026-06-27 (Wave 22 PR-4)

Wave 22 (QDRANT-asset-mutation isolation, June 2026) introduced the canonical
[`AssetMutationDispatcher`](../../internal/application/assets/mutations/dispatcher.go)
SSOT interface in PR-1 (commit `3449bf9b`). Subsequent tasks (2: narrow
per-feature ports, 3: restore + media-curate handlers, 5: deprecate ad-hoc
ports) hang off that interface. This audit — task 4 — is the **baselining
step**: every direct mutation call site (`\.Upsert\(`, `UpsertClip\(`,
`\.Restore\(`, `\.HardDelete\(`) is inventoried and classified so the
CI gate can detect any future regression.

## Raw output

The four required rg queries (plus the `ExecContext.*media_assets`
follow-up that confirms the raw-SQL antipattern is already gone) were run
on `2026-06-27` and the raw output saved to:

```
docs/tmp/bypass_audit_2026-06-27.txt   (93 lines, 80 actual hits)
```

## Bucket definitions

| Bucket | Action | Migration wave |
|---|---|---|
| **production — must use dispatcher** | rewrite to call `AssetMutationDispatcher.*` per task 2/3 | Wave 22 tasks 2 + 3 + 5 |
| **tx primitive — internal only OK** | keep, mark with `// internal-only: dispatcher-context` | already migrated (PR-1) |
| **admin migration/backfill** | explicit allowlist entry in `docs/migrations/admin-sql-allowlist.txt` | Wave 22 PR-4 (this PR) |
| **test** | no action | already excluded by rg `--glob '!**/*_test.go'` |
| **non-media-assets caller** | keep, separate narrow port surface | out of scope for AssetMutationDispatcher |

> **Note on bucket 5 (non-media-assets)**: replaces the prior "in-flight
> migration" pseudo-bucket for files that mutate tables OTHER than
> `media_assets` (channels, voiceovers, search_queries,
> asset_locations, assetindex cache, stockpipeline records, image
> registry). These callers already use their own typed ports and
> dispatcher paths; they are NOT bypasses of `AssetMutationDispatcher`.

## Classification summary

| rg query | total hits | production | tx primitive | admin | non-media | test |
|---|---|---|---|---|---|---|
| `rg '\.Upsert\(' internal cmd`                | 67 | 19 |  9 |  0 | 13 | 26 |
| `rg 'UpsertClip\(' internal cmd`             |  6 |  0 |  4 |  0 |  0 |  2 |
| `rg '\.Restore\(' internal cmd`              |  7 |  0 |  0 |  3 |  0 |  4 |
| `rg '\.HardDelete\(' internal cmd`           |  2 |  0 |  1 |  1 |  0 |  0 |
| `rg 'ExecContext.*media_assets' internal/...`|  0 |  0 |  0 |  0 |  0 |  0 |
| **TOTAL**                                    |**82**|**19**|**14**|**4**|**13**|**32** |

> Notes:
> - "production" column counts unique rg lines, not unique files; 19
>   production hits span 20 unique files (clip_ops.go has 1 prod line).
> - "test" column count differs slightly from the previous draft —
>   we now exclude the allowlist-section pipeline-excluded files
>   from the `test` bucket count.

## Per-line classification

### rg1 `\.Upsert\(` — generic `.Upsert(` callers

#### production — must use dispatcher (must migrate to `AssetMutationDispatcher.EnqueueAndIndex`)

Task 2 (handlers):

| file:line | context | remediation |
|---|---|---|
| internal/api/assets/clips/clip_create.go:55       | `h.assetRepo.Upsert(ctx, &clip)` (HTTP handler) | rewrite to dispatcher |
| internal/api/assets/clips/clip_upload.go:252      | `h.assetRepo.Upsert(ctx, clip)` (HTTP upload) | rewrite to dispatcher |
| internal/api/assets/clips/clip_action.go:228      | `h.assetRepo.Upsert(ctx, clip)` (HTTP action handler) | rewrite to dispatcher |
| internal/api/assets/clips/bulk_upload_worker.go:364 | `h.clipsRepo.Upsert(ctx, clip)` (HTTP bulk) | rewrite to dispatcher |
| internal/api/assets/clips/clip_ops.go:296          | `repo.Upsert(ctx, clip)` (HTTP bulk ops, clip target) | rewrite to dispatcher |
| internal/api/assets/soundeffect/handler.go:256    | `h.clipsRepo.Upsert(ctx, &clip)` (soundeffect HTTP) | rewrite to dispatcher |

Task 2 — mixed files (whole-file migration, voiceover/locations ride along):

| file:line | context | file's dominant semantic |
|---|---|---|
| internal/application/assets/artifacts/clips_adapter.go:62 | `r.assets.Upsert(ctx, m)` (artifact finalizer → clips) | clips adapter; L75/L89 are locations, ride along |
| internal/application/assets/ingest/adapter_clip.go:73, 229 | `a.repo.Upsert(ctx, m)` + `a.Upsert(ctx, rec)` (ingest → clips) | clip adapter; L86/L100 are locations, ride along |

Task 2 (application-layer workers):

| file:line | context | remediation |
|---|---|---|
| internal/application/clips/reprocess.go:90       | `uc.assetRepo.Upsert(ctx, clip)` (re-encode pipeline) | rewrite to dispatcher |
| internal/application/clips/enrich.go:92          | `uc.assetRepo.Upsert(enrichCtx, clip)` (metadata enrich) | rewrite to dispatcher |
| internal/application/clips/clip_ops.go:429       | `repo.Upsert(ctx, clip)` (bulk clip ops) | rewrite to dispatcher |
| internal/application/clips/bulk_upload_worker.go:417 | `w.repo.Upsert(ctx, clip)` (bulk upload) | rewrite to dispatcher |

Task 3 (media-curate + youtube + ai):

| file:line | context | remediation |
|---|---|---|
| internal/application/assets/providers/artlist/semantic_enricher.go:233 | `e.repo.Upsert(ctx, existing)` (enriched metadata writeback) | rewrite to dispatcher |
| internal/application/images/nvidia_animate.go:90 | `s.stockRepo.Upsert(ctx, clip)` (NVIDIA animate result) | rewrite to dispatcher |
| internal/application/youtube/extraction/intelligence.go:267 | `s.clips.Upsert(ctx, clip)` | rewrite to dispatcher |
| internal/application/youtube/orchestrator.go:46  | `s.assetRepo.Upsert(ctx, clip)` | rewrite to dispatcher |
| internal/application/youtube/metadata/service.go:114 | `s.assetRepo.Upsert(ctx, existing)` (metadata backfill) | rewrite to dispatcher |
| internal/application/youtube/metadata/service.go:296 | `s.assetRepo.Upsert(ctx, existing)` (metadata refresh) | rewrite to dispatcher |
| internal/infrastructure/ai/autotag/autotag.go:109 | `s.repo.Upsert(ctx, a)` (autotag metadata writeback) | rewrite to dispatcher |
| internal/infrastructure/ai/autotag/autotag.go:154 | same as above, post-enrichment writeback | rewrite to dispatcher |

#### tx primitive — internal only OK (kept; already migrated behind dispatcher)

| file:line | context | reason NOT a bypass |
|---|---|---|
| internal/infrastructure/database/sqlite/assets/clips_repository.go:337 | `return r.Upsert(ctx, clip)` | The canonical ClipsRepository owns the SQL primitive; dispatcher-context tail |
| internal/infrastructure/database/assetindex/service.go:17, 73 | `s.repo.Upsert(ctx, rec)` | AssetIndex cache surface — separate from ClipsRepository; dispatcher does NOT own this table |
| internal/application/assets/mutations/primitives.go | interface `UpsertClip(...) error` | the interface DECLARATION site, not a caller |
| internal/app/adapters_infra.go:40 | `a.repo.Upsert(ctx, clip)` | composition-root adapter delegate; rewires to AssetMutationDispatcher at next composition PR |
| internal/app/youtube_adapters.go:45 | `a.inner.Upsert(ctx, clip)` | composition-root adapter |
| internal/app/clips_adapters_repo.go:33 | `a.inner.Upsert(ctx, clip)` | composition-root adapter |

#### non-media-assets caller (kept; out of scope for AssetMutationDispatcher)

| file:line | context | target table |
|---|---|---|
| internal/application/channels/adapters.go:52 | `a.repo.Upsert(ctx, ch)` | `channels` |
| internal/application/channels/service.go:193, 218 | domain upserts | `channels` |
| internal/application/channels/handler.go:127 | HTTP channel upsert | `channels` |
| internal/application/voiceover/sync/service.go:217 | `s.repo.Upsert(ctx, rec)` | `voiceovers` |
| internal/application/voiceover/registry_adapter.go:18 | voiceover registry | `voiceovers` |
| internal/api/assets/handler_searchqueries.go:129 | HTTP search-queries upsert | `search_queries` |
| internal/application/assets/searchqueries/usecase.go:82 | `uc.repo.Upsert(ctx, q)` | `search_queries` |
| internal/application/assets/artifacts/finalizer.go:126 | `f.assetIndex.Upsert(ctx, assetRec)` | `assetindex` cache |
| internal/application/assets/catalogsync/sync_persist.go:83 | `s.assetIndex.Upsert(ctx, rec)` | `assetindex` cache |
| internal/application/jobs/assets/service.go (3 hits, L130, 195, 224) | `s.assetIndex.Upsert(ctx, rec)` | `assetindex` cache |
| internal/application/assets/providers/stock/stockpipeline/run_upload.go:100 | `s.assetIndex.Upsert(ctx, rec)` | stockpipeline records |
| internal/application/images/google_vids_assets.go:111, 268 | `s.stockRepo.Upsert(ctx, clip)` | stockpipeline records (NOT media_assets) |
| internal/application/assets/ingest/adapter_image.go:119 | `a.Upsert(ctx, rec)` | image registry |
| internal/application/assets/ingest/adapter_voiceover.go:24 | `a.repo.Upsert(ctx, mediaRecordToVoiceover(rec))` | `voiceovers` |
| internal/application/assets/ingest/adapter_voiceover.go:107 | `a.Upsert(ctx, rec)` | `voiceovers` |
| internal/app/clips_adapters_repo.go:138 | `a.inner.Upsert(ctx, voiceoverDTOToRecord(dto))` | `voiceovers` |

> Voiceover lines inside `clip_ops.go` (L305 in api/, L440 in
> application/) are NOT in this list because the file is now filed in
> task-2 (mixed) — the wholesale file rewrite will touch all lines
> together (clip lines become dispatcher calls; voiceover lines
> remain on the voiceover narrow port).

#### test (no action; pre-excluded by rg glob)

| file | hits |
|---|---|
| internal/infrastructure/database/assetindex/service_test.go | 7 |
| internal/application/assets/searchqueries/usecase_test.go | 1 |
| internal/application/channels/module_test.go | 2 |
| internal/application/jobs/assets/service_test.go | 2 |
| internal/application/assets/providers/artlist/service_test.go | multiple (in rg2 list) |
| internal/application/assets/providers/artlist/dispatcher_stub_test.go | multiple (in rg2 list) |
| internal/application/qdrant/dr/snapshot_test.go | 4 (in rg3 list) |

### rg2 `UpsertClip\(` — direct calls to the dispatcher-only primitive

| file:line | bucket | reason |
|---|---|---|
| internal/infrastructure/database/sqlite/outbox/repository.go:588 | tx primitive | comment-only (`// repo.UpsertClip(...)`) inside the dispatcher; documentation, not a call |
| internal/infrastructure/database/sqlite/assets/clips_repository.go:336 | tx primitive | the `UpsertClip` method definition on `*ClipsRepository` |
| internal/application/assets/mutations/primitives.go:72 | tx primitive | comment inside the interface (`UpsertClip(ctx, clip)` docblock) |
| internal/application/assets/mutations/primitives.go:89 | tx primitive | interface method declaration on `AssetMutationPrimitives` |
| internal/application/assets/providers/artlist/service_test.go:60 | test | test fixture |
| internal/application/assets/providers/artlist/dispatcher_stub_test.go:55 | test | test stub delegate |

### rg3 `\.Restore\(` — restore-path callers

| file:line | bucket | reason |
|---|---|---|
| cmd/admin/db_restore.go:34                           | admin | `storage.Restore(...)` operator tool |
| cmd/admin/dr_qdrant.go:348                           | admin | `svc.Restore(ctx, dr.RestoreOptions{...})` operator tool |
| internal/infrastructure/database/sqlite/admin/purge.go:72 | admin (already allowlisted) | `s.repo.Restore(ctx, id)` in `PurgeService.RestoreClip` |
| internal/application/qdrant/dr/snapshot_test.go (4 hits) | test | Qdrant DR mocked test |

> Pre-PR-1 callers of `\.Restore\(` (excluding the 3 admin + 4 test hits) had been
> migrated by the Wave 22 PR-1 commit. 0 production callers remain.

### rg4 `\.HardDelete\(` — physical-purge callers

| file:line | bucket | reason |
|---|---|---|
| internal/infrastructure/database/sqlite/admin/purge.go:62 | admin (already allowlisted) | `s.repo.HardDelete(ctx, id)` in `PurgeService.HardDeleteClip` |
| internal/infrastructure/database/sqlite/assets/clips_repository.go:425 | tx primitive | `return r.HardDelete(ctx, id)` (definition site; the Wraps-the-new-method shim) |

> Both `\.HardDelete\(` hits are already safe under the Wave 22 PR-1 contract
> (admin-only or dispatcher-internal).

### rg5 `ExecContext.*media_assets`

0 hits — already cleared by the prior QDRANT-001 wave. The CI gate retains
this query as a regression guard; if it ever re-fires, the offending file
MUST be migrated through `mutations.AssetMutationDispatcher.EnqueueAndIndex`,
not via raw `db.ExecContext`.

## DoD cross-check (single command — the gate is the source of truth)

```bash
bash scripts/ci-bypass-audit.sh ; echo "exit: $?"
```

A passing run prints `Bypass audit: 5 gates pass; ...` and exits 0.
A failing run lists non-allowlisted files + their offending lines.

## Migration plan

| Wave 22 task | Files migrating in that task |
|---|---|
| task 2 (handlers) | api/assets/clips/{bulk_upload_worker,clip_action,clip_create,clip_ops,clip_upload}.go + api/assets/soundeffect/handler.go + application/clips/{reprocess,enrich,clip_ops,bulk_upload_worker}.go + application/assets/{ingest/adapter_clip,artifacts/clips_adapter}.go (mixed) |
| task 3 (media-curate + restore) | images/{google_vids_assets,nvidia_animate}.go, youtube/{extraction/intelligence,orchestrator,metadata/service}.go, artlist/semantic_enricher.go, ai/autotag/autotag.go |
| task 5 (deprecate ad-hoc ports) | collapse `IndexDispatcherPort` passthrough in sourcing + YouTube metadata to single AssetMutationDispatcher consumer |

Each migration PR MUST:
1. Reshape the caller to invoke `mutations.AssetMutationDispatcher.{EnqueueAndIndex,EnqueueAndRestore,EnqueueAndDelete}`.
2. Remove the file from `docs/migrations/admin-sql-allowlist.txt` in the
   SAME PR (atomic).
3. Add or update the unit test for the new dispatcher-shaped call path.
4. Pass `bash scripts/ci-architectural-checks.sh` and
   `bash scripts/ci-bypass-audit.sh` clean.
