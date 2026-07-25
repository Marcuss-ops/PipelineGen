# syntax=docker/dockerfile:1.7
#
# PipelineGen multi-target Dockerfile.
#
# Multi-stage build with three runtime targets:
#   1) builder         — shared Go compiler (golang:1.25-bookworm, CGO enabled).
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

# ─── admin-ui-builder ─────────────────────────────────────────────
# Build the React/Vite admin UI so the Go server can embed it.
FROM --platform=$BUILDPLATFORM node:22-bookworm AS admin-ui-builder

WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm install --silent
COPY web/ ./
RUN npm run build

# ─── builder ─────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS builder
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

# Copy the built admin UI into the tree so web/embed.go can embed it.
COPY --from=admin-ui-builder /src/web/dist ./web/dist

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

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/pipelinegen /usr/local/bin/pipelinegen

# Copy migrations and config so the server can run DB migrations at startup.
COPY migrations/ /app/migrations/

RUN mkdir -p /data /etc/pipelinegen \
 && chown -R root:root /data /etc/pipelinegen
VOLUME ["/data"]

WORKDIR /app

EXPOSE 8000

HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 \
  CMD curl -fsS http://127.0.0.1:8000/health || exit 1

ENTRYPOINT ["/usr/local/bin/pipelinegen"]
CMD ["--mode", "server", "--config", "/etc/pipelinegen/config.yaml"]

# ─── worker-runtime ───────────────────────────────────────────────
FROM debian:bookworm-slim AS worker-runtime

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      ffmpeg \
      python3 \
 && rm -rf /var/lib/apt/lists/*

# yt-dlp pinned to a recent release. Bumped June 2026 from
# 2024.08.06 -> 2025.03.20 (Q1-2025 stable, quarterly cadence).
ARG YTDLP_VERSION=2025.03.20
RUN curl -fsSL "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp" \
      -o /usr/local/bin/yt-dlp \
 && chmod a+rx /usr/local/bin/yt-dlp

COPY --from=builder /out/worker /usr/local/bin/pipelinegen-worker

# Copy Python scripts and config needed by the worker at runtime.
COPY scripts/ /app/scripts/
COPY config/ /app/config/

RUN mkdir -p /data /etc/pipelinegen \
 && chown -R root:root /data /etc/pipelinegen
VOLUME ["/data"]

WORKDIR /app

ENTRYPOINT ["/usr/local/bin/pipelinegen-worker"]
CMD ["--config", "/app/config/config.yaml"]

# ─── admin-runtime ────────────────────────────────────────────────
FROM debian:bookworm-slim AS admin-runtime

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      jq \
      python3 \
      sqlite3 \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/admin /usr/local/bin/pipelinegen-admin

# Copy Python scripts and config used by admin commands (summarize-book,
# list-styles, backfill-missing, etc.).
COPY scripts/ /app/scripts/
COPY config/ /app/config/

RUN mkdir -p /data /etc/pipelinegen \
 && chown -R root:root /data /etc/pipelinegen
VOLUME ["/data"]

WORKDIR /app

ENTRYPOINT ["/usr/local/bin/pipelinegen-admin"]

# ─── runtime (compatibility alias) ────────────────────────────────
FROM server-runtime AS runtime
