# Rich Metadata Qdrant Verification Chain — Action Plan

**Date**: 2026-07-04
**Wave-tracker anchor**: `architecture/current.yaml#RICH-METADATA-QDRANT-VERIFY-2026-07-04`
**Umbrella**: `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` (parent wave)

## 1. Context

This action plan codifies the operator testing methodology for the full
Qdrant indexing chain: `media_assets → outbox → Qdrant → search → download`.
The 9-point verification verdict (see §4) was derived from an Italian audit
pasted to the orchestrator on 2026-07-04. The audit covers 8 segments of the
Pacquiao vs Broner WBA welterweight highlight video (`RRJvrDKunyA`).

**Existing coverage**: `tests/operational/qdrant_e2e_boxing_smoke.sh` already
implements Tests 1-6 (index-health + register-batch x2 + media_assets +
outbox + search + download). The gap addressed by THIS plan is **rich
per-segment metadata** — the current wire DTO `RegisterFromYouTubeRequest`
packs all semantic content into `description` (string) + `tags` ([]string),
but the operator methodology calls for dedicated fields:
`summary`, `topics`, `speakers`, `mentioned_people`, `hook`.

## 2. Scope

### Phase 1 — Wire DTO extension (Band A, deadline 2026-07-25)

Add 5 new fields to `RegisterFromYouTubeRequest`:

| Field | Type | Purpose |
|-------|------|---------|
| `summary` | `string` | Rich natural-language description for semantic search |
| `topics` | `[]string` | Concepts to retrieve later via hybrid Qdrant |
| `speakers` | `[]string` | Who is speaking in the clip |
| `mentioned_people` | `[]string` | Named entities in the clip |
| `hook` | `string` | Strong one-liner for YouTube/script consumption |

All fields are `omitempty` for backward compatibility — existing callers
that pack everything into `description` continue to work unchanged.

**Propagation chain** (4 surfaces):

1. `RegisterFromYouTubeRequest` DTO (`internal/api/assets/register/types.go`)
2. `BatchClipPayload` wire DTO (`internal/application/assets/sourcing/batch/service.go` — or wherever the per-clip payload is defined)
3. `media.clip` job payload (JSON wire format consumed by `ClipJobEnqueuer`)
4. `media_assets.metadata_json` persistence (via `ytSvc.Register` inside the media.clip handler's `*sql.Tx`)

### Phase 2 — Smoke test update (Band B, deadline 2026-08-01)

Update `tests/operational/qdrant_e2e_boxing_smoke.sh` (or create a sibling
`qdrant_e2e_rich_metadata_smoke.sh`) that:

1. Uses the new rich fields in `build_batch_payload` (summary, topics, speakers, mentioned_people, hook as separate JSON fields)
2. After extraction, verifies `metadata_json` contains the rich fields via `json_extract(metadata_json, '$.clip_summary')` etc.
3. Runs per-clip natural-language queries that target specific rich descriptions (e.g., "Pacquiao hurts Broner near the ropes in round 7")

### Phase 3 — Operator runbook codification (Band C, deadline 2026-08-15)

Document the 5-test operator sequence in `docs/operations/qdrant-verification-runbook.md`:
- Test 1: `GET /api/media/index-health`
- Test 2: SQLite `media_assets` query (index_state, file_hash, metadata)
- Test 3: SQLite `outbox_events` query (status, supersede detection)
- Test 4: `POST /api/media/search` with per-clip natural-language queries
- Test 5: Download via canonical clip endpoint

## 3. Honest scope-lock (godlike/07)

- The existing `qdrant_e2e_boxing_smoke.sh` already covers Tests 1-6 of the
  9-point verdict. The missing 3 items (per-clip natural-language queries with
  rich descriptions, per-clip download verification, metadata_json field-level
  assertions) are partially covered but the rich-field DTO gap blocks full
  verification.
- The `media.clip` handler (`PR-BATCH-REGISTER-ASYNC`, shipped 2026-07-04)
  already writes `media_assets.metadata_json` — the question is whether the
  rich fields SURVIVE the wire → job payload → metadata_json chain.
- The outbox pipeline (`asset.index.requested → IndexClip → Qdrant`) is
  architecturally sound per the `QDRANT-CHAIN-VERIFY-2026-07-04` audit.

## 4. The 9-point verification verdict

For each clip, ALL of these must pass:

1. `media_assets` row present
2. `file_hash` present
3. `drive_file_id` or `local_path` present
4. Metadata custom present: `clip_summary`, `topics`, `hook`, `tags`
5. `outbox_events` `asset.index.requested` present
6. Outbox event `completed` (or `superseded` only if newer `completed` exists)
7. `media_assets.index_state = INDEXED`
8. `/api/media/search` finds clip with natural-language query
9. `/api/media/{source}/clips/{id}/download` returns valid MP4

## 5. Per-file execution checklist

### 5a — DTO extension (Go files)

- [ ] `internal/api/assets/register/types.go` — add `Summary`, `Topics`, `Speakers`, `MentionedPeople`, `Hook` to `RegisterFromYouTubeRequest`
- [ ] `internal/application/assets/sourcing/batch/service.go` — propagate fields to per-clip payload
- [ ] `internal/application/assets/sourcing/types.go` — add fields to `BatchClipPayload` if needed
- [ ] `internal/api/assets/register/handler.go` — pass fields through to the enqueue payload
- [ ] Any intermediate DTOs in the chain (job payload, metadata writer)

### 5b — Smoke test (bash)

- [ ] Update `build_batch_payload` in `qdrant_e2e_boxing_smoke.sh` to use rich fields
- [ ] Add `json_extract(metadata_json, '$.clip_summary')` assertions in media_assets check
- [ ] Add per-clip natural-language search queries (not generic "Pacquiao Broner")
- [ ] Add per-clip download test (not just first clip)

### 5c — Runbook (markdown)

- [ ] Create `docs/operations/qdrant-verification-runbook.md`
- [ ] Document the `for q in ...; do curl search; done` mini-test loop
- [ ] Document the `for clip_id in ...; do curl download; done` batch verification

## 6. Forward pointers

| Ticket | Deadline | Description |
|--------|----------|-------------|
| `PR-RICH-METADATA-DTO-EXTEND` | 2026-07-25 | Add 5 rich fields to RegisterFromYouTubeRequest + propagate through chain |
| `PR-RICH-METADATA-SMOKE-TEST` | 2026-08-01 | Extend smoke test with rich-field assertions + per-clip queries |
| `PR-RICH-METADATA-RUNBOOK` | 2026-08-15 | Codify operator verification runbook |
| `PR-RICH-METADATA-HOTSPOT-CROSSREF` | 2026-08-15 | Cross-validate priority via git-log frequency |

## 7. Cross-references

- `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` — parent wave (6 linked_issues)
- `architecture/current.yaml#PR-BATCH-REGISTER-ASYNC` — async conversion (shipped 2026-07-04)
- `tests/operational/qdrant_e2e_boxing_smoke.sh` — existing 6-test smoke
- `tests/operational/stock_register_batch_boxing_smoke.sh` — async polling smoke
- `tests/operational/stock_run_boxing_smoke.sh` — full stock pipeline smoke

## 8. Honest-limitation disclosure

- The existing wire DTO was designed for the synchronous register-batch flow.
  The async conversion (`PR-BATCH-REGISTER-ASYNC`) preserved the DTO shape;
  adding rich fields now means touching 4+ intermediate surfaces.
- The `metadata_json` column is free-form JSON — the rich fields will land
  there regardless of DTO changes, but without dedicated fields, callers
  must pack everything into `description` (string) which loses structure.
- Per-clip download tests require the clips to exist on Google Drive, which
  adds ~3-5 min per clip for `yt-dlp` download + cut + upload. This is why
  the existing smoke only downloads the first clip.
