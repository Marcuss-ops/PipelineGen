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

# ─── Image certification: partially RETIRED 2026-09-13 ───────────────
#
# docker-sign, docker-verify-digest and docker-verify-ffmpeg invoked
# scripts/{cosign-sign,verify-image-digest,verify-ffmpeg}.sh, all deleted by
# commit 7e6965aab ("purge 94% shell + 87% python dust"). A target whose only
# possible outcome is "No such file or directory" is not a gate. What survives
# here is the part that still has a tracked implementation:
#
#   docker-build / docker-build-worker  — real docker build
#   docker-digest                       — real docker inspect (RepoDigests)
#   docker-verify-whisper               — scripts/verify-whisper.sh (present)
#
# NOTE: docker-compose signing/digest pinning is therefore no longer
# machine-verified. Reimplement the three probes in Go (or restore tracked
# scripts) before re-adding them as targets.

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

# docker-verify-whisper: Probe the worker image for the pinned Whisper
# runtime without downloading a model or requiring a GPU.
# Usage: make docker-verify-whisper IMAGE=pipelinegen-worker:latest
docker-verify-whisper:
	@bash scripts/verify-whisper.sh $${IMAGE:-pipelinegen-worker:latest}

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

# test-postgres: Run the PostgreSQL media-domain parity suite against an
# ephemeral PostgreSQL 18 + pgvector container (pgvector/pgvector:pg18).
# Mirrors the test-qdrant-fixtures flow: up --wait → go test → down.
# The suite skips (never fakes availability) when the container is absent.
test-postgres:
	@echo "→ Starting ephemeral PostgreSQL 18 + pgvector on port 16432..."
	docker compose -f docker-compose.test-postgres.yml up -d --wait 2>/dev/null || \
		docker compose -f docker-compose.test-postgres.yml up -d
	@echo "→ Running PostgreSQL media parity tests..."
	TEST_POSTGRES_DSN="postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media_test?sslmode=disable" \
		$(GO) test -count=1 -v ./internal/platform/postgres/media/ || \
		(echo "→ Tests failed — tearing down PostgreSQL..."; \
		 docker compose -f docker-compose.test-postgres.yml down --volumes 2>/dev/null; \
		 exit 1)
	@echo "→ Tests passed — tearing down PostgreSQL..."
	docker compose -f docker-compose.test-postgres.yml down --volumes 2>/dev/null
	@echo "✅ test-postgres OK"

# test-postgres-down: Tear down the test PostgreSQL container.
# Use this to clean up after a failed/aborted test run.
test-postgres-down:
	docker compose -f docker-compose.test-postgres.yml down --volumes 2>/dev/null
