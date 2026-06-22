# CHANGELOG — 22 Giugno 2026

## Riepilogo

Checkpoint `architecture-clean-v1` su main. Il framework strict-mode per
prevenire nuove violazioni architetturali e\u00e8 live; Check 17 (gate
`database/sql`). Retention policy per il DB di osservabilit\u00e0 (Disposable + cron).
Subsystem `admin db {status,check,migrations,backup,restore --verify}` operativo.
Wave 13 / Wave 8 / pre-existing build errors rimandati in backlog
documentale (vedi Known limits nel DoD).

## Cose live al checkpoint

- **`scripts/archcheck/main.go --strict`** — flag esposta in
  codex/db-set-and-paths. Rifiuta 601 alias + 61 rule violations vs zero
  target; chiusura Wave 16 = operator override. Ratchet count
  monotonico-decreasing via Check 17 (con baseline congelato di 42 file).
- **`scripts/ci-architectural-checks.sh::Check 17`** — gate zero-NEW-violations
  in `internal/{api,application,domain}` per `import "database/sql"`.
  Baseline = 42 file grandfathered, inlinato nello script (zero JSON
  sidecar / ratchet excuse).
- **`internal/infrastructure/database/set.go`** — `DatabaseSet.OpenSet /
  Migrate / Health / Close`. Unica entrata canonica per aprire
  entrambi i DB (Primary + Observability); nessun `sql.Open` altrove.
- **`cmd/admin db {status,check,migrations,backup,restore --verify}`**\u2014
  subsystem CLI: path / size / WAL / migration status (status);
  PRAGMA integrity + foreign_key + table counts + critical columns +
  index count + Qdrant connectivity (check); ledger schema_migrations
  (migrations); VACUUM INTO + SHA256 (backup); verify + smoke probe
  + RTO/RPO (restore).
- **`scripts/db-restore-drill.sh`** — drill canonica end-to-end su
  staging pulito: backup prod \u2192 clean staging dir \u2192 restore --verify.
- **`internal/infrastructure/database/rotation.go`** — ATTACH offload +
  INSERT INTO offload + DELETE from main + VACUUM. Emette JSON line
  con cutoff / offloaded_to / offloaded_rows / purged_rows /
  bytes_reclaimed / duration_ms.
- **`internal/infrastructure/config/types.go::StorageConfig`** —
  `ObservabilityMaxAgeDays=7`, `ObservabilityMaxSizeMB=1024`,
  `PrimaryDBPath`, `ObservabilityDBPath`, `WorkspaceDir`, `CacheDir`,
  `ExportDir` config fields per il `DatabaseSet`.
- **`architecture/migration.yaml`** — Wave 16 + Wave 17 chiuse a
  `status: done` con `status_note` che documenta l'override dell'operatore.
- **`docs/architecture/CLEAN_STRUCTURE_DEFINITION_OF_DONE.md`** —
  Certification identity + Approval blocks popolati. 6 Known limits
  espliciti documentano il backlog non-bloccante.
- **`.github/workflows/ci.yml`** — annotation comment nel step
  "Run Architectural Checks" che descrive Check 17 goal + baseline.

## Files changed (commit singolo pulito su main)

```
docs/CHANGELOG_2026-06-22.md                             (NEW)
docs/POST_CASCADE_OPERATIONAL_READINESS.md                (+§9)
docs/architecture/CLEAN_STRUCTURE_DEFINITION_OF_DONE.md  (cert id + Approval)
architecture/migration.yaml                               (Wave 16/17 → done)
internal/infrastructure/database/doctor.go               (NEW, Branch 1)
internal/infrastructure/database/backup.go               (NEW, Branch 1)
internal/infrastructure/database/rotation.go             (NEW, Branch 2)
internal/infrastructure/database/set.go                  (DatabaseSet, codex/db-set-and-paths)
internal/infrastructure/config/types.go                  (+ 6 StorageConfig fields, + retention)
cmd/admin/db.go                                          (NEW, dispatcher)
cmd/admin/db_status.go                                   (NEW)
cmd/admin/db_check.go                                    (NEW)
cmd/admin/db_migrations.go                               (NEW)
cmd/admin/db_backup.go                                   (NEW)
cmd/admin/db_restore.go                                  (NEW)
cmd/admin/db_rotate.go                                   (NEW, real impl in Branch 2)
cmd/admin/main.go                                        (+ case "db", -case "migrate-status")
cmd/admin/migrate_status.go                              (DELETED)
cmd/admin/logger.go                                      (comment-only update)
scripts/db-restore-drill.sh                              (NEW)
scripts/ci-architectural-checks.sh                       (+ Check 17)
.github/workflows/ci.yml                                  (annotation comment, run:| block-scalar)
ARCHITECTURE.md                                           (§10 update, §12b NEW retention policy)
```

## Known limits documentati

1. Wave 13 (`internal/media/` namespace elimination) — 89 file attivi in
   19 sub-directory. Bloccata da Wave 10 + 11.
2. Wave 8 (21 importers verso `internal/application/assets/{association,realtime}/`).
3. Check 17 baseline 42 file grandfathered (zero NEW).
4. Pre-existing build/vet errors in 10 file (composition.go, impl.go,
   artlist_handlers.go, ecc.) che pre-datano codex/db-set-and-paths.
   `go vet ./...` e `go build ./...` escono NON-ZERO oggi.
5. `go test ./...` esce NON-ZERO con 7 pacchetti skip documentati.
6. `go run ./scripts/archcheck --strict` esce 1 (601 alias + 61 rule
   violations vs zero target). Wave 16 chiuso per operator override.

## Operatori

Tag canonico: **`architecture-clean-v1`** su main. Push a origin
eseguito. Qualsiasi PR futuro che reintroduca un legacy root, un
database ad-hoc, un owner duplicato, o un forbidden import invalida
l'Approval della cert (vedi §"Approval" del DoD).
