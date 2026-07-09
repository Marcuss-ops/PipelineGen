# YouTube Clip Register E2E Testing — Action Plan

**Date**: 2026-07-09
**Status**: in_progress
**Owner**: Marcuss-ops
**Canonical endpoints**: `POST /api/media/register-from-youtube` + `POST /api/media/register-batch`
**Test video**: `https://www.youtube.com/watch?v=jNQXAC9IVRw` (Me at the zoo, 19s)

---

## §0 — Status Snapshot

The full YouTube clip download chain:
```
YouTube URL → validation → yt-dlp download → FFmpeg cut → local file → hash →
Drive upload → media_assets DB → outbox asset.index.requested → Qdrant/search →
duplicates/idempotency → cleanup/errors
```

All 18 tests must pass before declaring YouTube clip pipeline production-ready.

---

## §1 — Prerequisites (Gate 0)

Before running any test, verify:
- [ ] `yt-dlp --version` succeeds
- [ ] `ffmpeg -version | head -1` succeeds
- [ ] `ffprobe -version | head -1` succeeds
- [ ] `sqlite3 "$DB" "SELECT 1;"` succeeds
- [ ] Drive canary: upload a test file, get back a real `drive_file_id`

---

## §2 — Test Matrix (18 tests)

| # | Test | Method | Expected | Status |
|---|------|--------|----------|--------|
| 0 | Prerequisites | CLI | yt-dlp + ffmpeg + sqlite3 + Drive canary OK | pending |
| 1 | Invalid URL | `POST /register-from-youtube` with `url: "not-a-url"` | 400 Bad Request | pending |
| 2 | Single 5s clip | `POST /register-from-youtube` with 0-5s range | `ok=true`, all fields populated | pending |
| 3 | Local file exists | Check `$LOCAL_PATH` | file > 0 bytes, ffprobe duration ~5s | pending |
| 4 | DB media_assets | `SELECT ... FROM media_assets WHERE id='$CLIP_ID'` | source=youtube, ACTIVE, hash populated | pending |
| 5 | Drive real | Open `drive_link` | file visible, non-zero, correct name | pending |
| 6 | Outbox events | `SELECT ... FROM outbox_events WHERE aggregate_id='$CLIP_ID'` | asset.index.requested, completed/empty last_error | pending |
| 7 | Qdrant + search | Qdrant scroll + `/api/media/search` | clip findable, source=youtube | pending |
| 8 | Idempotency | Repeat request with same Idempotency-Key | no duplicates, same clip_id/hash | pending |
| 9 | Batch 3 clips | `POST /register-batch` with 3 items | ok=true, enqueued=3, poll to completed | pending |
| 10 | Auto-segmentation | `POST /register-batch` with seconds_per_segment=5 on 0-20s | 4 clips: 0-5, 5-10, 10-15, 15-20 | pending |
| 10b | Single rejects segment | `POST /register-from-youtube` with seconds_per_segment | 400 Bad Request | pending |
| 11 | no_audio | `POST /register-from-youtube` with `no_audio: true` | file has no audio stream | pending |
| 12 | Bad range | start=20, end=10 with seconds_per_segment | 400 Bad Request | pending |
| 13 | Drive not configured | fake folder_id | 503 Service Unavailable (not silent success) | pending |
| 14 | Concurrency 5x | parallel xargs 5 requests | no 500, no database lock, no panic | pending |
| 15 | Cleanup temporals | check /tmp for stale files | no .part, no giant temp dirs | pending |

---

## §3 — Failure → PR Mapping

| Failure signature | Canonical owner |
|---|---|
| URL validation returns 200 instead of 400 | `internal/api/assets/register/handler.go::RegisterFromYouTube` |
| yt-dlp download fails | `internal/infrastructure/downloader/downloader.go` |
| FFmpeg cut fails | `internal/infrastructure/media/render/cutter.go` |
| local_path empty after ok=true | `internal/application/assets/sourcing/youtube/usecase/` |
| drive_file_id empty after ok=true | `internal/infrastructure/drive/publisher.go` |
| media_assets row missing | `internal/infrastructure/database/sqlite/assets/` |
| outbox event missing | `internal/infrastructure/database/sqlite/outbox/` |
| Qdrant point missing | `internal/infrastructure/qdrant/indexing/` |
| Duplicate created on replay | idempotency middleware in `internal/api/middleware/` |
| Batch job stuck in running | `internal/application/jobs/worker/` |
| seconds_per_segment accepted on single | `internal/api/assets/register/handler.go::RegisterFromYouTube` |
| no_audio file still has audio | `internal/infrastructure/media/render/` |
| Drive folder_id fake → silent success | `internal/application/assets/delivery/publisher.go` (Pattern 12 fail-closed) |
| Database locked on concurrency | `internal/infrastructure/database/sqlite/` WAL mode |

---

## §4 — Execution Order

1. **Gate 0**: Run prerequisites first — if yt-dlp/ffmpeg/Drive canary fails, stop.
2. **Tests 1-8**: Single clip chain (sequential, each depends on prior).
3. **Tests 9-10**: Batch tests (can run after single chain verified).
4. **Tests 11-13**: Feature/edge-case tests (independent of each other).
5. **Test 14**: Concurrency (run last, requires stable pipeline).
6. **Test 15**: Cleanup verification (run after all clip tests).

---

## §5 — Verification Gate

Wave-flip to `status: shipped` requires ALL 18 rows green:
- All HTTP status codes match expected
- All DB queries return expected values
- All Drive files are real and accessible
- No `database is locked`, no `panic`, no `nil pointer` in logs
- Concurrency test: 5/5 requests succeed
- Zero stale temp files after test run

---

## §6 — Execution Variables

```bash
export BASE="http://127.0.0.1:8080"
export DB="data/media/media.db.sqlite"
export FOLDER_ID="<INSERISCI_CARTELLA_DRIVE_TEST>"
mkdir -p yt-clip-tests
```

---

## §7 — Honest Scope-Lock (godlike/07)

This action plan is STATIC — it defines the test matrix and expected outcomes.
Actual test execution requires a live PipelineGen server with yt-dlp + ffmpeg + Drive configured.
The wave-flip is gated on operator-verified results, not agent-fabricated pass/fail.
