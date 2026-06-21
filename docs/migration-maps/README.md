# Legacy Directory Migration Maps — ✅ ALL COMPLETE (June 2026)

Every legacy directory listed in the AGENTS.md Legacy Directories Policy has been
eliminated or migrated. This directory is retained as a historical record.

| Legacy directory | Target | Status |
|---|---|---|
| `internal/core/`                 | `internal/domain/asset/` or `internal/infrastructure/<X>/` | PR4 ✅ |
| `internal/media/monitor/`         | `internal/application/monitor/` | Wave 10 ✅ |
| `internal/media/ingest/`          | `internal/application/ingest/` | Wave 11 ✅ |
| `internal/media/mediaasset/`      | `internal/infrastructure/media/processor/` | Wave 12 ✅ |
| `internal/assets/`               | `internal/domain/asset/` | Pre-wave-7 ✅ |
| `internal/artifacts/`            | `internal/application/assets/artifacts/` | Wave 7 ✅ |
| `internal/sources/{youtube,artlist}/` | `internal/application/assets/providers/<X>/` | Pre-wave-9 ✅ |
| `internal/upload/drive/`         | `internal/infrastructure/drive/` | Wave 8 ✅ |
| `internal/application/scriptflow/` | `internal/application/scripts/` | Wave 6 ✅ |
| `internal/domain/media/`         | `internal/domain/asset/` | Wave 4C ✅ |
| `internal/domain/worker/`        | `internal/domain/job/` | Pre-wave-7 ✅ |
| `internal/domain/outbox/`        | `internal/domain/lifecycle/` | Pre-wave-7 ✅ |

The CI guard (`scripts/ci-architectural-checks.sh` Check 13) was retired — the
`LEGACY_DIRS` array is now empty.

## Per-directory manifests

Each `*.md` file in this directory documents the original migration plan and
has been updated with a completion header.

- [internal-core.md](internal-core.md)
- [internal-media.md](internal-media.md)
- [internal-assets.md](internal-assets.md)
- [internal-artifacts.md](internal-artifacts.md)
- [internal-sources.md](internal-sources.md)
- [internal-upload.md](internal-upload.md)
- [internal-application-scriptflow.md](internal-application-scriptflow.md)
- [internal-domain-media.md](internal-domain-media.md)
- [internal-domain-worker.md](internal-domain-worker.md)
- [internal-domain-outbox.md](internal-domain-outbox.md)
