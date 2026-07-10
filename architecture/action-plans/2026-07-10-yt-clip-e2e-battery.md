# YT-CLIP-E2E-BATTERY — YouTube Clip Registration E2E Test Battery

**Status**: in_progress
**Owner**: Marcuss-ops
**Created**: 2026-07-10
**Canonical test script**: `tests/operational/yt_clip_register_e2e_battery.sh`

---

## §0 — Purpose

Comprehensive end-to-end verification of the YouTube clip download and registration pipeline. The battery covers 15 test scenarios validating the full chain:

```
YouTube URL → validation → yt-dlp download → FFmpeg cut → local file →
hash → Drive upload → media_assets DB → outbox event → Qdrant index →
search discoverable → duplicates/idempotency → cleanup
```

## §1 — Canonical Endpoints

| Endpoint | Mode | Purpose |
|----------|------|---------|
| `POST /api/media/register-from-youtube` | Sync | Single clip registration |
| `POST /api/media/register-batch` | Async | Batch registration + fan-out |

## §2 — Test Matrix (15 Scenarios)

| # | Scenario | Endpoint | Expected | Failure Signal |
|---|----------|----------|----------|----------------|
| 01 | Invalid URL | register-from-youtube | HTTP 400 | URL validation bypass |
| 02 | Single 5s clip | register-from-youtube | ok=true, all fields populated | Missing clip_id/drive/file_hash |
| 03 | File exists + ffprobe | (local check) | File >0, codec present | Silent-success: API says ok, no file |
| 04 | DB media_assets | (SQL check) | source=youtube, ACTIVE | Drive ok but DB empty |
| 05 | Drive file exists | (Drive API) | File visible in folder | Drive link dead |
| 06 | Outbox event | (SQL check) | asset.index.requested created | Clip created, no outbox |
| 07 | Qdrant + search | Qdrant scroll + /api/media/search | Asset findable | Outbox completed but Qdrant empty |
| 08 | Duplicate/idempotency | register-from-youtube | Same clip_id, no uncontrolled dup | Uncontrolled duplicates |
| 09 | Batch 3 clips | register-batch | ok=true, enqueued=3 | Batch enqueues 0 |
| 10a | Single rejects segment | register-from-youtube | HTTP 400 | seconds_per_segment leaks to single |
| 10b | Batch auto-segments | register-batch | 4 clips (0-5, 5-10, 10-15, 15-20) | Fan-out produces wrong count |
| 11 | no_audio | register-from-youtube | File has no audio stream | Audio not stripped |
| 12 | Bad range | register-batch | HTTP 400 | Invalid range accepted |
| 13 | Drive fail-closed | register-batch | HTTP 503 (when Drive not wired) | Silent success with bad folder |
| 14 | Concurrency 5x | register-from-youtube | No DB lock, no panic | database is locked |
| 15 | Temp cleanup | (filesystem) | No .part accumulation | Disk growth |

## §3 — Prerequisites

```bash
# Required tools
yt-dlp --version
ffmpeg -version | head -1
ffprobe -version | head -1
sqlite3 --version

# Required env
export BASE="http://127.0.0.1:8000"
export DB="data/media/media.db.sqlite"
export FOLDER_ID="<your-test-drive-folder-id>"
export VELOX_ADMIN_TOKEN="<your-token>"
```

## §4 — Failure-to-PR Mapping

| Failure Signal | Canonical PR | Owner File |
|---------------|-------------|------------|
| URL invalid not caught | REG-T06-001 | `internal/api/assets/register/handler.go` |
| clip_id/drive empty | PR-CLIPS-REGISTER-TEST | `internal/application/assets/sourcing/` |
| File not on disk | PR-YT-DOWNLOAD-CLEANUP | `internal/application/youtube/` |
| DB row missing | PR-MEDIA-ASSETS-PROJECTION | `internal/application/assets/finalizer/` |
| Drive link dead | PR-DRIVE-AVAILABILITY-GATE | `internal/app/build_bundles_drive_gates.go` |
| Outbox missing | PR-OUTBOX-EMISSION | `internal/application/assets/finalizer/` |
| Qdrant empty | PR-QDRANT-INDEXCLIP-GUARD | `internal/infrastructure/indexing/clipindexer/` |
| Duplicate not caught | PR-IDEMPOTENCY-KEY | `internal/api/middleware/idempotency.go` |
| Batch enqueue=0 | PR-BATCH-DISPATCH | `internal/application/assets/sourcing/batch/` |
| Auto-segment wrong count | PR-SECONDS-PER-SEGMENT-WIRE | `internal/api/assets/register/handler.go` |
| no_audio not stripped | PR-NO-AUDIO-FFMPEG | `internal/infrastructure/media/ffmpeg/` |
| Bad range accepted | PR-BATCH-RANGE-VALIDATION | `internal/api/assets/register/handler.go` |
| Drive not fail-closed | PR-DRIVE-AVAILABILITY-GATE | `internal/app/build_bundles_drive_gates.go` |
| DB locked under concurrency | PR-DB-BUSY-TIMEOUT | `internal/infrastructure/database/sqlite/` |

## §5 — Execution

```bash
# Full battery (requires live server + Drive + Qdrant)
bash tests/operational/yt_clip_register_e2e_battery.sh

# Offline mode (skip Drive + Qdrant tests)
SKIP_DRIVE=1 SKIP_QDRANT=1 bash tests/operational/yt_clip_register_e2e_battery.sh

# Custom server
BASE=http://localhost:8081 bash tests/operational/yt_clip_register_e2e_battery.sh
```

## §6 — Exit Codes

| Code | Meaning |
|------|---------|
| 0 | ALL tests PASS |
| 1 | One or more tests FAILED |
| 2 | Prerequisite missing (server down, tools absent) |

## §7 — Wave-Flip Criterion

Wave flips to `status: shipped` when ALL 15 assertions green against a live server:
- Tests 01-08, 11-15: individual assertions
- Tests 09-10: batch + fan-out
- Test 14: concurrency safety

## §8 — Cross-References

- `architecture/action-plans/2026-07-09-youtube-clip-register-e2e-testing.md` (predecessor plan)
- `architecture/action-plans/2026-07-04-smoke-test-checklist.md` (broader smoke suite)
- `tests/operational/lib/common.sh` (shared helpers)
- `internal/api/assets/register/handler.go` (canonical handler)
- `internal/application/assets/sourcing/service.go` (canonical service)

## §9 — Co-authored-by

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
