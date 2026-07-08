# YouTube Clip DoD Action Plan — 2026-07-08

> **Canonical lockstep (per CANONICAL.md §1):** this file ≡
> `architecture/waves/wave_p1_high.yaml#YOUTUBE-CLIP-DOD-2026-07-08` ≡
> CHANGELOG.md `## Unreleased → ### Documentation` ≡
> AGENTS.md mirror entry.

## §0 — Verdict (godlike/07 NO-FAKE-AVAILABILITY)

Il 70-80% dei 12 DoD è **già coperto** dalla pipeline corrente
(`process_segment.go` 9-step pipeline). I gap reali sono:

1. **DoD 7 (metadata_json)** — verificare che tutti i campi richiesti
   siano effettivamente popolati da Step 10 MetadataService
2. **DoD 10 (search_text)** — il search_text deve essere composto da
   titolo+summary+hook+topics+transcript, non solo nome file
3. **DoD 11 (transcript+metadata)** — verificare il partial-state handling
   quando Step 10 fallisce dopo Step 9
4. **DoD 12 (E2E test)** — test pratico con 3 clip reali

## §1 — DoD-by-DoD status snapshot

| DoD | Stato | Cosa c'è già | Cosa manca |
|-----|-------|-------------|------------|
| 1. Single mp4 | ✅ SHIPPED | `process_segment.go` 9-step, no chunk figli, clip ID `yt_VIDEOID_start_end_v1` | Nothing |
| 2. Deterministic ID | ✅ SHIPPED | `yt_<videoID>_<startSec>_<endSec>_<policyVer>` in Step 1 | Nothing |
| 3. Timestamp respect | ✅ SHIPPED | Step 1 validation + SegmentPolicy gate + Step 5a ffprobe | Nothing |
| 4. Local file valid | ✅ SHIPPED | Step 5 fail-closed: empty path, zero-size, hash failure | Nothing |
| 5. Drive upload | ✅ SHIPPED | Step 8 DriveUploadFileIfChanged | Nothing |
| 6. SQLite record | ✅ SHIPPED | Step 9 ClipAtomicWriter single-tx | Nothing |
| 7. metadata_json | ⚠️ PARTIAL | Step 10 MetadataService exists | **Verifica campi obbligatori** |
| 8. Outbox event | ✅ SHIPPED | ClipAtomicWriter emette `asset.index.requested` | Nothing |
| 9. Qdrant point | ✅ SHIPPED | IndexingHandler → IndexClip → Qdrant upsert | Nothing |
| 10. search_text | ⚠️ PARTIAL | `composeStockChunkSearchText` esiste | **Verificare YouTube path** |
| 11. Transcript+metadata | ⚠️ PARTIAL | Step 10 typed-port transcript + Warn log | **TDD lock sul partial-state** |
| 12. E2E test | ❌ MISSING | N/A | **Scrivere test E2E con 3 clip** |

## §2 — Clip specifica (Broner vs Pacquiao)

```
URL:       http://www.youtube.com/watch?v=vdC5GXxS-qU
Timestamp: [00:02:26] - [00:02:35]  (durata 9s)
Titolo:    "La sfuriata contro Pacquiao (Pensa a me, non a Floyd!)"
Folder:    https://drive.google.com/drive/folders/1iAGhWidRF0hpJYvku_fIavEIY50_V1wA

Clip ID atteso:  yt_vdC5GXxS-qU_146_155_v1
Filename atteso: yt_vdC5GXxS-qU_146_155_v1_la-sfuriata-contro-pacquiao-pensa-a-me-non-a-floyd.mp4
```

## §3 — Per-PR migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

### PR-YT-DOD-7-METADATA-JSON (P0, deadline 2026-08-01)
**Verifica DoD 7**: tutti i campi obbligatori di `metadata_json` sono popolati.
- Audit del `CanonicalClipMetadata` struct + `ClipAtomicWriterAdapter` write path
- Verifica: `source_url`, `source_provider=youtube`, `video_id`, `clip_start_sec`,
  `clip_end_sec`, `clip_duration_sec`, `title/name`, `summary`, `topics`,
  `speakers`, `mentioned_people`, `hook`, `normalized_group`, `policy_version`,
  `drive_path`, `content_hash`
- TDD test: `TestClipAtomicWriter_MetadataJSON_AllRequiredFields`
- **5 file**: `clip_atomic_writer.go` + `clip_atomic_writer_test.go` +
  `clip_metadata_writer.go` + `process_segment_helpers.go` + `dto/clip_metadata.go`
- **Verification**: gofmt/vet/build + targeted `go test -short`

### PR-YT-DOD-10-SEARCH-TEXT (P0, deadline 2026-08-01)
**Verifica DoD 10**: search_text per clip YouTube contiene titolo+summary+hook+topics.
- Audit di `buildClipAsset` → `search_text` field nel `media_assets` INSERT
- Verifica che il search_text NON sia solo `yt_vdC5GXxS-qU_146_155_v1.mp4`
- Se vuoto/mancante: aggiungere `composeYouTubeClipSearchText` in `process_segment_helpers.go`
- TDD lock: `TestBuildClipAsset_SearchText_ContainsTitleSummaryHookTopics`
- **3 file**: `process_segment_helpers.go` + `clip_atomic_writer.go` +
  `process_segment_helpers_test.go`
- **Verification**: gofmt/vet/build + targeted `go test -short`

### PR-YT-DOD-11-PARTIAL-STATE (P1, deadline 2026-08-15)
**Verifica DoD 11**: partial-state handling quando Step 10 fallisce dopo Step 9.
- Il Warn log "Step 10 failed AFTER clip write" è già presente
- Aggiungere TDD test: `TestProcessSegment_Step10_PartialState_MediaAssetsRowExists`
  che verifica che dopo un FAIL di Step 10, la riga `media_assets` esista
  ancora (Step 9 ha già scritto) e l'outbox event sia presente
- **2 file**: `process_segment_correttezza_test.go` + `process_segment.go` (solo
  se necessario aggiungere un test seam)
- **Verification**: gofmt/vet/build + targeted `go test -short`

### PR-YT-DOD-12-E2E-TEST (P0, deadline 2026-08-01)
**Implementa DoD 12**: test E2E con 3 clip reali.
- Nuovo file `tests/e2e/youtube_clip_dod_e2e_test.go` (~500 LoC)
- 3 clip su `vdC5GXxS-qU` (il video Pacquiao/Broner):
  1. [00:02:26]-[00:02:35] "Sfuriata contro Pacquiao" (9s)
  2. Un'altra clip dal video (TBD)
  3. Un'altra clip dal video (TBD)
- Schema: httptest.NewServer + stub yt-dlp/Drive + sqlite in-memory
- 3 asserzioni per clip: mp4 su Drive, media_assets row, outbox completed,
  Qdrant point
- **1 file**: `tests/e2e/youtube_clip_dod_e2e_test.go`
- **Verification**: gofmt/vet/build isolato; `go test -short -count=1`

### PR-YT-DOD-HOTSPOT-CROSSREF (forward-pointer, deadline 2026-08-15)
Post-wave git-log frequency cross-validation. `git log --since=90.days ...`
per verificare che nessun hotspot ad alta frequenza sia stato omesso.

## §4 — Execution order

```
PR-YT-DOD-7  (metadata_json verification)
PR-YT-DOD-10 (search_text verification)     ← parallelo con DOD-7
PR-YT-DOD-11 (partial-state TDD)            ← dipende da DOD-7 + DOD-10
PR-YT-DOD-12 (E2E test)                     ← dipende da tutti i precedenti
PR-YT-DOD-HOTSPOT-CROSSREF                  ← post-wave audit
```

## §5 — Verification gates (per-PR)

Ogni PR:
- `gofmt -l` clean sul subtree target
- `go vet ./internal/...targeted...` exit 0
- `go build ./internal/...targeted...` exit 0
- `go test -short -count=1 ./internal/...targeted...` PASS

## §6 — Honest scope-lock (godlike/07)

- **DoD 1-6, 8-9 sono già SHIPPED** — nessun codice da toccare.
  L'action plan documenta SOLO la verifica e il lock dei contratti esistenti.
- **DoD 7 + 10** potrebbero richiedere fix minime se i campi non sono popolati.
- **DoD 12** è il più sostanzioso (nuovo file di test).
- **NON** in scope: refactor della pipeline, nuove feature, cambiamenti di
  architettura. Il piano è puramente DoD-verification-and-lock.

## §7 — Cross-references (godlike/06 umbrella)

- `architecture/waves/wave_p1_high.yaml#PR-STOCK-TIMESTAMP-CLIPS` — sister wave
- `architecture/waves/wave_p1_high.yaml#QDRANT-DOD-FINAL-2026-07-08` — Qdrant DoD
- `internal/application/youtube/usecase/process_segment.go` — canonical 9-step pipeline
- `internal/infrastructure/database/sqlite/assets/clip_atomic_writer.go` — ClipAtomicWriter
- `internal/application/jobs/outbox/indexing.go` — IndexingHandler

## §8 — Wave-flip criterion

Wave flips `status: shipped + exit_signal: true` quando tutti i 4 PR
(PR-YT-DOD-7, PR-YT-DOD-10, PR-YT-DOD-11, PR-YT-DOD-12) sono `status: shipped`
E il forward-pointer PR-YT-DOD-HOTSPOT-CROSSREF non surfaces new hotspots.

## §9 — Lifecycle audit-trail

| Data | Evento |
|------|--------|
| 2026-07-08 | Action plan creato da audit DoD 12 punti |
| TBD | PR-YT-DOD-7 shipped |
| TBD | PR-YT-DOD-10 shipped |
| TBD | PR-YT-DOD-11 shipped |
| TBD | PR-YT-DOD-12 shipped |
| TBD | Wave flipped shipped |

## §10 — Co-authored-by

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
AGENTS.md Git-Lesson-3.
