# PipelineGen — Piano esecutivo Zero Legacy

# Regole operative obbligatorie

Queste regole valgono per ogni ticket.

## Git e branch

- Base consentita: solo `origin/main`.
- Una sola branch dedicata per ticket, indicata nel ticket.
- Non creare branch secondarie, stacked branch, branch di esperimento o branch di supporto.
- Non lavorare direttamente su `main`.
- Non fare push diretto su `main`.
- Non combinare due ticket nella stessa branch o PR.
- Prima di iniziare:
  ```bash
  git fetch origin
  git checkout main
  git pull --ff-only origin main
  git status -sb
  ```
- La working tree deve essere pulita.
- Creare la branch esatta indicata nel ticket.
- Prima del push:
  ```bash
  git fetch origin
  git rebase origin/main
  git status -sb
  git diff --check
  ```
- Dopo il push:
  ```bash
  git log -n 5 --oneline
  git status -sb
  ```
- Verificare che il commit remoto sia realmente aggiornato.

## Regole architetturali

- Cercare sempre il codice esistente prima di aggiungere nuovi tipi o package.
- Non duplicare registry, resolver, sampler, mapping o logica di routing.
- Ogni nuova astrazione deve entrare nel contratto canonico esistente.
- Nessun alias di compatibilità.
- Nessun wrapper pass-through lasciato “temporaneamente”.
- Nessun fallback silenzioso verso nomi, route, env var o payload vecchi.
- Nessun import `internal/infrastructure/*` da `internal/api`, `internal/application` o `internal/domain`, salvo ticket che sta eliminando una baseline già esistente e solo nei file esplicitamente autorizzati.
- SQL solo sotto `internal/infrastructure/database/**`.
- Adapter concreti costruiti solo in `internal/app`.
- API = trasporto; application = casi d'uso; domain = contratti; infrastructure = implementazioni.
- Non modificare comportamento pubblico non incluso nello scope.
- Non aggiornare baseline o allowlist per nascondere una violazione.
- Non aggiungere nuove feature.
- Non committare file generati, output, `node_modules`, `*.tsbuildinfo`, database o artefatti di test.

## Stop conditions

Fermarsi senza improvvisare se:

- il path indicato non esiste più su `origin/main`;
- il codice canonico esiste già in un altro package;
- la modifica richiede una nuova route, un nuovo payload o un nuovo job type;
- è necessario mantenere una compatibility layer;
- emergono due writer per lo stesso stato persistente;
- il ticket richiede file fuori dallo scope;
- i test dimostrano una dipendenza pubblica non documentata;
- il rebase introduce conflitti architetturali.

In questi casi documentare il blocco nella PR; non inventare una soluzione laterale.

## Strategia di merge

I ticket si eseguono nell'ordine seguente. Ticket della stessa fase possono procedere in parallelo solo se gli scope non si sovrappongono; in caso di sovrapposizione vince l'ordine numerico.

## Fase 0 — Rendere onesti i gate

- [PG-001 — Separare ratchet e strict architettonico](PG-001_SEPARARE_RATCHET_E_STRICT_ARCHITETTONICO.md) — `codex/pg-001-archcheck-modes`

## Fase 1 — Chiudere i boundary più rischiosi

- [PG-008 — Rimuovere database/sql da application jobs e outbox](PG-008_RIMUOVERE_DATABASE_SQL_DA_APPLICATION_JOBS_E_OUTBOX.md) — `codex/pg-008-sql-jobs-outbox`
- [PG-017 — Eliminare il facade legacy del job service](PG-017_ELIMINARE_IL_FACADE_LEGACY_DEL_JOB_SERVICE.md) — `codex/pg-017-job-facade`
- [PG-018 — Separare application/jobs dall'infrastruttura concreta](PG-018_SEPARARE_APPLICATION_JOBS_DALL_INFRASTRUTTURA_CONCRETA.md) — `codex/pg-018-job-service-boundary`
- [PG-019 — Eliminare alias dbs.main e dbs.logs](PG-019_ELIMINARE_ALIAS_DBS_MAIN_E_DBS_LOGS.md) — `codex/pg-019-database-set`
- [PG-020 — Tipizzare AppDeps e unificare teardown](PG-020_TIPIZZARE_APPDEPS_E_UNIFICARE_TEARDOWN.md) — `codex/pg-020-appdeps-types`
- [PG-016 — Completare il boundary application YouTube](PG-016_COMPLETARE_IL_BOUNDARY_APPLICATION_YOUTUBE.md) — `codex/pg-016-youtube-boundary`

## Fase 2 — Ridurre API→infrastructure

- [PG-002 — Eliminare import infrastructure da API Channels e Images](PG-002_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_CHANNELS_E_IMAGES.md) — `codex/pg-002-api-channels-images`
- [PG-003 — Eliminare import infrastructure da API Sound Effect e YouTube](PG-003_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_SOUND_EFFECT_E_YOUTUBE.md) — `codex/pg-003-api-soundeffect-youtube`
- [PG-004 — Eliminare import infrastructure da API Artlist](PG-004_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_ARTLIST.md) — `codex/pg-004-api-artlist`
- [PG-005 — Eliminare import infrastructure da API Clips](PG-005_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_CLIPS.md) — `codex/pg-005-api-clips`
- [PG-006 — Eliminare import infrastructure da API Middleware](PG-006_ELIMINARE_IMPORT_INFRASTRUCTURE_DA_API_MIDDLEWARE.md) — `codex/pg-006-api-middleware`
- [PG-007 — Eliminare import infrastructure dai file root API](PG-007_ELIMINARE_IMPORT_INFRASTRUCTURE_DAI_FILE_ROOT_API.md) — `codex/pg-007-api-root`

## Fase 3 — Portare SQL fuori dai layer vietati

- [PG-009 — Rimuovere database/sql da application assets](PG-009_RIMUOVERE_DATABASE_SQL_DA_APPLICATION_ASSETS.md) — `codex/pg-009-sql-assets-application`
- [PG-010 — Rimuovere database/sql dal domain asset](PG-010_RIMUOVERE_DATABASE_SQL_DAL_DOMAIN_ASSET.md) — `codex/pg-010-sql-domain-asset`
- [PG-011 — Rimuovere database/sql da API middleware e test integration](PG-011_RIMUOVERE_DATABASE_SQL_DA_API_MIDDLEWARE_E_TEST_INTEGRATION.md) — `codex/pg-011-sql-api-middleware-tests`
- [PG-012 — Rimuovere database/sql da books images voiceover e scripts](PG-012_RIMUOVERE_DATABASE_SQL_DA_BOOKS_IMAGES_VOICEOVER_E_SCRIPTS.md) — `codex/pg-012-sql-remaining-application`
- [PG-013 — Eliminare definitivamente allowlist API e baseline SQL](PG-013_ELIMINARE_DEFINITIVAMENTE_ALLOWLIST_API_E_BASELINE_SQL.md) — `codex/pg-013-remove-legacy-baselines`

## Fase 4 — Eliminare compatibilità runtime e stub

- [PG-014 — Canonicalizzare la configurazione master del worker](PG-014_CANONICALIZZARE_LA_CONFIGURAZIONE_MASTER_DEL_WORKER.md) — `codex/pg-014-worker-master-url`
- [PG-015 — Rimuovere capability immagini non implementate](PG-015_RIMUOVERE_CAPABILITY_IMMAGINI_NON_IMPLEMENTATE.md) — `codex/pg-015-images-drop-stubs`
- [PG-021 — Trasformare migrazione Drive legacy in comando one-shot](PG-021_TRASFORMARE_MIGRAZIONE_DRIVE_LEGACY_IN_COMANDO_ONE_SHOT.md) — `codex/pg-021-drive-doc-migration`
- [PG-022 — Estrarre provisioning folder Drive dal bootstrap](PG-022_ESTRARRE_PROVISIONING_FOLDER_DRIVE_DAL_BOOTSTRAP.md) — `codex/pg-022-drive-folder-provisioning`
- [PG-023 — Rendere esplicito il contratto retry e payload job](PG-023_RENDERE_ESPLICITO_IL_CONTRATTO_RETRY_E_PAYLOAD_JOB.md) — `codex/pg-023-job-contract`
- [PG-024 — Eliminare route script legacy duplicate](PG-024_ELIMINARE_ROUTE_SCRIPT_LEGACY_DUPLICATE.md) — `codex/pg-024-script-routes`
- [PG-034 — Rimuovere o collegare lo stub Qdrant](PG-034_RIMUOVERE_O_COLLEGARE_LO_STUB_QDRANT.md) — `codex/pg-034-qdrant-stub`
- [PG-035 — Eliminare GemmaMemory stub](PG-035_ELIMINARE_GEMMAMEMORY_STUB.md) — `codex/pg-035-gemmamemory`
- [PG-036 — Normalizzare ownership di outbox Dispatcher](PG-036_NORMALIZZARE_OWNERSHIP_DI_OUTBOX_DISPATCHER.md) — `codex/pg-036-outbox-dispatcher`
- [PG-037 — Rimuovere utility e API deprecate](PG-037_RIMUOVERE_UTILITY_E_API_DEPRECATE.md) — `codex/pg-037-deprecated-utils`

## Fase 5 — Spezzare god object e mega-package

- [PG-028 — Spezzare internal/app/composition.go](PG-028_SPEZZARE_INTERNAL_APP_COMPOSITION_GO.md) — `codex/pg-028-composition-split`
- [PG-029 — Spezzare scripts/types.go e rimuovere stub](PG-029_SPEZZARE_SCRIPTS_TYPES_GO_E_RIMUOVERE_STUB.md) — `codex/pg-029-scripts-types`
- [PG-030 — Splittare il mega-package internal/domain/asset](PG-030_SPLITTARE_IL_MEGA_PACKAGE_INTERNAL_DOMAIN_ASSET.md) — `codex/pg-030-domain-asset-split`
- [PG-031 — Ridurre il mega-package internal/app](PG-031_RIDURRE_IL_MEGA_PACKAGE_INTERNAL_APP.md) — `codex/pg-031-app-package-compaction`
- [PG-032 — Ridurre mega-package Artlist application](PG-032_RIDURRE_MEGA_PACKAGE_ARTLIST_APPLICATION.md) — `codex/pg-032-artlist-package`
- [PG-033 — Eliminare interface{} dai wiring principali](PG-033_ELIMINARE_INTERFACE_DAI_WIRING_PRINCIPALI.md) — `codex/pg-033-interface-any`
- [PG-038 — Spezzare YouTube tagutil](PG-038_SPEZZARE_YOUTUBE_TAGUTIL.md) — `codex/pg-038-youtube-tagutil`
- [PG-039 — Spezzare internal/app/lifecycle.go](PG-039_SPEZZARE_INTERNAL_APP_LIFECYCLE_GO.md) — `codex/pg-039-app-lifecycle`
- [PG-040 — Spezzare assets sourcing service](PG-040_SPEZZARE_ASSETS_SOURCING_SERVICE.md) — `codex/pg-040-asset-sourcing`
- [PG-041 — Eliminare stub locali da scripts/flow_helpers.go](PG-041_ELIMINARE_STUB_LOCALI_DA_SCRIPTS_FLOW_HELPERS_GO.md) — `codex/pg-041-script-flow-helpers`
- [PG-042 — Tipizzare scripts/pipeline_usecase.go](PG-042_TIPIZZARE_SCRIPTS_PIPELINE_USECASE_GO.md) — `codex/pg-042-script-pipeline`
- [PG-043 — Spezzare outbox/metadata_export.go](PG-043_SPEZZARE_OUTBOX_METADATA_EXPORT_GO.md) — `codex/pg-043-metadata-export`
- [PG-044 — Ridurre service_orchestrator YouTube](PG-044_RIDURRE_SERVICE_ORCHESTRATOR_YOUTUBE.md) — `codex/pg-044-youtube-orchestrator`
- [PG-045 — Spezzare YouTube metadata service](PG-045_SPEZZARE_YOUTUBE_METADATA_SERVICE.md) — `codex/pg-045-youtube-metadata`
- [PG-047 — Compattare il registry dei comandi admin](PG-047_COMPATTARE_IL_REGISTRY_DEI_COMANDI_ADMIN.md) — `codex/pg-047-admin-registry`

## Fase 6 — Ritirare strumenti e documentazione transitoria

- [PG-025 — Consolidare e poi rimuovere tracker di migrazione obsoleti](PG-025_CONSOLIDARE_E_POI_RIMUOVERE_TRACKER_DI_MIGRAZIONE_OBSOLETI.md) — `codex/pg-025-cleanup-trackers`
- [PG-026 — Ritirare comandi admin di migrazione conclusa](PG-026_RITIRARE_COMANDI_ADMIN_DI_MIGRAZIONE_CONCLUSA.md) — `codex/pg-026-admin-retirement`
- [PG-027 — Rimuovere legacy cognitiva dai commenti di produzione](PG-027_RIMUOVERE_LEGACY_COGNITIVA_DAI_COMMENTI_DI_PRODUZIONE.md) — `codex/pg-027-comment-cleanup`

## Fase 7 — Chiusura definitiva

- [PG-046 — Chiudere Wave 14–17 e promuovere strict nel CI](PG-046_CHIUDERE_WAVE_14_17_E_PROMUOVERE_STRICT_NEL_CI.md) — `codex/pg-046-final-zero-legacy`

## Regola di assegnazione agli agenti

- Un agente riceve un solo ticket alla volta.
- L'agente non sceglie il prossimo ticket.
- Ogni ticket produce una sola PR.
- Una PR non può chiudere parzialmente due ticket.
- Nessun merge senza check verdi o evidenza locale completa più issue CI.

## Definition of Done globale

- `go test ./...`
- `go vet ./...`
- `go build ./...`
- `go run ./scripts/archcheck --strict`
- zero import API→infrastructure;
- zero `database/sql` fuori da infrastructure/database;
- zero baseline/allowlist;
- zero route/capability non implementate;
- zero alias e wrapper di compatibilità;
- un solo writer e un solo registry per stato/capability;
- tracker Wave 14–17 chiusi o rimossi.
