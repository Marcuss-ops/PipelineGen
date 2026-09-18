# make/operations.auth.mk - auth-related operational targets.
# Root Makefile contains include make/*.mk plus all: build.

doctor:
	@[ -n "$$VELOX_ADMIN_TOKEN" ] || { echo "❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth or export manually."; exit 1; }
	@curl -sS -f -H "Authorization: Bearer $(VELOX_ADMIN_TOKEN)" http://127.0.0.1:$${VELOX_PORT:-8000}/api/system/doctor | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

TERM ?= technology
artlist:
	@[ -n "$$VELOX_ADMIN_TOKEN" ] || { echo "❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth or export manually."; exit 1; }
	@curl -sS -f -X POST http://127.0.0.1:$${VELOX_PORT:-8000}/api/artlist/run \
		-H "Content-Type: application/json" \
		-H "Authorization: Bearer $(VELOX_ADMIN_TOKEN)" \
		-d '{"term":"$(TERM)","limit":$(LIMIT),"strategy":"$(STRATEGY)"}' | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# auth-check — prove the admin token is accepted by the LIVE server.
#
# Probes /api/system/doctor: an admin-authenticated route that is mounted by
# the system module UNCONDITIONALLY.
#
# It formerly probed /api/artlist/job-consumer, which the canonical
# config.yaml deliberately does not mount (`features.artlist_enabled: false` —
# "YouTube is the only external acquisition provider. The Stock pipeline remains
# enabled because its acquisition/search path is YouTube-backed; Artlist is
# intentionally not mounted."). That made this gate report "Velox
# authentication failed: HTTP 404" on a perfectly healthy, intentionally
# configured deployment, and it blocked every target that depends on it —
# verify-pipeline-e2e-live could not start at all. An auth probe must assert
# AUTHENTICATION against a route the canonical configuration actually
# registers; a feature-gated route conflates "module disabled on purpose" with
# "token rejected".
#
# --max-time is generous because /api/system/doctor runs real probes (Drive
# canary, ffmpeg/ollama); it is not an availability check for those.
auth-check:
	@scripts/with-velox-auth bash -c 'code=$$(curl -sS -o /dev/null -w "%{http_code}" --max-time 30 -H "Authorization: Bearer $$VELOX_ADMIN_TOKEN" $${BASE:-http://127.0.0.1:$${VELOX_PORT:-8000}}/api/system/doctor); if [ "$$code" != "200" ]; then echo "❌ Velox authentication failed: HTTP $$code (/api/system/doctor)"; exit 1; fi; echo "✅ Velox authentication available: HTTP 200"'

regenerate-token:
	@bash scripts/regenerate_token.sh
