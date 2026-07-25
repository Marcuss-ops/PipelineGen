# make/operations.auth.mk - thematic include (P2 Manutenibilita, July 2026).
# Sub-bucket of the former make/operations.mk (414 lines → 4 sub-files).
# Holds auth-related operational targets: doctor, artlist, auth-check,
# regenerate-token, scraper-up.
# Root Makefile contains include make/*.mk plus all: build.

doctor:
	@[ -n "$$VELOX_ADMIN_TOKEN" ] || { echo "❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth or export manually."; exit 1; }
	@curl -sS -f -H "Authorization: Bearer $(VELOX_ADMIN_TOKEN)" http://127.0.0.1:$${VELOX_PORT:-8000}/api/system/doctor | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# Run artlist pipeline via POST /api/artlist/run.
# Usage: make artlist TERM=technology LIMIT=10 STRATEGY=default
# Port is read from $VELOX_PORT (canonical default 8000).
# Admin token is read from $VELOX_ADMIN_TOKEN (canonical SSOT). The forbidden
# legacy `ADMIN_TOKEN ?= test-admin-token-12345` placeholder has been removed;
# callers must export VELOX_ADMIN_TOKEN (= `scripts/with-velox-auth` wrapped) or
# the recipe fails closed at the curl layer.
TERM ?= technology
artlist:
	@[ -n "$$VELOX_ADMIN_TOKEN" ] || { echo "❌ VELOX_ADMIN_TOKEN unset — source scripts/with-velox-auth or export manually."; exit 1; }
	@curl -sS -f -X POST http://127.0.0.1:$${VELOX_PORT:-8000}/api/artlist/run \
		-H "Content-Type: application/json" \
		-H "Authorization: Bearer $(VELOX_ADMIN_TOKEN)" \
		-d '{"term":"$(TERM)","limit":$(LIMIT),"strategy":"$(STRATEGY)"}' | jq . || { echo "Server not running? Try: make run (override port via VELOX_PORT)"; exit 1; }

# auth-check — operator pre-flight against the canonical auth-gated
# endpoint. scripts/with-velox-auth loads + validates VELOX_ADMIN_TOKEN
# (canonical SSOT) and exports it; the recipe probes
# /api/artlist/job-consumer with `Authorization: Bearer $$VELOX_ADMIN_TOKEN`
# and fails closed (exit 1) on any non-200 response, printing the actual
# HTTP code on failure. NOT part of `verify-main` (which is headless):
# this gate requires a running server, so it's operator-only and should
# be invoked pre-deploy or post-deploy to verify the live auth surface.
# See AGENTS.md "Authentication SSOT (Velox admin token)" for the SSOT
auth-check:
	@scripts/with-velox-auth bash -c 'code=$$(curl -sS -o /dev/null -w "%{http_code}" --max-time 5 \
		-H "Authorization: Bearer $$VELOX_ADMIN_TOKEN" \
		http://127.0.0.1:$${VELOX_PORT:-8000}/api/artlist/job-consumer); \
	if [ "$$code" != "200" ]; then \
	    echo "❌ Velox authentication failed: HTTP $$code"; \
	    exit 1; \
	fi; \
	echo "✅ Velox authentication available: HTTP 200"'

regenerate-token:
	@bash scripts/regenerate_token.sh

# ─── Sidecar Node scraper (PR-LIVE-VERIFY-1, P0) ───────────────────────────
#
# scraper-up — launches the Node.js artlist scraper sidecar as a background
# process for live-verify runs. Per architecture/issues.yaml::PR-LIVE-VERIFY-1
# follow_up: brings up the sidecar via `node node-scraper/artlist_server.js`
# with CHROME_EXECUTABLE=/usr/bin/google-chrome +
# ARTLIST_SCRAPER_BIND=127.0.0.1 + ARTLIST_SCRAPER_PORT=9123, then
# confirms /health responds healthy=true (the dry-run preflight contract).
#
# This is a thin operator-convenience target (binding, July 2026). The
# canonical long-running path is via systemd unit OR the docker-compose
# `scraper` service entry (see docker-compose.yml); system service is
# the prod path, this target is dev-loop / quickstart.
# NOT part of verify-main — the sidecar requires Chrome + a non-trivial
# Node startup; live-verify batteries (verify-artlist-live) invoke it
# externally. Sidecar logs at /tmp/velox-scraper.log.
# Operator stop: `pkill -f artlist_server.js` or systemd stop.
# NOT idempotent — invoke once, then `pkill -f artlist_server.js` before
# retrying (a second invocation will hit EADDRINUSE on bind, but the
# surviving first sidecar's /health will report green and mask the
# EADDRINUSE).
scraper-up:
	@echo "→ Starting Node artlist scraper sidecar on $${ARTLIST_SCRAPER_BIND:-127.0.0.1}:$${ARTLIST_SCRAPER_PORT:-9123}..."
	@CHROME_EXECUTABLE=$${CHROME_EXECUTABLE:-/usr/bin/google-chrome} \
	ARTLIST_SCRAPER_BIND=$${ARTLIST_SCRAPER_BIND:-127.0.0.1} \
	ARTLIST_SCRAPER_PORT=$${ARTLIST_SCRAPER_PORT:-9123} \
	node node-scraper/artlist_server.js > /tmp/velox-scraper.log 2>&1 &
	@sleep 2 && curl -sf -m 3 "http://$${ARTLIST_SCRAPER_BIND:-127.0.0.1}:$${ARTLIST_SCRAPER_PORT:-9123}/health" >/dev/null && \
		echo "✅ scraper-up: sidecar /health green" || \
