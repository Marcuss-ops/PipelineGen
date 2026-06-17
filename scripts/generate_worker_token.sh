#!/usr/bin/env bash
# scripts/generate_worker_token.sh
#
# Generates a secure 32-byte random hex string suitable for use as a
# PipelineGen VELOX_WORKER_TOKEN. The token is the only credential a
# remote worker uses to authenticate against pipelinegen — it should be
# (a) at least 32 hex chars (256 bits of entropy),
# (b) rotated periodically and immediately if a breach is suspected,
# (c) distributed to workers via env vars, .env files, or a secret manager.
#
# Usage:
#   ./scripts/generate_worker_token.sh            # prints the hex token
#   ./scripts/generate_worker_token.sh --env     # prints a ready-to-paste .env line
#
# Requirements:
#   - openssl on PATH (standard on Linux/macOS).
secho() {
    printf '%s\n' "$1" >&2
}

if ! command -v openssl >/dev/null 2>&1; then
    secho "openssl not found on PATH; install OpenSSL or use a different random source"
    exit 69   # EX_UNAVAILABLE
fi

token=""
case "${1:-}" in
    --env)
        token="$(openssl rand -hex 32)"
        printf 'VELOX_WORKER_TOKEN=%s\n' "$token"
        ;; \
    ""|"-h"|"--help")
        openssl rand -hex 32
        ;; \
    *)
        secho "unknown argument: $1"
        secho "usage: $0 [--env | --help]"
        exit 64
        ;; esac
