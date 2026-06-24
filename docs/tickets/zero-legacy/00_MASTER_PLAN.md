# PipelineGen — Piano esecutivo Zero Legacy

Ogni ticket parte da `origin/main`, usa una sola branch dedicata e produce una sola PR. Vietati push diretti su `main`, branch secondarie, alias temporanei, wrapper pass-through e aggiornamenti di baseline usati per nascondere violazioni.

## Fase 0 — Gate

- [PG-001 — Ratchet e strict](PG-001_SEPARARE_RATCHET_E_STRICT_ARCHITETTONICO.md) — `codex/pg-001-archcheck-modes`

## Fase 1 — Boundary più rischiosi

- [PG-008 — SQL jobs e outbox](PG-008_RIMUOVERE_DATABASE_SQL_DA_APPLICATION_JOBS_E_OUTBOX.md) — `codex/pg-008-sql-jobs-outbox`
- [PG-017 — Contratto job service](PG-017_JOB_SERVICE_CONTRACT.md) — `codex/pg-017-job-service-contract`
- [PG-018 — Boundary jobs application](PG-018_TICKET.md) — `codex/pg-018-jobs-boundary`
- [PG-019 — Ownership storage](storage-ownership.md) — `codex/pg-019-storage-ownership`
- [PG-020 — AppDeps e teardown](ticket-pg020.md) — `codex/pg-020-appdeps-types`
- [PG-016 — Boundary YouTube](PG-016_COMPLETARE_IL_BOUNDARY_APPLICATION_YOUTUBE.md) — `codex/pg-016-youtube-boundary`

## Fase 2 — API verso application/domain

- [PG-002 — Channels e Images](PG-002_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_CHANNELS_E_IMAGES.md) — `codex/pg-002-api-channels-images`
- [PG-003 — Sound Effect e YouTube](PG-003_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_SOUND_EFFECT_E_YOUTUBE.md) — `codex/pg-003-api-soundeffect-youtube`
- [PG-004 — Artlist](PG-004_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_ARTLIST.md) — `codex/pg-004-api-artlist`
- [PG-005 — Clips](PG-005_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_CLIPS.md) — `codex/pg-005-api-clips`
- [PG-006 — Middleware](PG-006_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_MIDDLEWARE.md) — `codex/pg-006-api-middleware`
- [PG-007 — Root API](PG-007_ELIMINARE_IMPORT_INFRASTRUCTURE_DAI_FILE_ROOT_API.md) — `codex/pg-007-api-root`

## Fase 3 — SQL fuori dai layer vietati

- [PG-009 — Application assets](PG-009_RIMUOVERE_DATABASE_SQL_DA_APPLICATION_ASSETS.md) — `codex/pg-009-sql-assets-application`
- [PG-010 — Domain asset](PG-010_RIMUOVERE_DATABASE_SQL_DAL_DOMAIN_ASSET.md) — `codex/pg-010-sql-domain-asset`
- [PG-011 — API middleware e test](PG-011_RIMUOVERE_DATABASE_SQL_DA_API_MIDDLEWARE_E_TEST_INTEGRATION.md) — `codex/pg-011-sql-api-middleware-tests`
- [PG-012 — Books, Images, Voiceover e Scripts](PG-012_RIMUOVERE_DATABASE_SQL_DA_BOOKS_IMAGES_VOICEOVER_E_SCRIPTS.md) — `codex/pg-012-sql-remaining-application`
- [PG-013 — Rimozione baseline](PG-013_ELIMINARE_DEFINITIVAMENTE_ALLOWLIST_API_E_BASELINE_SQL.md) — `codex/pg-013-remove-legacy-baselines`

## Fase 4 — Compatibilità runtime e servizi fittizi

- [PG-014 — Configurazione worker](PG-014_CANONICALIZZARE_LA_CONFIGURAZIONE_DEL_WORKER.md) — `codex/pg-014-worker-url`
- [PG-015 — Capability immagini](PG-015_RIMUOVERE_CAPABILITY_IMMAGINI_NON_IMPLEMENTATE.md) — `codex/pg-015-images-drop-stubs`
- [PG-021 — Migrazione documenti Drive](ticket-pg021.md) — `codex/pg-021-drive-doc-migration`
- [PG-022 — Provisioning folder Drive](ticket-pg022.md) — `codex/pg-022-drive-folder-provisioning`
- [PG-023 — Retry e payload job](ticket-pg023.md) — `codex/pg-023-job-contract`
- [PG-024 — Route script](ticket-pg024.md) — `codex/pg-024-script-routes`
- [PG-034 — Qdrant](ticket-pg034.md) — `codex/pg-034-qdrant-capability`
- [PG-035 — GemmaMemory](ticket-pg035.md) — `codex/pg-035-gemmamemory`
- [PG-036 — Dispatcher outbox](ticket-pg036.md) — `codex/pg-036-outbox-dispatcher`
- [PG-037 — API deprecate](ticket-pg037.md) — `codex/pg-037-deprecated-utils`

## Fase 5 — God object e mega-package

- [PG-028 — composition.go](ticket-pg028.md) — `codex/pg-028-composition-split`
- [PG-029 — scripts/types.go](ticket-pg029.md) — `codex/pg-029-scripts-types`
- [PG-030 — domain/asset](ticket-pg030.md) — `codex/pg-030-domain-asset-split`
- [PG-031 — internal/app](ticket-pg031.md) — `codex/pg-031-app-package-compaction`
- [PG-032 — Artlist application](ticket-pg032.md) — `codex/pg-032-artlist-package`
- [PG-033 — Wiring tipizzati](ticket-pg033.md) — `codex/pg-033-typed-wiring`
- [PG-038 — YouTube tagutil](ticket-pg038.md) — `codex/pg-038-youtube-tagutil`
- [PG-039 — lifecycle.go](ticket-pg039.md) — `codex/pg-039-app-lifecycle`
- [PG-040 — Asset sourcing](ticket-pg040.md) — `codex/pg-040-asset-sourcing`
- [PG-041 — Flow helpers](ticket-pg041.md) — `codex/pg-041-script-flow-helpers`
- [PG-042 — Script pipeline](ticket-pg042.md) — `codex/pg-042-script-pipeline`
- [PG-043 — Metadata export](ticket-pg043.md) — `codex/pg-043-metadata-export`
- [PG-044 — YouTube orchestrator](ticket-pg044.md) — `codex/pg-044-youtube-orchestrator`
- [PG-045 — YouTube metadata](ticket-pg045.md) — `codex/pg-045-youtube-metadata`
- [PG-047 — Registry admin](ticket-pg047.md) — `codex/pg-047-admin-registry`

## Fase 6 — Strumenti e documentazione transitoria

- [PG-025 — Tracker](ticket-pg025.md) — `codex/pg-025-cleanup-trackers`
- [PG-026 — Comandi admin migratori](ticket-pg026.md) — `codex/pg-026-admin-retirement`
- [PG-027 — Commenti storici](ticket-pg027.md) — `codex/pg-027-comment-cleanup`

## Fase 7 — Chiusura

- [PG-046 — Strict CI e chiusura Wave](ticket-pg046.md) — `codex/pg-046-final-zero-legacy`

## Regole di assegnazione

- Un agente riceve un solo ticket alla volta.
- L’agente non sceglie il ticket successivo.
- Una branch e una PR per ticket.
- Ticket sovrapposti si eseguono in ordine numerico.
- Nessun merge senza check verdi o evidenza locale completa.

## Definition of Done globale

- `go test ./...`
- `go vet ./...`
- `go build ./...`
- `go run ./scripts/archcheck --strict`
- zero import API verso adapter concreti;
- zero SQL nei layer vietati;
- zero baseline e allowlist;
- zero route o capability finte;
- zero alias e wrapper di compatibilità;
- un solo writer e registry per stato o capability;
- Wave 14–17 chiuse con evidenza.
