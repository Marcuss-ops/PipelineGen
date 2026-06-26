# QDRANT-005 — health truth, reconciler, observability e disaster recovery

> **Stato:** `BLOCKED / PHASE 1 PARZIALE`  
> **Audit baseline:** `main@c72949a362656f05222f333adf67b1b0eee973ae` — 26 giugno 2026  
> **Owner suggerito:** Qdrant operations + health/readiness + outbox reconciliation  
> **Branch suggeriti:**
> - `codex/qdrant-005a-health-readiness`
> - `codex/qdrant-005b-reconciler`
> - `codex/qdrant-005c-observability-dr`

## OBIETTIVO

Rendere SQLite, outbox e Qdrant verificabili e riparabili senza affidarsi a confronti numerici superficiali o procedure manuali.

Il ticket completo comprende:

1. health e readiness veritiere;
2. reconciler batch ID/version/payload;
3. repair attraverso il percorso canonico outbox;
4. metriche e alert operativi;
5. snapshot, restore e rollback verificati;
6. pulizia dei payload/point legacy;
7. smoke golden-query e filter matrix usati dal reindex gate.

## STATO REALE

### Completato nella Phase 1

- esiste `internal/infrastructure/health/QdrantChecker` per `/health?check=qdrant`;
- esiste `internal/infrastructure/qdrant/HealthProbe` per la readiness barrier;
- `LifecycleManager` espone `AddProbe` come metodo tipizzato;
- `cmd/server/main.go` registra il probe Qdrant quando presente;
- i probe usano client HTTP dedicati con timeout;
- `HealthProbe` supporta `X-Api-Key` quando il client Qdrant contiene una chiave;
- la diagnostica index-health espone alcuni count SQLite/outbox/Qdrant.

### Blocker Phase 1

1. **API key non propagata al health checker.** `buildHealthService` costruisce `NewQdrantChecker(cfg.Qdrant.BaseURL, "", true)`.
2. **API key non propagata al client readiness.** `BuildProcessBundle` costruisce `qdrant.Config` con BaseURL e Timeout, ma non copia `cfg.Qdrant.APIKey`.
3. **Probe dipendente dal ClipIndexer.** Qdrant client e probe vengono creati soltanto con `cfg.Qdrant.Enabled && clipIndexerService.IsEnabled()`. Se Qdrant è abilitato ma ClipIndexer no, la readiness può non controllarlo.
4. **Health e readiness duplicano client/probe.** Esistono due implementazioni HTTP separate; il rischio di drift su auth, timeout e semantica è già visibile.
5. **Nessuna prova CI remota verde sull'HEAD auditato.** I gate operativi restano da rendere obbligatori.

### Funzionalità ancora assenti

- reconciler che confronta set reali di asset/point;
- confronto `asset_id`, canonical point ID, `source_version`, `index_version`, embedding versions e lifecycle;
- repair attraverso dispatcher/outbox;
- dead-letter integration nel reindex verifier;
- golden query runner;
- filter matrix runner;
- cleanup dei payload legacy `drive_link`/`local_path`;
- stale-link/deleted-point cleaner completo;
- metriche complete e alert;
- backup/snapshot, restore e test di rollback;
- retention automatica e verificabile delle collection precedenti.

## SCOMPOSIZIONE CONSIGLIATA

## QDRANT-005A — Health e readiness veritiere

### Task

- propagare `cfg.Qdrant.APIKey` a health checker e `qdrant.Client`;
- costruire il probe quando `Qdrant.Enabled=true`, indipendentemente dal ClipIndexer;
- avere una sola implementazione canonica del probe HTTP o un adapter condiviso;
- distinguere chiaramente:
  - capability disabilitata -> `applicable=false`;
  - capability abilitata ma non configurata -> unhealthy;
  - API key errata -> unhealthy con errore osservabile;
  - Qdrant irraggiungibile -> readiness 503;
- aggiungere test con Qdrant fake per 200, 401, 500, timeout e base URL mancante.

### Definition of Done 005A

- `qdrant.enabled=true` implica sempre un probe readiness reale;
- l'API key configurata viene inviata da health e readiness;
- `/health?check=qdrant` e `/ready` concordano sullo stato;
- nessun probe è silently skipped per dipendenze non correlate.

## QDRANT-005B — Reconciler e repair canonico

### Task

Creare un use case batch che legga SQLite come authority e scorra Qdrant per confrontare:

```text
SQLite asset ID
canonical Qdrant point ID
workspace_id
lifecycle_state / deleted_at
source_version
index_version
embedding_version_<channel>
payload minimum
```

Classificazioni minime:

- missing in Qdrant;
- orphan in Qdrant;
- point ID non canonico;
- payload incompleto;
- versione stale;
- workspace mismatch;
- lifecycle mismatch;
- locator legacy presente.

Repair richiesto:

- missing/stale -> evento outbox canonico di reindex;
- deleted/orphan -> evento outbox canonico di delete o cleanup controllato;
- nessuna scrittura diretta che bypassa dispatcher/outbox;
- dry-run obbligatorio di default;
- batch size, cursor/checkpoint, retry e idempotenza;
- report JSON machine-readable.

### Definition of Done 005B

- due insiemi con lo stesso count ma ID differenti risultano degraded;
- repair è idempotente;
- una seconda esecuzione dopo repair produce zero drift;
- workspace e versioni fanno parte del confronto;
- il reconciler non modifica Qdrant direttamente fuori dai port autorizzati.

## QDRANT-005C — Observability, cleanup e disaster recovery

### Metriche minime

- outbox pending/processing/dead-letter/superseded;
- oldest pending age;
- indexing latency e failure rate;
- Qdrant points per collection/alias;
- missing/orphan/version mismatch/payload issue;
- last successful reconcile timestamp;
- reconcile duration e repaired count;
- health/readiness failures per causa;
- alias switch success/failure;
- stale locator cleanup count.

### Backup e restore

- creare snapshot della collection fisica prima di promozione o cleanup distruttivo;
- registrare alias target, schema version e snapshot ID;
- documentare e automatizzare restore su collection separata;
- validare restore con lo stesso verifier del reindex;
- switch alias soltanto dopo verifica;
- testare rollback verso collection precedente.

### Cleanup

- eliminare payload legacy `drive_link`/`local_path`;
- rimuovere point per asset soft-deleted/trashed;
- non cancellare point non verificati quando lo scroll è parziale;
- produrre audit log e metriche.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione richiesta |
|---|---|---|
| health checker con API key vuota | `internal/app/build_bundles_core.go` | propagare config reale |
| client Qdrant readiness senza API key | `internal/app/build_bundles_process.go` | propagare `cfg.Qdrant.APIKey` |
| probe creato solo se ClipIndexer è enabled | process bundle | separare capability Qdrant da ClipIndexer |
| due probe HTTP che possono divergere | `internal/infrastructure/health` e `internal/infrastructure/qdrant` | consolidare o condividere client/contract |
| diagnostica basata principalmente sui count | index health adapter/report | confrontare ID, versioni e payload |
| dead-letter checker `nil` nel reindex | `cmd/admin/reindex_qdrant.go` | iniettare port reale |
| golden/filter flag placeholder | `internal/infrastructure/qdrant/verifier.go` | runner reali |
| payload legacy con locator | collection storiche | reconciler/cleanup |
| retention collection soltanto advisory | config/runbook | stato persistito e job verificabile |
| snapshot/restore manuali o assenti | operations | automazione e test restore |
| gate descritti ma non richiesti dalla CI | CI | promuovere a required checks |

## DEFINITION OF DONE COMPLETA

QDRANT-005 può essere marcato `CLOSED` soltanto quando **005A, 005B e 005C sono tutte chiuse** e:

- health/readiness usano configurazione e autenticazione reali;
- Qdrant abilitato non può essere dichiarato ready senza probe;
- reconciler confronta set, identità, workspace, lifecycle e versioni;
- repair passa dal percorso outbox canonico;
- dead-letter, golden query e filter matrix bloccano alias switch;
- metriche e alert coprono drift e backlog;
- cleanup dei locator legacy è operativo;
- snapshot, restore e rollback sono stati eseguiti in test;
- una procedura automatizzata dimostra recupero completo da collection corrotta o alias errato;
- i gate sono eseguiti dalla CI e non soltanto documentati.

## GATE ANTI-REGRESSIONE

```bash
set -euo pipefail

# La API key deve essere propagata ai due percorsi.
rg -n 'APIKey:\s*cfg\.Qdrant\.APIKey|NewQdrantChecker\([^\n]*cfg\.Qdrant\.APIKey' \
  internal/app

# Il probe non deve dipendere dall'abilitazione del ClipIndexer.
! rg -n 'cfg\.Qdrant\.Enabled\s*&&\s*clipIndexerService\.IsEnabled\(\)' \
  internal/app/build_bundles_process.go

# Nessun placeholder positivo nel verifier.
! rg -n 'GoldenQueriesOK:\s*true|FiltersOK:\s*true' \
  internal/infrastructure/qdrant/verifier.go

# Reconciler e report devono esistere e avere test.
rg -n 'type .*Reconciler|func .*Reconcile' internal/application internal/infrastructure/qdrant
rg -n 'MissingCount|OrphanCount|VersionMismatch|PayloadIssues' \
  internal/application internal/infrastructure/qdrant

# Test health/readiness/reconcile/restore.
go test ./internal/application/system/health/... \
  ./internal/infrastructure/health/... \
  ./internal/infrastructure/qdrant/... \
  ./internal/app/... \
  -count=1
```

## TEST MINIMI DA AGGIUNGERE

- health/readiness con API key corretta e errata;
- Qdrant enabled + ClipIndexer disabled;
- timeout e HTTP non-200;
- stesso count ma ID set differenti;
- point ID non canonico;
- workspace/lifecycle/version mismatch;
- dry-run reconciler senza mutazioni;
- repair idempotente tramite outbox;
- dead-letter > 0 blocca reindex;
- golden query e filter smoke falliti;
- payload locator legacy rilevato e rimosso;
- snapshot creato, restore verificato e alias rollback riuscito.

## NON CHIUDERE SE

- viene dichiarata chiusa soltanto la Phase 1;
- health funziona senza API key mentre il Qdrant reale la richiede;
- il reconciler confronta solo i count;
- il repair scrive direttamente in Qdrant bypassando outbox;
- golden query, filter smoke o dead-letter sono placeholder;
- restore e rollback esistono solo come runbook non testato;
- i gate non sono required checks della CI.
