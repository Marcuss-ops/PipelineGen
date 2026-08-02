YOUTUBE_STOCK_PACKAGE := ./internal/application/assets/providers/stock/stockplan
YOUTUBE_STOCK_TEST := $(GO) test -count=1 $(YOUTUBE_STOCK_PACKAGE)
verify-youtube-url:
	$(YOUTUBE_STOCK_TEST) -run TestYouTubeAcquisitionContracts
verify-youtube-metadata:
	$(GO) test -count=1 ./internal/infrastructure/youtube -run 'TestGetVideoMetadata|TestYouTubeMetadata'
verify-youtube-transcript:
	$(GO) test -count=1 ./internal/infrastructure/youtube -run 'Test.*Subtitle|Test.*Whisper'
verify-highlight-selection verify-stock-download verify-stock-cache:
	$(YOUTUBE_STOCK_TEST) -run TestYouTubeAcquisitionContracts
verify-youtube-highlights:
	$(YOUTUBE_STOCK_TEST) -run 'TestYouTubeAcquisitionContracts|TestYouTubeStockJSONRoundTrip'
verify-stock-download-plan:
	$(YOUTUBE_STOCK_TEST) -run TestYouTubeAcquisitionContracts
verify-stock-partial-download:
	$(GO) test -count=1 ./internal/infrastructure/downloader -run 'TestDownload'
verify-stock-drive:
	$(GO) test -count=1 ./internal/application/assets/providers/stock/stockpipeline -run 'Test.*Upload'
verify-stock-concurrency:
	$(GO) test -count=1 ./internal/application/assets/providers/stock/stockpipeline -run 'Test.*Concurrent|Test.*Interleave'
verify-race-youtube-stock:
	$(GO) test -race -count=1 $(YOUTUBE_STOCK_PACKAGE)
verify-stock-cut:
	$(GO) test -count=1 ./internal/infrastructure/downloader -run TestDownload
verify-stock-dedupe verify-stock-index verify-stock-recovery:
	$(GO) test -count=1 ./internal/application/assets/providers/stock/stockpipeline
verify-stock-youtube-e2e:
	$(YOUTUBE_STOCK_TEST)
benchmark-stock-download:
	$(GO) test -run '^$$' -bench 'Benchmark.*' -benchmem ./internal/application/assets/providers/stock/stockpipeline
benchmark-youtube-stock: benchmark-stock-download
verify-youtube-stock-fast: verify-youtube-url verify-youtube-metadata verify-youtube-transcript verify-youtube-highlights verify-stock-download-plan
verify-youtube-stock-local: verify-youtube-stock-fast verify-stock-partial-download verify-stock-cache verify-stock-dedupe verify-stock-index
verify-youtube-stock-resilience: verify-stock-recovery verify-stock-concurrency
verify-youtube-stock-live:
	@scripts/with-velox-auth bash tests/operational/youtube_stock_live_e2e.sh
verify-youtube-stock-release: verify-youtube-stock-local verify-youtube-stock-resilience verify-youtube-stock-live
doctor-youtube-stock:
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
verify-stock-acquisition:
	$(MAKE) verify-youtube-url verify-youtube-metadata verify-youtube-transcript verify-highlight-selection verify-stock-download verify-stock-cache
verify-stock-indexing:
	$(MAKE) verify-stock-index verify-stock-dedupe verify-stock-recovery
