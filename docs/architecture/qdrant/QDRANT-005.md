# QDRANT-005 — health truth, reconciler, observability e disaster recovery

> **Stato:** `BLOCKED / PHASE 1 PARZIALE`  
> **Audit baseline:** `main@e20d5e7fc4afd9f446d9d9e92703db639008b37f` — 26 giugno 2026  
> **Tipo verifica:** audit statico; nessuna esecuzione CI associata all'HEAD.

## OBIETTIVO

Rendere SQLite, outbox e Qdrant verificabili, osservabili, riparabili e recuperabili senza confronti numerici superficiali o procedure manuali non testate.

Il ticket completo comprende:

1. health e readiness veritiere;
2. reconciler ID/version/payload/lifecycle;
3. repair tramite outbox canonico;
4. metriche e alert;
5. cleanup legacy;
6. snapshot, restore e rollback verificati;
7. golden query e filter matrix usati dal gate reindex.

## COMPLETATO NELLA PHASE 1

- esistono `health.QdrantChecker` e `qdrant.HealthProbe`;
- il lifecycle può ricevere un probe Qdrant;
- il probe supporta `X-Api-Key` quando il client contiene la chiave;
- `/health` e `/ready` hanno integrazioni dedicate;
- la diagnostica espone count SQLite, Qdrant e outbox.

## BLOCKER PHASE 1

### 1. API key non propagata

- `buildHealthService` costruisce il checker con API key vuota;
- `BuildProcessBundle` costruisce `qdrant.Config` senza `cfg.Qdrant.APIKey`;
- `cmd/admin reindex-qdrant` omette la stessa API key.

Il supporto header esiste nei client, ma il composition root non gli consegna il valore.

### 2. Readiness accoppiata al ClipIndexer

Client, searcher, collection manager e health probe Qdrant vengono creati soltanto quando:

```go
cfg.Qdrant.Enabled && clipIndexerService.IsEnabled()
```

Qdrant può quindi essere abilitato ma non controllato da readiness quando ClipIndexer è disabilitato.

### 3. Due implementazioni health divergenti

`internal/infrastructure/health/QdrantChecker` e `internal/infrastructure/qdrant/HealthProbe` duplicano HTTP client, timeout, auth e semantica. Il drift sulla API key dimostra che la duplicazione è già pericolosa.

### 4. Diagnostica count-only

`diagIndexHealthAdapter` deduce missing/orphan dalla differenza tra count SQLite e Qdrant. Due set con lo stesso numero di elementi ma ID diversi risultano erroneamente sani.

## FUNZIONALITÀ ANCORA ASSENTI

- reconciler che confronta gli insiemi reali di asset e point;
- verifica canonical point ID;
- confronto workspace, lifecycle, source/index version e versioni embedding per canale;
- rilevazione `status`/`lifecycle_state` drift;
- repair tramite dispatcher/outbox;
- cleanup payload `drive_link`/`local_path`;
- cleanup point orphan o di asset cancellati;
- dead-letter checker obbligatorio nel reindex;
- golden query runner;
- filter matrix runner;
- metriche complete e alert;
- snapshot Qdrant, restore su collection separata e rollback testato;
- retention automatica delle collection precedenti;
- gate QDRANT specifici nella CI.

## QDRANT-005A — HEALTH E READINESS

### Task

- propagare `cfg.Qdrant.APIKey` a checker, client runtime e client admin;
- costruire il client/probe quando `Qdrant.Enabled=true`, indipendentemente dal ClipIndexer;
- consolidare health e readiness su un client/probe comune;
- distinguere capability disabled, misconfigured, unauthorized, unreachable e healthy;
- testare 200, 401, 500, timeout e base URL mancante.

### DoD 005A

- Qdrant abilitato implica sempre un probe reale;
- health e readiness concordano;
- API key viene inviata;
- nessun controllo viene silently skipped.

## QDRANT-005B — RECONCILER E REPAIR

### Confronto minimo

```text
SQLite asset_id
Qdrant payload.asset_id
canonical Qdrant point ID
workspace_id
lifecycle_state/status
deleted_at
source_version
index_version
embedding_version_<channel>
payload minimum
locator legacy
```

### Classificazioni

- missing;
- orphan;
- point ID non canonico;
- payload incompleto;
- workspace mismatch;
- lifecycle mismatch;
- versione stale;
- locator legacy;
- chiave lifecycle legacy.

### Repair

- dry-run di default;
- missing/stale -> evento outbox di reindex;
- orphan/deleted -> evento outbox di delete o cleanup controllato;
- nessuna mutazione Qdrant diretta fuori dai port autorizzati;
- cursor/checkpoint, retry, batch size e idempotenza;
- report JSON machine-readable.

### DoD 005B

- stesso count con ID diversi produce degraded;
- una seconda esecuzione dopo repair produce zero drift;
- repair è idempotente e passa da outbox;
- scan parziale non può cancellare dati.

## QDRANT-005C — OBSERVABILITY E DISASTER RECOVERY

### Metriche minime

- outbox pending/processing/dead-letter/superseded;
- oldest pending age;
- indexing latency/failure;
- points per collection e alias;
- missing/orphan/version/payload/lifecycle mismatch;
- last reconcile, duration e repaired count;
- health/readiness failures per causa;
- alias switch e rollback;
- payload legacy ripuliti.

### Snapshot e restore

- snapshot prima di promozione o cleanup distruttivo;
- registrazione alias target, schema version e snapshot ID;
- restore su collection separata;
- verifica restore con lo stesso verifier del reindex;
- switch soltanto dopo gate verdi;
- rollback automatico/testato verso il target precedente.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione |
|---|---|---|
| checker con API key vuota | core bundle | propagare config |
| runtime/admin client senza API key | process/admin | propagare config |
| probe dipendente dal ClipIndexer | process bundle | separare capability |
| doppio probe HTTP | health + qdrant | consolidare |
| diagnostica count-only | assets adapters | confrontare set reali |
| dead-letter opzionale | verifier/admin | renderla obbligatoria |
| golden/filter placeholder | verifier | runner reali |
| assenza reconciler | application/infra | implementare use case |
| payload locator e chiave lifecycle legacy | collection storiche | cleanup idempotente |
| retention advisory | config/operations | job e stato verificabili |
| snapshot/restore assenti | operations | automazione e test |
| gate solo Markdown | CI | required checks |

## DEFINITION OF DONE COMPLETA

QDRANT-005 può essere marcato `CLOSED` soltanto quando 005A, 005B e 005C sono tutte chiuse e:

- health/readiness usano auth e config reali;
- Qdrant abilitato non può risultare ready senza probe;
- reconciler confronta set, identità, workspace, lifecycle e versioni;
- repair usa il percorso outbox canonico;
- dead-letter, golden query e filter matrix bloccano alias switch;
- metriche e alert coprono drift e backlog;
- cleanup legacy è operativo e idempotente;
- snapshot, restore e rollback sono stati realmente testati;
- la CI esegue tutti i gate.

## GATE MINIMO

```bash
set -euo pipefail

rg -n 'APIKey:\s*cfg\.Qdrant\.APIKey|NewQdrantChecker\([^\n]*cfg\.Qdrant\.APIKey' internal/app cmd/admin
! rg -n 'cfg\.Qdrant\.Enabled\s*&&\s*clipIndexerService\.IsEnabled\(\)' internal/app/build_bundles_process.go
! rg -n 'GoldenQueriesOK:\s*true|FiltersOK:\s*true' internal/infrastructure/qdrant/verifier.go
rg -n 'type .*Reconciler|func .*Reconcile' internal/application internal/infrastructure/qdrant
go test ./internal/application/system/health/... ./internal/infrastructure/health/... ./internal/infrastructure/qdrant/... ./internal/app/... -count=1
```

## NON CHIUDERE SE

- è completa soltanto la Phase 1;
- Qdrant protetto da API key appare unhealthy per wiring incompleto;
- il reconciler confronta soltanto count;
- repair scrive direttamente in Qdrant;
- golden/filter/dead-letter sono opzionali o placeholder;
- restore e rollback esistono soltanto come runbook;
- i gate non sono required checks.
