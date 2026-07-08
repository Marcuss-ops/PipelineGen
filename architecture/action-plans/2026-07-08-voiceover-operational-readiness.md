# VO-OPERATIONAL-READINESS-2026-07-08 — Voiceover Deploy Readiness Action Plan

**Source**: Operational readiness analysis (2026-07-08) — Marcuss-ops review of
voiceover translation + Drive upload pipeline.

**Status**: in_progress (1/13 PR shipped)
**Deadline**: 2026-08-15 (P0 items), 2026-09-01 (P1 items)

## §0 — Honest Status Snapshot

The voiceover pipeline architecture is well-structured (4-stage pipeline, typed ports,
canonical Publisher, atomic finalizer, outbox events). However, the following gaps
must be closed before production deployment:

- **FASE 1 CRITICAL** (shipped): Language/Project propagation to VoiceoverPublishCommand
- **FASE 2**: Contract tests per stage (TTS, Drive upload, finalizer)
- **FASE 3**: True idempotency (job-level idempotency key + unique DB constraint)
- **FASE 4**: Transaction boundary hardening (cleanup after DB failure post-Drive upload)
- **FASE 5**: Observability (job-level structured logging + Prometheus metrics)
- **FASE 6**: Healthcheck hardening (/readyz with all dependency probes)
- **FASE 7**: E2E smoke test (make smoke-voiceover)
- **FASE 8**: Concurrency limits + retry/backoff for Drive/Ollama/TTS
- **FASE 9**: Translation fail-closed in production (cfg.Translation.Required)
- **FASE 10**: Scaling stress tests (50-100 concurrent jobs)
- **FASE 11**: Runbook + operator documentation
- **FASE 12**: Post-deploy verification checklist
- **FASE 13**: Hotspot cross-validation (git-log frequency)

## §1 — Per-FASE Execution Checklist

### FASE 1: Language/Project Propagation ✅ SHIPPED

- [x] Verify ProcessSegmentUseCase.Execute passes Language + Project to VoiceoverPublishCommand
- [x] go build + commit + push
- [x] SHA: `b2e7c9688`

### FASE 2: Contract Tests Per Stage

- [ ] Test TTS: input text+language → valid mp3/wav, non-empty, duration>0
- [ ] Test TTS failure modes: empty text, unsupported language, missing Python script
- [ ] Test Publisher with Language: positive (Language="it-IT") + negative (Language="", expect ErrVoiceoverPublishLanguageRequired)
- [ ] Test Publisher with Project: positive (Project="test-project") + fallback (empty Project)
- [ ] Test finalizer: dedupe gate, delete+insert, media_assets projection, outbox events

### FASE 3: True Idempotency

- [ ] Add job-level idempotency key (job_id + language + content_hash)
- [ ] Add UNIQUE INDEX on voiceovers(job_id, language) via SQLite migration
- [ ] Test: retry same job → no duplicate Drive upload, no duplicate DB row
- [ ] Test: partial failure + retry → same result (no orphan files)

### FASE 4: Transaction Boundary Hardening

- [ ] Test: Drive upload OK + DB finalize FAIL → cleanup event emitted
- [ ] Test: cleanup handler deletes orphan Drive file
- [ ] Verify outbox retry correctly retries cleanup events

### FASE 5: Observability

- [ ] Add structured logging: job_id, asset_id, project, language, stage, duration_ms
- [ ] Add Prometheus metrics: voiceover_jobs_total, voiceover_stage_duration_seconds,
  drive_upload_failures_total, tts_failures_total, translation_failures_total

### FASE 6: Healthcheck Hardening

- [ ] Add /readyz probe: DB, migrations, temp dir, ffmpeg, python TTS, Drive credentials,
  Drive root folder, Ollama, Qdrant (if enabled), outbox worker
- [ ] /readyz returns 503 if any critical dependency fails

### FASE 7: E2E Smoke Test

- [ ] Create tests/operational/voiceover_smoke_test.go
- [ ] Test: POST /api/voiceover/generate → poll job → verify audio on Drive → verify DB row
- [ ] Add make smoke-voiceover target

### FASE 8: Concurrency Limits + Retry

- [ ] Add max-concurrent Drive uploads config (2-5 per worker)
- [ ] Add retry with exponential backoff for Drive (3 attempts: 1s, 3s, 10s)
- [ ] Add timeout for TTS (2 min), Drive upload (5 min), Ollama (2 min)

### FASE 9: Translation Fail-Closed

- [ ] Add cfg.Translation.Required config field
- [ ] When Required=true and translationPort==nil, fail at boot (not silent fallback)
- [ ] When Required=false (dev mode), preserve current graceful degradation

### FASE 10: Scaling Stress Tests

- [ ] Test 10 consecutive jobs → all SUCCEEDED
- [ ] Test 50 queued jobs → no duplicates, no orphans
- [ ] Simulate Drive offline → verify retry + eventual success
- [ ] Simulate Ollama offline → verify fail-closed + retry

### FASE 11: Runbook + Operator Documentation

- [ ] Write docs/operations/voiceover-runbook.md
- [ ] Document: startup order, healthcheck interpretation, common failures, recovery procedures

### FASE 12: Post-Deploy Verification Checklist

- [ ] go test ./... passes
- [ ] All migrations applied on clean DB
- [ ] make smoke-voiceover passes
- [ ] /readyz returns 200 with all checks OK

### FASE 13: Hotspot Cross-Validation

- [ ] Run git log --since=90.days frequency analysis
- [ ] Cross-validate against STATIC priority defined here
- [ ] Append any NEW hotspots to linked_issues per slim-schema append-only ratchet

## §2 — Cross-References

- `architecture/waves/wave_p1_high.yaml#VO-DECOMPOSITION-2026-07-04` (parent voiceover decomposition wave)
- `architecture/waves/wave_p1_high.yaml#PR-P12-DRIVE-COMPLETION-2026-07-08` (Drive as Central Capability)
- `internal/application/voiceover/process_segment.go` (ProcessSegmentUseCase — Stage 3 fix)
- `internal/app/adapters_voiceover_publisher.go` (useCasePublisherAdapter — semantic routing)

## §3 — godlike/06 3-Surface Lockstep

Per CANONICAL.md §1:
- This action plan ≡ CHANGELOG.md `## Unreleased → ### Documentation` mirror
- ≈ `architecture/waves/wave_p1_high.yaml#VO-OPERATIONAL-READINESS-2026-07-08` (to be appended)
- ≡ AGENTS.md mirror entry (via AGENTS.md Git-Lesson-3)
