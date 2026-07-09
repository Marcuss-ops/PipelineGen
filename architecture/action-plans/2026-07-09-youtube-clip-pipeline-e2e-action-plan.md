# YouTube Clip Pipeline E2E Action Plan (2026-07-09)

**Wave ID:** `YOUTUBE-CLIP-DOD-2026-07-09`
**Author:** Marcuss-ops
**Date:** 2026-07-09
**Status:** pending
**Deadline:** 2026-08-08 (P0 absolute — godlike/07 NO-FAKE-AVAILABILITY contract)

---

## §0 — Honest status snapshot (godlike/07 NO-FAKE-AVAILABILITY)

The YouTube clip pipeline (`process_segment.go` + `step_publish.go` + `clipindexer.IndexClip`) is **architetturalmente ben messo** at the static level (canonical chain: extract → stage → cut → publish → index) but has **zero operator-facing E2E diagnostic surface** on `origin/main` — confirmed via:
- `find tests/operational/ -name '*youtube*' -o -name '*yt*' 2>/dev/null` returns only `yt_extraction_verify.sh` (a single-clip integration probe, NOT a battery).
- The STK-E2E-BATTERY-2026-07-05 closure shipped 8 hermetic shell smokes for the stock pipeline. No parallel battery exists for the YouTube path.
- 4 of the 12 DoD gates from the 2026-07-04 stock → Qdrant chain (`PR-YT-DOD-7-METADATA-JSON-AUDIT` + `PR-YT-DOD-10-SEARCH-TEXT-CONTRACT-TDD` + `PR-YT-DOD-11` + `PR-YT-DOD-12-E2E-FULL`) have shipped TDD probes for the in-process layer; the operator-facing E2E surface that maps to those DoD gates is missing.

**Verdict (godlike/07 NO-FAKE-AVAILABILITY):** the YouTube pipeline cannot currently be declared E2E-functional without a 4-point must-pass chain that asserts the canonical 4 surfaces (clip persisted in `media_assets` → outbox `asset.index.requested` completed → Qdrant point present with `lifecycle_state=ACTIVE` → unified search returns the result). This action plan ships that battery.

---

## §1 — Goal

Ship `tests/operational/youtube_e2e_full_battery.sh` + 4 hermetic shell smokes (YT-E2E-A → YT-E2E-D) that exercise the canonical 4-must-pass chain against a live PipelineGen server on port 8000 OR 8081, mirroring the STK-E2E-BATTERY-2026-07-05 structure (action plan + battery aggregator + 8 individual probes), so an operator (or CI) can verify the YouTube clip pipeline is end-to-end functional in <60 seconds with operator-readable FAIL signatures mapped to canonical PR-YT-DOD-* forward-pointers per godlike/06 SSOT one-canonical-owner-per-fact.

---

## §2 — Canonical 4-must-pass chain (per godlike/07 NO-FAKE-AVAILABILITY)

The battery asserts 4 mandatory gates; a probe FAIL with one of the canonical signatures is the PR-YT-DOD-* forward-pointer that MUST ship BEFORE the wave flips. The 4 gates mirror the 4 per-clip assertions in `tests/e2e/youtube_clip_dod_e2e_test.go::TestE2E_YouTubeClip_DoD12_BronerPacquiao` (commit `93c196cfe`, 2026-07-04) but at the operator-facing HTTP/SQL surface (not the in-process TDD surface):

| # | Gate | Probe | Canonical FAIL→PR forward-pointer | Owner file path |
|---|------|-------|-----------------------------------|-----------------|
| 1 | Clip persisted in `media_assets` | YT-E2E-D (sqlite3 SELECT) | `PR-YT-DOD-7-METADATA-JSON-AUDIT` (returns empty metadata_json) | `internal/infrastructure/database/sqlite/assets/clip_metadata_writer.go` |
| 2 | Outbox `asset.index.requested.status='completed'` | YT-E2E-E (sqlite3 SELECT) | `PR-YT-DOD-8` (status='retry_pending' or 'dead_lettered') | `internal/infrastructure/database/sqlite/outboxevents/repository.go` |
| 3 | Qdrant point present with `lifecycle_state=ACTIVE` | YT-E2E-F (Qdrant REST scroll) | `PR-YT-DOD-9` (point missing) | `internal/infrastructure/qdrant/indexing/index_writer.go` |
| 4 | Unified search returns the result | YT-E2E-F (hybrid `/api/media/search`) | `PR-YT-DOD-9` (empty result) | `internal/application/search/handler.go` |

The battery aggregator (YT-E2E-H) tallies `passed / 4 gates` and prints the verdict; on `4/4 PASS` + `WRAPPER_BOOKKEEPING=1` env var, the wrapper flips `architecture/current.yaml#YOUTUBE-CLIP-DOD-2026-07-09` to `status: shipped + exit_signal: true` via the canonical 6-step recipe (yaml pre-flight visual dump via `rg -A 20` + block-aware Python surgery + `commit -F` + race-protect + push).

---

## §3 — Per-probe topology (godlike/06 SSOT one-canonical-owner-per-fact)

### YT-E2E-A — Route aliveness smoke
- **Hermetic shell smoke** (`tests/operational/youtube_e2e_route_aliveness_smoke.sh`, ~30 LoC)
- **Action**: `POST /api/script/generate` (canonical v2) with empty `{}` payload → expect HTTP 400 from BindJSON.
- **Exit codes**: 0=PASS, 1=FAIL (404=route unregistered, 503=composition unwired, 200=BindJSON bypass silent-success, 401/403=auth misconfigured).
- **Canonical FAIL→PR**: `PR-YT-DOD-1` (404) or `PR-YT-DOD-2` (503) or `PR-YT-DOD-3` (200 silent-success godlike/07 violation) or `PR-YT-DOD-4` (401/403).
- **Owner**: `internal/app/wire_script.go::registerScriptPostProcessors` + `internal/api/script/handler_flow.go::RegisterRoutes`.

### YT-E2E-B — Search-and-run loop smoke (1 representative video)
- **Hermetic shell smoke** (`tests/operational/youtube_e2e_search_and_run_smoke.sh`, ~80 LoC)
- **Action**: `POST /api/script/generate` with canonical v2 envelope (topic + 1 YouTube URL from canonical test video `vdC5GXxS-qU` per the prior DoD-7 fixture) → poll `/api/jobs/{id}/full` every 3s for 60 iter (~180s) → final state ≥ 1 succeeds on `SUCCEEDED/INDEX_PENDING`.
- **Exit codes**: 0=ALL 1 SUCCEED, 1=FAIL or stuck, 2=prereq missing.
- **Canonical FAIL→PR**: `PR-YT-DOD-5` (404 route) or `PR-YT-DOD-6` (composition) or `PR-YT-DOD-7` (clip metadata) or `PR-YT-DOD-8` (outbox) or `PR-YT-DOD-9` (Qdrant).
- **Owner**: `internal/api/script/handler_jobs.go` (async poll) + `internal/infrastructure/indexing/clipindexer/`.

### YT-E2E-C — Direct URL smoke (1 representative video)
- **Hermetic shell smoke** (`tests/operational/youtube_e2e_direct_url_smoke.sh`, ~50 LoC)
- **Action**: `POST /api/script/generate` with `SourceSpec.YoutubeVideoURL=...` (NOT search-and-run) on 1 test video → same poll as B.
- **Exit codes**: 0=PASS, 1=FAIL, 2=prereq missing.
- **Canonical FAIL→PR**: `PR-YT-DOD-7-METADATA-JSON-AUDIT` (clip metadata missing) or `PR-YT-DOD-9` (Qdrant).
- **Owner**: `internal/application/youtube/usecase/extract_important_clips.go` + `internal/application/youtube/jobs/extract_important_clips_job_handler.go`.

### YT-E2E-D — media_assets DB projection probe
- **Hermetic shell probe** (`tests/operational/youtube_e2e_db_assets_smoke.sh`, ~80 LoC)
- **Action**: sqlite3 SELECT against `data/media/media.db.sqlite` `media_assets` WHERE `source='youtube' AND filename LIKE '%clip%'` → verify `file_hash`/`drive_file_id`/`drive_link` non-empty.
- **Exit codes**: 0=PASS, 1=FAIL (some row missing key fields), 2=prereq missing.
- **Canonical FAIL→PR**: `PR-YT-DOD-7-METADATA-JSON-AUDIT` (empty metadata_json) or `PR-YT-DOD-9` (Qdrant projection).
- **Owner**: `internal/infrastructure/database/sqlite/assets/clip_atomic_writer.go` + `internal/infrastructure/database/sqlite/assets/clip_metadata_writer.go`.

### YT-E2E-E — outbox_events DB probe
- **Hermetic shell probe** (`tests/operational/youtube_e2e_db_outbox_smoke.sh`, ~80 LoC)
- **Action**: sqlite3 SELECT against `data/media/media.db.sqlite` `outbox_events` WHERE `event_type='asset.index.requested'` ORDER BY `created_at DESC, id DESC` LIMIT 20 → verify `status IN ('pending','completed')` + `last_error==''` + status NOT 'dead_lettered'.
- **Exit codes**: 0=PASS, 1=FAIL (any dead_lettered / failed / last_error != ''), 2=prereq missing.
- **Canonical FAIL→PR**: `PR-YT-DOD-8-OUTBOX-LAST-ERROR` (last_error) or `PR-YT-DOD-8-OUTBOX-RETRY-EXHAUSTED` (failed) or `PR-YT-DOD-8-OUTBOX-DEAD-LETTERED` (dead_lettered).
- **Owner**: `internal/infrastructure/database/sqlite/outboxevents/repository.go` (rg-verified canonical write seam lines 252 + 266 + 321 + 367).

### YT-E2E-F — Qdrant scroll + unified search probe
- **Hermetic shell probe** (`tests/operational/youtube_e2e_unified_search_smoke.sh`, ~100 LoC)
- **Action**: 2-step: (a) `POST /collections/media_assets_current/points/scroll` with `video_id` filter from the canonical test video → verify ≥ 1 point present with `lifecycle_state='ACTIVE'`; (b) `POST /api/media/search` with `query='<test video topic>'`, `sources=['youtube']`, `mode='hybrid'`, `limit=10` → verify ≥ 1 hit with `source=youtube + score>0 + downloadable id`.
- **Exit codes**: 0=PASS, 1=FAIL (404/422/502/503/500/200-empty), 2=prereq missing.
- **Canonical FAIL→PR**: `PR-YT-DOD-9-QDRANT-EMPTY` (200-empty) or `PR-YT-DOD-9-SEARCH-SOURCE-FILTER` (no source=youtube) or `PR-YT-DOD-9-SEARCH-SCORE-OWNERSHIP` (no scored results).
- **Owner**: `internal/infrastructure/qdrant/indexing/index_writer.go` (Qdrant) + `internal/application/search/handler.go` (unified search).

### YT-E2E-G — Hermes-only per-clip TDD wire (NO shell smoke)
- **Hermetic Go test** (`tests/e2e/youtube_clip_dod_e2e_test.go`, ALREADY on `origin/main` at commit `93c196cfe` 2026-07-04)
- **No new operator-facing surface** — the TDD probe exists and asserts the 4 per-clip × 3-clip = 12 invariants in-process. Battery point tally: 1 (aggregator references it but does not invoke directly).
- **Canonical owner**: `tests/e2e/youtube_clip_dod_e2e_test.go` (canonical hermetic TDD probe per AGENTS.md Pattern 6 diagnostic-first).

### YT-E2E-H — Aggregator battery
- **Hermetic shell smoke** (`tests/operational/youtube_e2e_full_battery.sh`, ~150 LoC)
- **Action**: runs A→G sequentially + asserts the 4-point must-pass checklist (per-probe point tally: A=1 + B=1 + C=1 + D=1 + E=1 + F=2 + G=1 = 8 points).
- **Exit codes**: 0=PASS (8/8 green), 1=FAIL, 2=prereq missing.
- **Wave-flip**: on 8/8 PASS + `WRAPPER_BOOKKEEPING=1` env var, flips `architecture/current.yaml#YOUTUBE-CLIP-DOD-2026-07-09` to `status: shipped + exit_signal: true` via the canonical 6-step recipe (mirrors STK-E2E-H pattern at commit `4c2e3d13`).

---

## §4 — Per-probe commit pattern (godlike/07 minimum-blast-radius)

Each per-probe shell smoke lands incrementally on `main` per AGENTS.md Git-Lesson-2 (one atomic commit per probe; NO branches, NO PR, NO `--force`; race-protect via `git fetch && git log --oneline HEAD..@{u}` returning empty for safe ff-push; byte-equivalent-replay-race acceptance per AGENTS.md Git-Lesson-5):

```bash
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    add tests/operational/<probe>.sh
git commit -m 'test(yt-e2e): YT-E2E-<X> closure — <canonical 4-surface>

<per-probe description>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'
git fetch origin         # race-protection (AGENTS.md Git-Lesson-4/5)
git push origin main     # direct-to-main; no branch
```

---

## §5 — Verification gates (godlike/07 minimum-blast-radius)

Per probe before commit:
- `bash -n tests/operational/<probe>.sh` clean (no syntax errors).
- `jq` + `curl` + `sqlite3` + `ffmpeg`/`ffprobe` are pre-existing on operator hosts (mirrors STK-E2E prerequisite gate).
- `chmod 755` applied for operator-executable surface.
- 4-surface godlike/06 SSOT lockstep entry appended to `AGENTS.md` `## Recent cross-cutting closures` per codebase convention.
- 1-surface CHANGELOG.md closure meta-entry appended under `## Unreleased > ### Added`.

Per battery aggregator before commit:
- `bash -n tests/operational/youtube_e2e_full_battery.sh` clean.
- `chmod 755` applied.
- 6-step recipe embedded as a comment block (yaml pre-flight + block-aware Python surgery + `commit -F` + race-protect + push).
- The aggregator's tally matches the per-probe sum exactly (8 points: 1+1+1+1+1+2+1).

---

## §6 — Honest scope-lock (godlike/07 NO-FAKE-AVAILABILITY)

This action plan does NOT:
- Modify production code (zero Go file touched; zero SQLite migration; zero composition-root wiring change).
- Touch the existing in-process TDD surface (`tests/e2e/youtube_clip_dod_e2e_test.go`).
- Add new Python or Go dependencies.
- Replace the canonical `internal/application/youtube/usecase/extract_important_clips.go` path with a new extraction path.

The wave-flip criterion is satisfied ONLY when all 4 mandatory gates pass via the YT-E2E-H aggregator against a live PipelineGen server (port 8000 OR 8081, `VELOX_ADMIN_TOKEN` set, DB writable, yt-dlp + ffmpeg + ffprobe on PATH). A probe FAIL with one of the canonical signatures is the PR-YT-DOD-* forward-pointer that MUST ship BEFORE the wave flips. The 2-doc surfaces (action plan + CHANGELOG + AGENTS) are the canonical SOLE closure record until the wave flips; the wave-tracker slot is **DEFERRED** per `architecture/current.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer.

---

## §7 — Pre-existing carry-forward preserved (NOT regressions)

- 6-item voiceover + app build-issue carry-forward per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` UNCHANGED — NOT a regression of this action plan.
- Pre-existing 4-band wave files (wave_p0/wave_p1/wave_p2/wave_p3) parse state per the recent PART-N forward-pointer chain — UNCHANGED.
- Pre-existing `yt_extraction_verify.sh` (single-clip integration probe) UNCHANGED — the battery is an ADDITIVE surface, not a replacement.
- Pre-existing `tests/e2e/youtube_clip_dod_e2e_test.go` (in-process TDD) UNCHANGED — the battery is an ADDITIVE operator-facing surface, not a replacement.
- 9 NEW hermetic TDD tests in `internal/application/scripts/usecase/translation_test.go` (PR-1 of SCRIPT-TRANSLATION-TESTING-2026-07-08) UNCHANGED — the battery does NOT cover translation; translation is forward-pointer `PR-YT-DOD-13-TRANSLATION-E2E` (out of scope).

---

## §8 — Wave-flip criterion (godlike/07 NO-FAKE-AVAILABILITY)

`architecture/current.yaml#YOUTUBE-CLIP-DOD-2026-07-09` flips `status: pending → status: shipped + exit_signal: true` ONLY WHEN:
- (a) All 4 mandatory gates (D + E + F's 2 sub-gates) pass via YT-E2E-H aggregator.
- (b) `cmd/admin` or operator-driven replay on a live PipelineGen server reports 8/8 points.
- (c) `architecture/current.yaml#PR-YT-DOD-HOTSPOT-CROSSREF` (deadline 2026-08-15) cross-validates the priority statica via `git log --since=90.days --pretty=format: --name-only | sort | uniq -c | sort -rn | head -30` (mirrors the `PR-GODOBJ-HOTSPOT-CROSSREF` + `PR-P12-HOTSPOT-CROSSREF` + `PR-CLEANUP-HOTSPOT-CROSSREF` pattern).

Per the ratchet-style slim-schema append-only ratchet (godlike/06 SSOT): ZERO new `linked_issues` are appended unless the cross-validation surfaces actual frequency findings.

---

## §9 — Lifecycle audit-trail

- 2026-07-09: this action plan lands (3-surface godlike/06 SSOT lockstep: action plan + CHANGELOG + AGENTS).
- 2026-07-09 → 2026-08-08: per-probe shell smokes land incrementally on `main` (one atomic commit per probe; race-protect via `git fetch && git log --oneline HEAD..@{u}`).
- 2026-08-08: wave-flip deadline (if 8/8 PASS); otherwise forward-pointer to next attempt.
- 2026-08-15: `PR-YT-DOD-HOTSPOT-CROSSREF` deadline (post-wave git-log frequency cross-validation).

---

## §10 — Co-authored-by

This action plan ships with `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` per AGENTS.md Git-Lesson-3 (canonical agent identity trailer). Direct-to-main per AGENTS.md Git-Lesson-2 (no branches, no `--no-ff`, no `--force`). Race-protect per AGENTS.md Git-Lesson-4 (`git fetch origin && git log --oneline HEAD..@{u}` must return empty for safe ff-push). Byte-equivalent-replay-race acceptance per AGENTS.md Git-Lesson-5 (if a parallel agent lands a byte-equivalent commit on `origin/main` during the commit-to-push window, accept without force-push per the canonical discipline).

---

## §11 — Cross-references (godlike/06 SSOT umbrella)

- **STK-E2E-BATTERY-2026-07-05** (precedent): `architecture/action-plans/2026-07-05-stock-e2e-battery.md` + `tests/operational/stock_e2e_full_battery.sh` (canonical battery pattern + verdict block + 6-step recipe).
- **PR-YT-DOD-7-METADATA-JSON-AUDIT** (prior DoD closure): `architecture/waves/wave_p1_high.yaml#PR-YT-DOD-7-METADATA-JSON-AUDIT` (shipped 2026-07-08, ship_sha `ab09488d4`).
- **PR-YT-DOD-10-SEARCH-TEXT-CONTRACT-TDD** (prior DoD closure): `internal/application/youtube/usecase/process_segment_test.go` (shipped 2026-07-08, ship_sha `d21b725cb` + round-2 fixup `653c9c5a3`).
- **PR-YT-DOD-12-E2E-FULL** (prior DoD closure): `tests/e2e/youtube_clip_dod_e2e_test.go` (shipped 2026-07-08, ship_sha `93c196cfe`).
- **PR-YT-DOD-HOTSPOT-CROSSREF** (forward-pointer): deadline 2026-08-15 (post-wave git-log frequency cross-validation per slim-schema ratchet).
- **GODOBJ-2026-07-03** (sister wave): `architecture/waves/wave_p1_high.yaml#GODOBJ-2026-07-03` (12-file god-object decomposition; canonical 4-band priority pattern).
- **QDRANT-CHAIN-VERIFY-2026-07-04** (sister wave): `architecture/waves/wave_p1_high.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` (Qdrant 4-step projection sequence + outbox v1 contract).
- **STK-E2E-BATTERY-2026-07-05** (sister wave): `architecture/waves/wave_p1_high.yaml#STOCK-E2E-BATTERY-2026-07-05` (8 hermetic shell smokes; canonical 6-step recipe precedent).
- **PRE-EXISTING-BUILD-ISSUES-2026-07-04** (carry-forward): `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (6-item voiceover + app build-issue list; UNCHANGED).
- **PRE-EXISTING-YAML-PARSE-2026-07-04** (carry-forward): `architecture/waves/wave_p0_yaml_parse_carryforward.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` (wave-tracker slot flip DEFERRED per the YAML parse carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer, deadline 2026-08-15).
- **AGENTS.md** (canonical mirror surface): §"Recent cross-cutting closures" entry per AGENTS.md Git-Lesson-3 lockstep.
- **CHANGELOG.md** (canonical closure meta-entry): `## Unreleased > ### Added` per CANONICAL.md §1 3-surface godlike/06 SSOT lockstep.

---

## §12 — Forward-pointers (godlike/07 honest scope-lock)

1. `PR-YT-DOD-1` (deadline 2026-07-15): route-aliveness gate fix (404) — composition root wiring if `internal/app/wire_script.go::registerScriptPostProcessors` lacks the YouTube processor registration.
2. `PR-YT-DOD-2` (deadline 2026-07-15): composition-wire gate fix (503) — `internal/app/wire_script_postprocess.go` must inject YouTube deps at composition time.
3. `PR-YT-DOD-3` (deadline 2026-07-22): BindJSON bypass fix (200) — silent-success godlike/07 violation if `internal/api/script/handler_flow.go` accepts empty payload without 400.
4. `PR-YT-DOD-4` (deadline 2026-07-22): auth-check fix (401/403) — `internal/api/script/handler_deps.go` must validate `VELOX_ADMIN_TOKEN` env presence.
5. `PR-YT-DOD-5` (deadline 2026-08-01): search-and-run flow fix (multi-PR mapping per the existing 4-surface diagnostic decision tree).
6. `PR-YT-DOD-6` (deadline 2026-08-01): composition-wired extraction flow fix (canonical owner `internal/app/wire_script.go`).
7. `PR-YT-DOD-7-METADATA-JSON-AUDIT` (shipped 2026-07-08, ship_sha `ab09488d4`): already-closed; reference-only.
8. `PR-YT-DOD-8-OUTBOX-{LAST-ERROR,RETRY-EXHAUSTED,DEAD-LETTERED}` (3 sub-PRs, deadline 2026-08-01): outbox write-seam fix at `internal/infrastructure/database/sqlite/outboxevents/repository.go`.
9. `PR-YT-DOD-9` (deadline 2026-08-01): Qdrant projection + unified search fix (canonical owner `internal/infrastructure/qdrant/indexing/index_writer.go` + `internal/application/search/handler.go`).
10. `PR-YT-DOD-13-TRANSLATION-E2E` (deadline 2026-08-22): optional translation E2E smoke (out of scope; the SCRIPT-TRANSLATION-TESTING-2026-07-08 in-process TDD already covers the translation surface).
11. `PR-YT-DOD-HOTSPOT-CROSSREF` (deadline 2026-08-15): post-wave git-log frequency cross-validation per the `PR-GODOBJ-HOTSPOT-CROSSREF` precedent.
12. `PR-YT-E2E-FORWARD-POINTER` (deadline TBD): aggregated forward-pointer entry for the 11 PR-YT-DOD-* sub-PRs.

---

## §13 — Co-authored-by (canonical)

`Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` per AGENTS.md Git-Lesson-3.

---

## §14 — Direct-to-main (canonical)

Per AGENTS.md Git-Lesson-2: no branches, no `--no-ff`, no `--force`. Per AGENTS.md Git-Lesson-4: race-protect via `git fetch origin && git log --oneline HEAD..@{u}` returning empty for safe ff-push. Per AGENTS.md Git-Lesson-5: byte-equivalent-replay-race acceptance if a parallel agent lands a byte-equivalent commit on `origin/main` during the commit-to-push window.

---

## §15 — Verification of this action plan

After this 3-surface lockstep commit, the next session can:
- Read `architecture/action-plans/2026-07-09-youtube-clip-pipeline-e2e-action-plan.md` (this file) to see the canonical 4-must-pass chain + per-probe topology.
- Read `CHANGELOG.md` to see the closure meta-entry.
- Read `AGENTS.md` to see the mirror entry + cross-references.
- Verify the 6-step recipe + 8-point tally + per-probe failure mappings.
- Ship YT-E2E-A first (route aliveness, the cheapest gate) → YT-E2E-D (the cheapest DB probe) → YT-E2E-E → YT-E2E-F (the most expensive) → YT-E2E-B → YT-E2E-C (the async jobs) → YT-E2E-G (already shipped) → YT-E2E-H aggregator last.

Wave-flip on 8/8 PASS + 4 mandatory gates green + hotspot-crossref-deadline-met (per godlike/07 slim-schema ratchet).

---

*End of action plan.*
