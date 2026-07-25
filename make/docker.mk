# make/docker.mk - thematic include (P2 Manutenibilita, July 2026).
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 directive,
# the canonical build chain is split into 7 thematic includes.
# This file holds only the docker-bucket targets. Cross-bucket
# dependencies (e.g. verify-artlist-live -> auth-check) resolve
# naturally via Make's recursive target resolution.
# Root Makefile contains include make/*.mk plus all: build.

docker-build:
	@test -f Dockerfile || { echo "❌ Dockerfile not found"; exit 1; }
	docker build \
		--target $${TARGET:-server-runtime} \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t pipelinegen:latest .

# docker-build-worker: build ONLY the worker image (worker-runtime target)
# for certification and signing.
docker-build-worker:
	@test -f Dockerfile || { echo "❌ Dockerfile not found"; exit 1; }
	docker build \
		--target worker-runtime \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t pipelinegen-worker:latest .

# Docker run: maps the canonical VELOX_PORT (default 8000) host port
docker-run: docker-build
	docker run -p $${VELOX_PORT:-8000}:8000 --env-file .env pipelinegen:latest
# ─── Image certification (Barriera 2, June 2026) ──────────────────────

# docker-sign: Build the worker image and sign it with Cosign.
#
# Modes (COSIGN_MODE env):
#   keyless (default) — OIDC-based keyless signing (GitHub Actions or browser flow)
#   key               — use cosign.key / cosign.pub key pair
#
# Output: prints IMAGE_DIGEST=sha256:... for downstream pinning.
#
# Prerequisites:
#   - cosign v2.4+ installed (go install github.com/sigstore/cosign/v2/cmd/cosign@latest)
#   - docker available
#   - (key mode) cosign.key + cosign.pub in project root
#
# Usage:
#   make docker-sign                                    # keyless
#   make docker-sign COSIGN_MODE=key                    # key pair
#   make docker-sign IMAGE=ghcr.io/org/worker:v1.0      # custom image ref
docker-sign: docker-build-worker
	@bash scripts/cosign-sign.sh $${IMAGE:-pipelinegen-worker:latest}

# docker-digest: Print the SHA256 digest of the worker image for pinning
# in docker-compose.yml or deployment manifests.
#
# WARNING: this target REQUIRES the image to have been pushed to a
# registry first (docker push). Without a push, RepoDigests is empty
docker-digest:
	@echo "→ Worker image digest:"
	@DIGEST=$$(docker inspect --format='{{index .RepoDigests 0}}' pipelinegen-worker:latest 2>/dev/null); \
	if [ -n "$$DIGEST" ]; then \
		echo "$$DIGEST"; \
	else \
		echo "ERROR: No RepoDigests found — image has NOT been pushed to a registry." >&2; \
		echo "" >&2; \
		echo "  Remediation:" >&2; \
		echo "    1. Push the image:  docker push pipelinegen-worker:latest" >&2; \
		echo "    2. Re-run:          make docker-digest" >&2; \
		echo "" >&2; \
		echo "  NOTE: docker inspect {{.Id}} is a layer ID — NOT pinnable." >&2; \
		echo "  Do NOT use it as a docker-compose digest reference." >&2; \
		exit 1; \
	fi

# docker-verify-digest: Verify the running container's image matches the
# pinned SHA256 digest in docker-compose.yml. Fails on mismatch.
# Usage: make docker-verify-digest CONTAINER=pipelinegen-worker
docker-verify-digest:
	@bash scripts/verify-image-digest.sh $${CONTAINER:-pipelinegen-worker} --strict

# docker-verify-ffmpeg: Probe the worker image for engine binaries
# (ffmpeg, ffprobe, yt-dlp, python3). Part of Barriera 2 image certification.
# Usage: make docker-verify-ffmpeg IMAGE=pipelinegen-worker:latest
docker-verify-ffmpeg:
	@bash scripts/verify-ffmpeg.sh $${IMAGE:-pipelinegen-worker:latest}

# docker-bootstrap-smoke: Quick smoke test of the worker binary in the
# image — verifies ENTRYPOINT, --help, and version output.
# Usage: make docker-bootstrap-smoke IMAGE=pipelinegen-worker:latest
docker-bootstrap-smoke:
	@bash scripts/worker-bootstrap-smoke.sh $${IMAGE:-pipelinegen-worker:latest}
test-qdrant-fixtures:
	@echo "→ Starting ephemeral Qdrant on port $${TEST_QDRANT_PORT:-16333}..."
	docker compose -f docker-compose.test-qdrant.yml up -d --wait 2>/dev/null || \
		docker compose -f docker-compose.test-qdrant.yml up -d
	@sleep 3  # give Qdrant time to accept connections
	@echo "→ Running synthetic asset integration tests..."
	TEST_QDRANT_URL=http://localhost:$${TEST_QDRANT_PORT:-16333} $(GO) test -tags=integration -v -count=1 ./tests/fixtures/... || \
		(echo "→ Tests failed — tearing down Qdrant..."; \
		 docker compose -f docker-compose.test-qdrant.yml down --volumes 2>/dev/null; \
		 exit 1)
	@echo "→ Tests passed — tearing down Qdrant..."
	docker compose -f docker-compose.test-qdrant.yml down --volumes 2>/dev/null
	@echo "✅ test-qdrant-fixtures OK"

# test-qdrant-fixtures-down: Tear down the test Qdrant container.
# Use this to clean up after a failed/aborted test run.
test-qdrant-fixtures-down:
