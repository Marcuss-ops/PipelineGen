# make/artlist.mk - thematic include (P2 Manutenibilita, July 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive,
# the canonical build chain is split into 7 thematic includes.
# This file holds only the artlist-bucket targets. Cross-bucket
# dependencies (e.g. verify-artlist-live -> auth-check) resolve
# naturally via Make's recursive target resolution.
# Root Makefile contains include make/*.mk plus all: build.

verify-artlist:
	$(GO) test -race ./internal/infrastructure/artlist/... && \
	$(GO) test -race ./internal/application/assets/providers/artlist/... && \
	$(GO) test -race ./internal/api/assets/artlist/... && \
	cd node-scraper && npm test
	@echo "✅ Artlist verification passed"

# ─── Artlist operational batteries (granular, July 2026) ────────────────
#
# The Artlist battery is split into
# 9 granular sub-scripts under tests/operational/artlist/, each gated
# by its own Make target so a developer debugging one phase can iterate
# in seconds instead of waiting for the full battery. The 9 sub-targets
# are NOT part of verify-main (the pre-push headless gate) — they all
# require the live stack (Chrome + scraper + Drive + Qdrant).
#
# Library surface (single canonical owner per fact): every sub-script
# imports helpers from tests/operational/lib/ — no curl/jq/SQLite/ffprobe
# is duplicated across the 9 files. See tests/operational/lib/{common,
# artlist, drive, qdrant, sqlite}.sh for the canonical surface.
#
# Debug pattern:
#   make verify-artlist-stream   # while iterating on /detail streaming
#   make verify-artlist-download # while iterating on /download + ffprobe
#   make verify-artlist-live     # only after all 9 green
#
# No go-version-check prereq (the sub-scripts are bash + node-scraper,
# Go-unaware). No node-version-check prereq either: each sub-script
# does its own runtime assertion at the top (the lib helpers fail
# closed when node-scraper is unreachable).

# verify-artlist-startup — Phase 1: server / scraper readiness + admin
# auth probe. Catches cold-start failures (port in use, scraper not
# running, token mismatch) before any downstream phase touches an
# endpoint that depends on those surfaces.
verify-artlist-startup:
	@bash tests/operational/artlist/01_startup.sh

# verify-artlist-search — Phase 2: POST /api/artlist/search/live.
# Confirms the live search endpoint resolves terms via the scraper and
# persists discovered candidates. Cheap gate (~30s) once startup is green.
verify-artlist-search:
	@bash tests/operational/artlist/02_search_live.sh

# verify-artlist-stream — Phase 3: POST /api/artlist/detail + ffprobe
# hard gate. Confirms the canonical stream probe rejects silent clips
# (audio_probe MUST surface HasAudio=false) and accepts clips with an
# audio track. The most common debug target when stream resolution
# regresses.
verify-artlist-stream:
	@bash tests/operational/artlist/03_detail_stream.sh

# verify-artlist-download — Phase 4: POST /api/artlist/download +
# ffprobe hard gate. Confirms the download endpoint returns a file
# whose ffprobe metadata matches the canonical expectations (codec,
# duration, container). Debug target for download-pipeline regressions.
verify-artlist-download:
	@bash tests/operational/artlist/04_download.sh

# verify-artlist-pipeline — Phase 5: end-to-end pipeline on a FRESH
# fixture (no cache replay). Drives search → detail → download →
# drive upload → index, asserting each stage's canonical surface.
verify-artlist-pipeline:
	@bash tests/operational/artlist/05_pipeline_fresh.sh

# verify-artlist-drive — Phase 6: Drive upload + folder routing.
# Confirms the canonical Publisher routes Artlist clips through the
# shared Drive surface and that the destination folder resolver picks
# the right root for the search term.
verify-artlist-drive:
	@bash tests/operational/artlist/06_drive.sh

# verify-artlist-index — Phase 7: Qdrant projection. Confirms the clip
# appears in the v3 collection with the expected payload (source_url,
# text_hash, source_version). Fails fast when the SQLite→Qdrant
# rebuild contract is broken.
verify-artlist-index:
	@bash tests/operational/artlist/07_index.sh

# verify-artlist-cache — Phase 8: cache-replay path. Re-runs the
# pipeline on a cached fixture and asserts the cache-hit surface (no
# re-download, no re-transcription, but full Drive + Qdrant surface
# still validated).
verify-artlist-cache:
	@bash tests/operational/artlist/08_cache_replay.sh

# verify-artlist-errors — Phase 9: failure-mode catalogue. Exercises
# the typed error surface: STREAM_NOT_FOUND, missing Drive fields,
# transcription failure, transcript persist failure, audio-probe
# miss. Asserts each error path surfaces the canonical typed sentinel
# rather than a generic no-op success.
verify-artlist-errors:
	@bash tests/operational/artlist/09_failure_modes.sh

# verify-artlist-live — composite: runs ALL 9 granular sub-scripts in
# order via tests/operational/artlist/run_all.sh. Run AFTER all 9
# individual gates are green; this is the post-deploy / pre-certification
# battery. NOT part of verify-main (it requires the live stack).
verify-artlist-live: auth-check
	@scripts/with-velox-auth bash tests/operational/artlist/run_all.sh
