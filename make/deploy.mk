# make/deploy.mk - thematic include (deploy closure, September 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive, the build
# chain is split into thematic includes. This file holds ONLY the deploy
# bucket. The driver lives in scripts/deploy/pipelinegen_deploy.sh so the
# logic is testable in isolation (make is a thin wrapper).
#
# Contract (see the driver header for the full rationale):
#   make deploy        build -> install -> restart -> wait-for-ready -> verify identity
#   make deploy-status one block: on-disk hash vs /proc/<MainPID>/exe hash, uptime, health
#   make deploy-wait   block until /health answers (no build, no restart)
#   make deploy-dry-run preflight + plan only (no build, no restart)
#   make deploy-selftest hermetic: exercises restart/ready/identity against fakes
#
# The restart uses scripts/systemd/pipelinegenctl (fail-closed `sudo -n`) and is
# NEVER piped: the historical failure was `sudo -n install ... | tail` reporting
# exit 0 while sudo had refused. Never reintroduce a pipe on a privileged step.

.PHONY: deploy deploy-status deploy-wait deploy-dry-run deploy-selftest

deploy:
	@bash scripts/deploy/pipelinegen_deploy.sh

deploy-status:
	@bash scripts/deploy/pipelinegen_deploy_status.sh

deploy-wait:
	@bash scripts/deploy/pipelinegen_deploy.sh --skip-build --skip-restart --dry-run >/dev/null
	@bash -c 'set -e; deadline=$$(( SECONDS + $${DEPLOY_HEALTH_TIMEOUT:-90} )); \
	  until curl -sf --max-time 2 "$${VELOX_BASE_URL:-http://127.0.0.1:8000}/health" >/dev/null; do \
	    [ $$SECONDS -ge $$deadline ] && { echo "deploy-wait: /health did not answer" >&2; exit 1; }; \
	    sleep 2; \
	  done; echo "deploy-wait: /health is up"'

deploy-dry-run:
	@bash scripts/deploy/pipelinegen_deploy.sh --dry-run

deploy-selftest:
	@bash scripts/deploy/deploy_selftest.sh
