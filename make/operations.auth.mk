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

auth-check:
	@scripts/with-velox-auth bash -c 'code=$$(curl -sS -o /dev/null -w "%{http_code}" --max-time 5 -H "Authorization: Bearer $$VELOX_ADMIN_TOKEN" $${BASE:-http://127.0.0.1:$${VELOX_PORT:-8000}}/api/artlist/job-consumer); if [ "$$code" != "200" ]; then echo "❌ Velox authentication failed: HTTP $$code"; exit 1; fi; echo "✅ Velox authentication available: HTTP 200"'

regenerate-token:
	@bash scripts/regenerate_token.sh
