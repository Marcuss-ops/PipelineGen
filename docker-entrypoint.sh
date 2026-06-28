#!/bin/bash
# docker-entrypoint.sh
#
# Phase 7 (docker-compose-hardening, June 2026): entrypoint wrapper for
# the server/worker/admin runtimes. Named volumes mounted at /data
# are created with root ownership by Docker, but the runtimes now run
# as the non-root `pipelinegen` user (UID/GID 1000). This wrapper
# re-chowns /data on every start so the non-root user can write
# SQLite DBs, asset cache, etc. without the operator having to
# manually chown the volume on first boot.
#
# Chown is a no-op (silently succeeds) if /data is not a mount point
# or if the volume is already owned by pipelinegen. The 2>/dev/null
# + || true guards keep the wrapper safe under read-only mounts
# (e.g., CI smoke runs that bind /data to a tmpfs).
#
# After the chown, exec the CMD (or the first positional arg if
# invoked with explicit args). Using `exec` ensures the wrapped
# process replaces the shell so signals (SIGTERM from `docker stop`)
# reach the binary directly.
set -e

# Best-effort chown of the named-volume mount point. Idempotent:
# stat the mount point's UID and skip the recursive chown if the
# non-root user (UID 1000) already owns it. This avoids the
# `chown -R` round-trip on every container restart once the first
# boot has initialised the volume. `2>/dev/null` guards a non-existent
# or unreadable /data (e.g., CI smoke runs that bind /data to a
# tmpfs the entrypoint can't stat).
if [ -d /data ] && [ "$(stat -c %u /data 2>/dev/null)" != "1000" ]; then
  chown -R pipelinegen:pipelinegen /data 2>/dev/null || true
fi

# Exec the CMD (Docker passes the CMD as positional args).
exec "$@"
