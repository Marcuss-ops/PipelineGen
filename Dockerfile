# syntax=docker/dockerfile:1.7
#
# PipelineGen multi-target Dockerfile.
#
# Multi-stage build with three runtime targets:
#   1) builder         — shared Go compiler (golang:1.25-bookworm, CGO enabled).
#   2) server-runtime  — HTTP server only (ca-certificates + curl).
#   3) worker-runtime  — background job executor (ffmpeg + yt-dlp + Python/Whisper).
#   4) admin-runtime   — one-shot admin CLI (sqlite3 + jq + python3).
#
# Compatibility alias — `runtime` points to `server-runtime` so existing
# deployment scripts that reference `--target runtime` continue to work.
#
# Each target copies only its own binary. No target contains the other
# binaries, and the server target does not carry media tools it will
# never use.

# ─── admin-ui-builder ─────────────────────────────────────────────
# Build the React/Vite admin UI through the canonical web-build target so
# Docker, local verification, and clean-checkout CI use the same lockfile,
# Node-version guard, Vite build, and embed-entrypoint check.
FROM --platform=$BUILDPLATFORM node:22-bookworm AS admin-ui-builder

RUN apt-get update \
 && apt-get install -y --no-install-recommends make \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY make/build.mk ./make/build.mk
COPY scripts/ci/node-version-check.sh ./scripts/ci/node-version-check.sh
COPY node-scraper/package.json ./node-scraper/package.json
COPY web/ ./web/
RUN make -f make/build.mk web-build


# ─── builder ─────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS builder
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
      python3 \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/pipelinegen /usr/local/bin/pipelinegen

# Copy migrations and config so the server can run DB migrations at startup.
COPY migrations/ /app/migrations/
COPY config/ /app/config/
COPY scripts/bridges/ /app/scripts/bridges/

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
      python3-venv \
 && rm -rf /var/lib/apt/lists/*

# Keep the Python ML runtime isolated from Debian's externally-managed
# system interpreter (PEP 668). The manifest pins the Whisper inference
# engine, CTranslate2, and the CUDA 12/cuDNN 9 runtime wheels together.
# Keep copies in the image so image certification can compare installed
# metadata against the direct manifest and exact lock used during the build.
COPY scripts/requirements-whisper.txt /opt/whisper/requirements.txt
COPY requirements/whisper.lock.txt /opt/whisper/requirements.lock.txt
RUN python3 -m venv /opt/whisper-venv \
 && /opt/whisper-venv/bin/python -m pip install --no-cache-dir \
      --requirement /opt/whisper/requirements.lock.txt \
 && site_packages=$(/opt/whisper-venv/bin/python -c 'import site; print(site.getsitepackages()[0])') \
 && ln -s "$site_packages/nvidia/cublas/lib" /opt/whisper-venv/cublas-lib \
 && ln -s "$site_packages/nvidia/cuda_nvrtc/lib" /opt/whisper-venv/cuda-nvrtc-lib \
 && ln -s "$site_packages/nvidia/cudnn/lib" /opt/whisper-venv/cudnn-lib
ENV PATH="/opt/whisper-venv/bin:${PATH}" \
    LD_LIBRARY_PATH="/opt/whisper-venv/cublas-lib:/opt/whisper-venv/cuda-nvrtc-lib:/opt/whisper-venv/cudnn-lib" \
    VELOX_WHISPER_DEVICE="auto" \
    VELOX_WHISPER_MODEL="base" \
    VELOX_WHISPER_CUDA_LIB_DIR="/opt/whisper-venv/cublas-lib"

# yt-dlp pinned to the official stable release. Use the generic Python
# executable rather than the x86-64-only standalone Linux binary so the
# worker remains usable on supported Docker architectures; python3 is
# installed above.
# The previous pin was not a published release tag and returned HTTP 404.
ARG YTDLP_VERSION=2026.07.04
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
