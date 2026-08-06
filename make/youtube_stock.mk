YOUTUBE_STOCK_PACKAGE := ./internal/application/assets/providers/stock/stockplan
YOUTUBE_STOCK_TEST := $(GO) test -count=1 $(YOUTUBE_STOCK_PACKAGE)

test-youtube-url:
	$(YOUTUBE_STOCK_TEST) -run TestYouTubeAcquisitionContracts

test-youtube-metadata:
	$(GO) test -count=1 ./internal/infrastructure/youtube -run 'TestGetVideoMetadata|TestYouTubeMetadata'

test-youtube-transcript:
	$(GO) test -count=1 ./internal/infrastructure/youtube -run 'Test.*Subtitle|Test.*Whisper'

test-highlight-selection test-stock-download test-stock-cache:
	$(YOUTUBE_STOCK_TEST) -run TestYouTubeAcquisitionContracts

test-youtube-highlights:
	$(YOUTUBE_STOCK_TEST) -run 'TestYouTubeAcquisitionContracts|TestYouTubeStockJSONRoundTrip'

test-stock-download-plan:
	$(YOUTUBE_STOCK_TEST) -run TestYouTubeAcquisitionContracts

test-stock-partial-download:
	$(GO) test -count=1 ./internal/infrastructure/downloader -run 'TestDownload'

test-stock-drive:
	$(GO) test -count=1 ./internal/application/assets/providers/stock/stockpipeline -run 'Test.*Upload'

test-stock-concurrency:
	$(GO) test -count=1 ./internal/application/assets/providers/stock/stockpipeline -run 'Test.*Concurrent|Test.*Interleave'

test-race-youtube-stock:
	$(GO) test -race -count=1 $(YOUTUBE_STOCK_PACKAGE)

test-stock-cut:
	$(GO) test -count=1 ./internal/infrastructure/downloader -run TestDownload

test-stock-dedupe test-stock-index test-stock-recovery:
	$(GO) test -count=1 ./internal/application/assets/providers/stock/stockpipeline

test-stock-youtube-e2e:
	$(YOUTUBE_STOCK_TEST)

benchmark-stock-download:
	$(GO) test -run '^$$' -bench 'Benchmark.*' -benchmem ./internal/application/assets/providers/stock/stockpipeline

benchmark-youtube-stock: benchmark-stock-download

test-youtube-stock-fast: test-youtube-url test-youtube-metadata test-youtube-transcript test-youtube-highlights test-stock-download-plan

test-youtube-stock-local: test-youtube-stock-fast test-stock-partial-download test-stock-cache test-stock-dedupe test-stock-index

test-youtube-stock-resilience: test-stock-recovery test-stock-concurrency

test-youtube-stock-live:
	@scripts/with-velox-auth bash tests/operational/youtube_stock_live_e2e.sh

test-youtube-stock-release: test-youtube-stock-local test-youtube-stock-resilience test-youtube-stock-live

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

# This is the only stock live gate. It uses the canonical authenticated
# operational battery rather than a local fixture or a package-only alias.
# Keep the receipt in a caller-selectable location so release can validate the
# exact output from this one live run without executing the battery twice.
STOCK_E2E_RECEIPT ?= $(if $(TMPDIR),$(TMPDIR),/tmp)/pipelinegen-stock-e2e-receipt.$(shell date +%s%N).log

verify-stock-live: auth-check
	@rm -f "$(STOCK_E2E_RECEIPT)"
	@bash -o pipefail -c 'scripts/with-velox-auth bash tests/operational/stock_e2e_full_battery.sh 2>&1 | tee "$$1"' -- "$(STOCK_E2E_RECEIPT)"
	@echo "✅ verify-stock-live passed (receipt: $(STOCK_E2E_RECEIPT))"

verify-stock-release: verify-stock-unit verify-stock-integration verify-stock-live
	@bash scripts/ci/verify-stock-receipt.sh "$(STOCK_E2E_RECEIPT)"
	@echo "✅ verify-stock-release passed (canonical 14/14 receipt: $(STOCK_E2E_RECEIPT))"
