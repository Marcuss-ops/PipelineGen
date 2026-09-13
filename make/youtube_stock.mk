YOUTUBE_STOCK_PACKAGE := ./internal/capabilities/assets/providers/stock/stockplan
YOUTUBE_STOCK_TEST := $(GO) test -count=1 $(YOUTUBE_STOCK_PACKAGE)

test-youtube-url:
	$(YOUTUBE_STOCK_TEST) -run TestYouTubeAcquisitionContracts

test-youtube-metadata:
	$(GO) test -count=1 ./internal/platform/youtube -run 'TestGetVideoMetadata|TestYouTubeMetadata'

test-youtube-transcript:
	$(GO) test -count=1 ./internal/platform/youtube -run 'Test.*Subtitle|Test.*Whisper'

test-highlight-selection test-stock-download test-stock-cache:
	$(YOUTUBE_STOCK_TEST) -run TestYouTubeAcquisitionContracts

test-youtube-highlights:
	$(YOUTUBE_STOCK_TEST) -run 'TestYouTubeAcquisitionContracts|TestYouTubeStockJSONRoundTrip'

test-stock-download-plan:
	$(YOUTUBE_STOCK_TEST) -run TestYouTubeAcquisitionContracts

test-stock-partial-download:
	$(GO) test -count=1 ./internal/platform/downloader -run 'TestDownload'

test-stock-drive:
	$(GO) test -count=1 ./internal/capabilities/assets/providers/stock/stockpipeline -run 'Test.*Upload'

test-stock-concurrency:
	$(GO) test -count=1 ./internal/capabilities/assets/providers/stock/stockpipeline -run 'Test.*Concurrent|Test.*Interleave'

test-race-youtube-stock:
	$(GO) test -race -count=1 $(YOUTUBE_STOCK_PACKAGE)

test-stock-cut:
	$(GO) test -count=1 ./internal/platform/downloader -run TestDownload

test-stock-dedupe test-stock-index test-stock-recovery:
	$(GO) test -count=1 ./internal/capabilities/assets/providers/stock/stockpipeline

test-stock-youtube-e2e:
	$(YOUTUBE_STOCK_TEST)

benchmark-stock-download:
	$(GO) test -run '^$$' -bench 'Benchmark.*' -benchmem ./internal/capabilities/assets/providers/stock/stockpipeline

benchmark-youtube-stock: benchmark-stock-download

test-youtube-stock-fast: test-youtube-url test-youtube-metadata test-youtube-transcript test-youtube-highlights test-stock-download-plan

test-youtube-stock-local: test-youtube-stock-fast test-stock-partial-download test-stock-cache test-stock-dedupe test-stock-index

test-youtube-stock-resilience: test-stock-recovery test-stock-concurrency

# test-youtube-stock-live — RETIRED 2026-09-13: its driver
# tests/operational/youtube_stock_live_e2e.sh was deleted by commit 7e6965aab
# ("purge 94% shell + 87% python dust"). A target whose only possible outcome is
# "No such file or directory" is not a gate (see make/verify.mk for the
# certification-driver rationale). The L1 → L3 StockRust boundary is currently
# certified by the Go surfaces only: internal/platform/media/rustexec
# (L2 adapter → Rust) and the Rust crate tests (L3 binary).

# test-youtube-stock-release — Go-only release gate. The live leg was pruned on
# 2026-09-13 with the retirement of test-youtube-stock-live.
test-youtube-stock-release: test-youtube-stock-local test-youtube-stock-resilience

diagnose-youtube-stock:
	@set -eu; \
	for tool in yt-dlp ffmpeg ffprobe sqlite3 curl jq; do \
		command -v "$$tool" >/dev/null 2>&1 || { echo "FAIL: $$tool missing"; exit 2; }; \
	done; \
	echo "yt-dlp=$$(yt-dlp --version)"; \
	ffmpeg -version | head -1; \
	ffprobe -version | head -1; \
	df -h .; \
	: "$${YOUTUBE_CANARY_CAPTIONS_URL:?YOUTUBE_CANARY_CAPTIONS_URL is required}"; \
	timeout 60 yt-dlp --skip-download --dump-single-json --no-playlist "$$YOUTUBE_CANARY_CAPTIONS_URL" > "$${TMPDIR:-/tmp}/youtube-canary-metadata.json"; \
	jq -e '(.id|type=="string" and length>0) and (.duration|numbers and .>0) and ((.live_status // "unknown") != "is_live")' "$${TMPDIR:-/tmp}/youtube-canary-metadata.json" >/dev/null || { echo "FAIL: canary metadata is not a playable non-live video"; exit 1; }; \
	jq '{id,title,duration,availability,live_status,subtitles:(.subtitles|keys),automatic_captions:(.automatic_captions|keys)}' "$${TMPDIR:-/tmp}/youtube-canary-metadata.json"; \
	echo '{"final":"PASS"}'

test-stock-acquisition:
	$(MAKE) test-youtube-url test-youtube-metadata test-youtube-transcript test-highlight-selection test-stock-download test-stock-cache

test-stock-indexing:
	$(MAKE) test-stock-index test-stock-dedupe test-stock-recovery

# The four canonical stock verification levels. Granular checks above are
# diagnostic/test helpers and are intentionally not release-authoritative.
verify-stock-unit: test-stock-component test-youtube-stock-fast
	@echo "✅ verify-stock-unit passed"

verify-stock-integration: test-youtube-stock-local test-youtube-stock-resilience
	@echo "✅ verify-stock-integration passed"

# ─── Stock live / release batteries: RETIRED 2026-09-13 ───────────────
#
# verify-stock-live drove tests/operational/stock_e2e_full_battery.sh (and the
# 7 stock_e2e_*_smoke.sh probes it aggregated), all deleted by commit
# 7e6965aab ("purge 94% shell + 87% python dust"). verify-stock-release also
# depended on scripts/ci/verify-stock-{receipt,claim}.sh, deleted by the same
# commit, so the receipt chain it certified no longer exists in any form.
#
# The STOCK_E2E_RUN_ID / STOCK_E2E_RECEIPT / STOCK_E2E_RECEIPT_KEY_FILE
# variables and the openssl key-file dance were removed with them: they only
# existed to feed the receipt verifier. A target whose only possible outcome is
# "No such file or directory" is not a gate (see make/verify.mk).
#
# Stock live coverage is currently owned by the Go surfaces:
#   go test ./internal/capabilities/assets/providers/stock/...   (unit/contract)
#   go test ./internal/platform/media/rustexec/...         (L2 adapter → Rust)
# Reintroduce verify-stock-live only with a tracked driver (Go preferred).
