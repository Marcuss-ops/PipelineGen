# syntax=docker/dockerfile:1.7
#
# PipelineGen multi-target Dockerfile.
#
# Multi-stage build with three runtime targets:
#   1) builder         — shared Go compiler (golang:${GO_VERSION}-bookworm, CGO enabled).
#   2) server-runtime  — HTTP server only (ca-certificates + curl).
#   3) worker-runtime  — background job executor (ffmpeg + yt-dlp + python3).
#   4) admin-runtime   — one-shot admin CLI (sqlite3 + jq + python3).
#
# Compatibility alias — `runtime` points to `server-runtime` so existing
# deployment scripts that reference `--target runtime` continue to work.
#
# Each target copies only its own binary. No target contains the other
# binaries, and the server target does not carry media tools it will
# never use.

# ─── builder ─────────────────────────────────────────────────────
# Phase 6 (ci/full-repository-gate, June 2026): Go version SSOT.
# The canonical Go toolchain version is parsed from go.mod's `go`
# directive by the Makefile (GO_VERSION), the GitHub Actions setup-go
# steps (go-version-file: 'go.mod'), and the preflight check (which
# asserts this ARG exists + the FROM line uses ${GO_VERSION}). The
# Dockerfile consumes it via the GO_VERSION build arg (defaulted to
# the value matching the current go.mod so `docker build .` without
# `--build-arg` still works). Operators that need a different pin
# pass `--build-arg GO_VERSION=1.26` etc.
ARG GO_VERSION=1.25
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS builder
ARG TARGETOS=linux
ARG TARGETPLATFORM
ARG VERSION=dev
ARG COMMIT=unknown

# CGO is required (mattn/go-sqlite3).
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential \
      pkg-config \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Layer-cache friendly: copy go.mod/go.sum first, download deps, then copy
# the rest of the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Compile the three canonical binaries.
ENV CGO_ENABLED=1
RUN mkdir -p /out \
 && go build -trimpath \
      -ldflags "-s -w -X main.buildVersion=${VERSION} -X main.commitHash=${COMMIT}" \
      -o /out/pipelinegen ./cmd/server \
 && go build -trimpath \
      -ldflags "-s -w -X main.buildVersion=${VERSION} -X main.commitHash=${COMMIT}" \
      -o /out/worker ./cmd/worker \
 && go build -trimpath \
      -ldflags "-s -w -X main.buildVersion=${VERSION} -X main.commitHash=${COMMIT}" \
      -o /out/admin ./cmd/admin

# ─── server-runtime ───────────────────────────────────────────────
FROM debian:bookworm-slim AS server-runtime

# Phase 7 (docker-compose-hardening, June 2026): install passwd +
# add the non-root `pipelinegen` user (UID/GID 1000) before any
# COPY/chown. The standard 1000 UID matches the node-scraper base
# image's `node` user and the typical operator workstation UID,
# keeping bind-mount permissions predictable across developer +
# CI + production.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      passwd \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -r -g 1000 pipelinegen \
 && useradd -r -u 1000 -g pipelinegen -m -d /home/pipelinegen -s /sbin/nologin pipelinegen

COPY --from=builder /out/pipelinegen /usr/local/bin/pipelinegen

# Copy migrations and config so the server can run DB migrations at startup.
COPY --chown=pipelinegen:pipelinegen migrations/ /app/migrations/

RUN mkdir -p /data /etc/pipelinegen /home/pipelinegen \
 && chown -R pipelinegen:pipelinegen /data /etc/pipelinegen /app /home/pipelinegen
VOLUME ["/data"]

# Phase 7: drop root before the entrypoint. Operators that need to
# chown a named volume on first boot (the canonical "named volume
# was created with root ownership by Docker" problem) should use
# the entrypoint wrapper at docker-entrypoint.sh which re-chowns
# /data before exec'ing the binary.
COPY --chown=pipelinegen:pipelinegen docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
USER pipelinegen

WORKDIR /app

EXPOSE 8000

HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 \
  CMD curl -fsS http://127.0.0.1:8000/health || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/pipelinegen", "--mode", "server", "--config", "/etc/pipelinegen/config.yaml"]

# ─── worker-runtime ───────────────────────────────────────────────
FROM debian:bookworm-slim AS worker-runtime

# Phase 7 (docker-compose-hardening, June 2026): non-root `pipelinegen`
# user (UID/GID 1000) + entrypoint wrapper for named-volume chown.
# Mirrors the server-runtime pattern.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      ffmpeg \
      python3 \
      passwd \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -r -g 1000 pipelinegen \
 && useradd -r -u 1000 -g pipelinegen -m -d /home/pipelinegen -s /sbin/nologin pipelinegen

# yt-dlp pinned to a recent release. Bumped June 2026 from
# 2024.08.06 -> 2025.03.20 (Q1-2025 stable, quarterly cadence).
ARG YTDLP_VERSION=2025.03.20
RUN curl -fsSL "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp" \
      -o /usr/local/bin/yt-dlp \
 && chmod a+rx /usr/local/bin/yt-dlp

COPY --from=builder /out/worker /usr/local/bin/pipelinegen-worker

# Phase 7: Copy Python scripts and config needed by the worker at
# runtime. The current COPY is a catch-all (scripts/) — the
# Phase-7 audit asked to narrow to only the invoked scripts, but
# the Go worker code invokes scripts via cfg.External.* paths
# (no direct script paths in source) so a static analysis cannot
# deterministically identify the subset. The conservative
# catch-all is preserved here; a follow-up PR can instrument the
# worker with a script-usage trace + narrow the COPY.
COPY --chown=pipelinegen:pipelinegen scripts/ /app/scripts/
COPY --chown=pipelinegen:pipelinegen config/ /app/config/

RUN mkdir -p /data /etc/pipelinegen /home/pipelinegen \
 && chown -R pipelinegen:pipelinegen /data /etc/pipelinegen /app /home/pipelinegen
VOLUME ["/data"]

COPY --chown=pipelinegen:pipelinegen docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
USER pipelinegen

WORKDIR /app

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/pipelinegen-worker", "--config", "/app/config/config.yaml"]

# ─── admin-runtime ────────────────────────────────────────────────
FROM debian:bookworm-slim AS admin-runtime

# Phase 7 (docker-compose-hardening, June 2026): non-root `pipelinegen`
# user (UID/GID 1000). Admin commands are one-shot so the named-volume
# chown is less critical (no persistent data), but we keep the same
# pattern for consistency.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      jq \
      python3 \
      sqlite3 \
      passwd \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -r -g 1000 pipelinegen \
 && useradd -r -u 1000 -g pipelinegen -m -d /home/pipelinegen -s /sbin/nologin pipelinegen

COPY --from=builder /out/admin /usr/local/bin/pipelinegen-admin

# Copy Python scripts and config used by admin commands (summarize-book,
# list-styles, backfill-missing, etc.).
COPY --chown=pipelinegen:pipelinegen scripts/ /app/scripts/
COPY --chown=pipelinegen:pipelinegen config/ /app/config/

RUN mkdir -p /data /etc/pipelinegen /home/pipelinegen \
 && chown -R pipelinegen:pipelinegen /data /etc/pipelinegen /app /home/pipelinegen
VOLUME ["/data"]

COPY --chown=pipelinegen:pipelinegen docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
USER pipelinegen

WORKDIR /app

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/pipelinegen-admin"]

# ─── runtime (compatibility alias) ────────────────────────────────
FROM server-runtime AS runtime
