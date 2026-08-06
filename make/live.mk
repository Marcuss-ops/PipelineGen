# make/live.mk - thematic include (P2 Manutenibilita, July 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive,
# the canonical build chain is split into 7 thematic includes.
# This file holds only the live-bucket targets. Cross-bucket
# dependencies (e.g. verify-artlist-live -> auth-check) resolve
# naturally via Make's recursive target resolution.
# Root Makefile contains include make/*.mk plus all: build.

# ─── Post-deploy live batteries (STEP 4/4, July 2026) ─────────────────
#
# verify-live — top-level post-deploy gate. Composes the live batteries
# (images + artlist + script + vidrush + stock) so a single `make verify-live`
# runs the full operational suite. NOT part of verify-main or
# verify-release: these batteries all require Chrome + scraper + Drive
# + Qdrant and must never be wired into the pre-push chain.
#
# Individual live targets are also exposed so a post-deploy operator
# can validate ONE surface without paying the full battery cost
# (e.g. `make verify-images-live` after a Drive-side change).
#
# verify-images-live — tests/operational/test2_images.sh — image
# ingestion + Drive upload + Qdrant projection for the image surface.
verify-images-live: auth-check
	@scripts/with-velox-auth bash tests/operational/test2_images.sh

# verify-script-live — tests/operational/generate/run.sh basic.json —
# end-to-end script.generate dispatch + worker pull + finalizer,
# without the full Vid Rush media path.
verify-script-live: auth-check
	@scripts/with-velox-auth bash tests/operational/generate/run.sh basic.json

# test-intro-hook-stock-live — the intro-hook stock binding diagnostic battery
# (boxers-generate). Runs the practical suite (intro_hook_suite.py): Tests
# 1..9 prove the intro-hook rides the canonical stock_bindings contract as a
# normal narrative segment — scene-0, kind intro, stock + timeline clip
# coexistence (intro and post_segment), source_text, translation + voiceover,
# and the full 6-scene/6-stock scenario; N1..N8 assert validator rejections;
# the final scenario is saved to FULL and gate-checked. Render is never
# invoked.
test-intro-hook-stock-live: auth-check
	@scripts/with-velox-auth bash tests/operational/boxers-generate/run_intro_hook_stock.sh

# verify-vidrush-live — tests/operational/vidrush_script_generate_e2e.sh —
# the canonical VidRush battery. It uses one POST /api/script/generate per
# case, polls the canonical job result, and validates per-segment extraction,
# provider policy, replay cache, and SQLite provenance. The older provider
# route battery remains available as a separate operator script.
verify-vidrush-live: auth-check
	@scripts/with-velox-auth bash tests/operational/vidrush_script_generate_e2e.sh

# verify-artlist-scale-live — quota-expensive 20x10 Artlist/VidRush battery.
# It validates continuous API health, Drive persistence, VLM/Qdrant payloads,
# throughput and replay no-redownload. Kept OUT of verify-live deliberately:
# the default matrix may consume up to 200 authorized Artlist downloads.
verify-artlist-scale-live: auth-check
	@scripts/with-velox-auth bash tests/operational/artlist_scale_e2e.sh

# verify-live — composite: all 5 live batteries in sequence. Fail-closed:
# any single battery failure aborts the chain.
#
# verify-artlist-live is ALSO runnable standalone (it wraps
# tests/operational/artlist/run_all.sh) for an artlist-only post-deploy
# validation that does not pay the full battery cost. verify-live itself
# composes it alongside the 4 sibling batteries (images + script +
# vidrush + stock) so a single `make verify-live` runs the full operational
# suite, including the stock 14/14 battery.
verify-live: auth-check verify-images-live verify-artlist-live verify-script-live verify-vidrush-live verify-stock-live
	@echo "✅ verify-live passed"
# ─── end Post-deploy live batteries ─────────────────────────────────────

# verify-images — quick verification dedicated to the Images module.
