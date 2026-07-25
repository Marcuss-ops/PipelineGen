# make/operations.smoke-voiceover.mk - thematic include (P2 Manutenibilita, July 2026).
# Sub-bucket of the former make/operations.mk (414 lines → 4 sub-files).
# Holds the voiceover E2E smoke target (separated because it requires a
# 5min wall-clock budget and is typically skipped in the fast smoke loop).
# Root Makefile contains include make/*.mk plus all: build.

# smoke-voiceover — FASE 7 E2E smoke for the voiceover pipeline.
# Usage:
#   make smoke-voiceover                              # use default env
#   VELOX_ADMIN_TOKEN=<t> make smoke-voiceover         # explicit token
#   VELOX_ADMIN_TOKEN=<t> SMOKE_DB=<path> make smoke-voiceover  # custom DB
#
# Runs the Go E2E smoke test at tests/operational/voiceover_e2e_smoke_test.go.
# The test skips when VELOX_ADMIN_TOKEN is unset or -short is active.
# Wall-clock budget: 5min (3min job poll + TTS/Drive latency).
smoke-voiceover:
	@echo "→ Running voiceover E2E smoke test..."
	@if [ -z "$$VELOX_ADMIN_TOKEN" ]; then \
		echo "❌ VELOX_ADMIN_TOKEN not set — the voiceover E2E smoke needs auth. Set VELOX_ADMIN_TOKEN and retry."; \
		exit 1; \
	fi
	@go test -v -count=1 -timeout 5m -run TestVoiceoverE2ESmoke ./tests/operational/...
	@echo "✅ smoke-voiceover OK"
