# syntax=docker/dockerfile:1.7
#
# PipelineGen production image.
#
# Multi-stage build:
#   1) builder  — golang:1.25-bookworm with CGO enabled (required because
#      mattn/go-sqlite3 needs a C toolchain). Compiles the three canonical
#      binaries: cmd/server, cmd/worker, cmd/admin. Embeds go build flags
#      so the resulting binaries carry build-version + commit hash.
#
#   2) runtime  — debian:bookworm-slim with the runtime dependencies the
#      binary needs:
#        - ffmpeg          : stock pipeline + video composition
#        - sqlite3         : CLI for manual DB inspection (the binary uses
#                            the mattn/go-sqlite3 CGO engine directly
#                            and does NOT depend on the CLI, but it's
#                            useful in operator shells).
#        - yt-dlp          : YouTube downloads via the in-process
#                            downlooper; pinned to the official release.
#        - curl, ca-certificates : outbound HTTPS (delivery.requested +
#                            Ollama + Drive + node-sidecar scraping).
#        - python3 + pip  : speech/transcription sidecars (book_processor,
#                            tts, semantic_tagger). Kept minimal — explicit
#                            Python deps layered in deployment.
#
# The image expects a /data volume mounted (or a bind mount) for the SQLite
# database, asset downloads, and asset_metadata sidecars.
#
# Default CMD runs the HTTP server with --mode all (HTTP + worker +
# scheduler + maintenance sweepers co-located). Operators that prefer the
# split deployment override the command:
#   docker run ... pipelinegen --mode server   # HTTP only
#   docker run ... pipelinegen-admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
#
# Health: the binary exposes /health (no auth) and /api/health/deep
# (auth). container HEALTHCHECK hits /health on a 10 s interval.
#
# Compatibility: the official image is intended for x86_64. Cross-arch is
# supported by the FROM lines but the --ldflags line embeds host-arch
# defaults. Override TARGETOS/TARGETARCH at build time.

# ─── builder ─────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS builder
ARG TARGETOS=linux
ARG TARGETPLATFORM
ARG VERSION=dev
ARG COMMIT=unknown

# CGO is required (mattn/go-sqlite3). Build tools: gcc, libc-dev.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential \
      pkg-config \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Layer-cache friendly: copy go.mod/go.sum first, download deps, then copy
# the rest of the source. Avoids re-downloading on every source change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Compile the three canonical binaries. CGO_ENABLED=1 is required for
# sqlite3. Output paths are /out/<binary>.
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

# ─── runtime ─────────────────────────────────────────────────────
FROM debian:bookworm-slim AS runtime

# Runtime-only packages. Note: no Go toolchain / no gcc — the final
# image does not need to compile anything.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      ffmpeg \
      jq \
      sqlite3 \
      python3 \
 && rm -rf /var/lib/apt/lists/*

# yt-dlp pinned to a recent release. Updated by ops in their own cadence.
ARG YTDLP_VERSION=2024.08.06
RUN curl -fsSL "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp" \
      -o /usr/local/bin/yt-dlp \
 && chmod a+rx /usr/local/bin/yt-dlp

# Copy the three built binaries from the builder stage. /usr/local/bin
# places them on the standard PATH so CMD can call them by name.
COPY --from=builder /out/pipelinegen /usr/local/bin/pipelinegen
COPY --from=builder /out/worker      /usr/local/bin/pipelinegen-worker
COPY --from=builder /out/admin       /usr/local/bin/pipelinegen-admin

# /data is the operator-mounted volume. SQLite DB + asset directories
# live there. Create with root ownership so the runtime user can write
# through the bind mount on first boot.
RUN mkdir -p /data /etc/pipelinegen \
 && chown -R root:root /data /etc/pipelinegen
VOLUME ["/data"]

# Standard server port. Override via `docker run -p 8080:8080`. The
# container listens on 8080 by default per internal/infrastructure/
# config/types.go (`Server.Port` default).
EXPOSE 8080

# Health: hit /health every 10 s. /health is unauth per internal/api/
# routes.go so the probe does not need a token in the env file.
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 \
  CMD curl -fsS http://127.0.0.1:8080/health || exit 1

# Default command: full pipeline (HTTP + background jobs + maintenance).
# Override in deployment for split modes:
#   docker run ... pipelinegen-worker --mode worker
#   docker run ... pipelinegen-admin gen-api-docs ...
ENTRYPOINT ["/usr/local/bin/pipelinegen"]
CMD ["--mode", "all"]
