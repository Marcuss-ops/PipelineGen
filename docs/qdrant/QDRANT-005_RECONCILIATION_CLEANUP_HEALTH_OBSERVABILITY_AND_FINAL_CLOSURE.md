# ANNULLATO — SUPERATO DA PG-034 (June 2026)

# PG-034 (commit cb01c131) È la riconciliazione/cleanup finale: la capability
# Qdrant è stata rimossa invece di essere riconciliata/cleanup. La traccia
# documentale dei ticket originali è preservata in questo file come sola
# audit trace; il "deletion" come atto operativo di chiusura è distribuito
# attraverso i commit di PG-034 stesso + i tombstone QDRANT-00X (questa
# commit series).

# QDRANT-005 — Reconciliation, cleanup sicuro, health reale, osservabilità e chiusura finale

## Stato

FINALE — eseguire soltanto dopo QDRANT-001, QDRANT-002, QDRANT-003 e QDRANT-004.

## Obiettivo

Chiudere definitivamente la migrazione Qdrant con un sistema operativo verificabile. Il backend deve saper misurare e riparare divergenze tra SQLite, outbox, storage e Qdrant senza script distruttivi, numeri inventati o accessi diretti legacy.

Questo ticket è completato soltanto quando:

- SQLite è la fonte canonica;
- Qdrant è ricostruibile;
- health e consistency report usano dati reali;
- cleanup è idempotente, auditable e dry-run-first;
- metriche e alert rilevano drift, retry e dead-letter;
- i test end-to-end provano ingest, search, update, delete, crash e recovery;
- tutta la legacy diretta è rimossa.

## Ordine complessivo dei cinque ticket

```text
QDRANT-001  ownership + API gateway + rimozione writer diretti
     ↓
QDRANT-002  transazione SQLite/outbox + retry + idempotenza
     ↓
QDRANT-003  schema versionato + alias + embedding reali
     ↓
QDRANT-004  search API + hybrid reale + filtri + delivery sicura
     ↓
QDRANT-005  reconciliation + cleanup + health + metriche + certificazione
```

Un agente riceve un solo ticket alla volta. Ticket successivo soltanto dopo commit verificato su `origin/main`.

## Problemi attuali da eliminare

- `IndexHealth` può riportare DB e Qdrant perfettamente allineati usando soltanto `points_count` Qdrant.
- `CleanupStalePoints` usa un pattern N+1 e richiama ogni point dopo lo scroll.
- La pulizia dipende da un validator esterno e cancella punti senza una macchina a stati completa.
- I controtest Python possono modificare file Drive e punti Qdrant reali.
- Non esiste una distinzione netta tra readiness, liveness e consistency.
- Missing, orphan, stale version e invalid payload non sono classificati in modo canonico.
- Non esiste una procedura sicura di repair con dry-run obbligatorio.
- Non esiste una certificazione end-to-end ripetibile in CI/test environment.
- Le metriche operative non coprono l'intero flusso SQLite → outbox → worker → Qdrant.
- Vecchi script, commenti e capability possono continuare a far credere che esistano writer o vector finti supportati.

## Decisioni architetturali

1. Readiness verifica che le dipendenze necessarie siano utilizzabili.
2. Liveness verifica che il processo e i worker siano vivi.
3. Consistency è un report separato e potenzialmente costoso.
4. Il reconciler confronta SQLite e Qdrant; non usa Qdrant come fonte canonica.
5. Ogni repair è idempotente.
6. Dry-run è il default e non modifica nulla.
7. La cancellazione segue stati e tombstone, non delete immediato casuale.
8. Il cleanup fisico, SQLite e Qdrant devono seguire una policy coordinata.
9. I test distruttivi usano collection e database isolati.
10. Nessun report può sostituire dati reali con valori sintetici.

## Classificazioni canoniche del drift

```text
MISSING_IN_QDRANT
ORPHAN_IN_QDRANT
STALE_INDEX_VERSION
STALE_EMBEDDING_VERSION
INVALID_VECTOR_DIMENSION
INVALID_PAYLOAD
MISSING_LOCAL_FILE
MISSING_REMOTE_FILE
DELETE_PENDING_STUCK
INDEX_PENDING_STUCK
OUTBOX_RETRY_EXHAUSTED
DEAD_LETTER_PRESENT
ALIAS_SCHEMA_MISMATCH
```

Ogni issue deve contenere:

```go
type ReconciliationIssue struct {
    Code          IssueCode
    AssetID       string
    PointID       string
    Severity      Severity
    Repairable    bool
    SuggestedAction string
    Details       map[string]string
}
```

Non inserire secret o vector completi nei details.

## API private finali

### Consistency report

```http
GET /internal/v1/media/index/health
```

Risposta esempio:

```json
{
  "ok": false,
  "degraded": true,
  "index_version": "v3",
  "sqlite_assets": 10500,
  "sqlite_indexable": 10120,
  "qdrant_points": 10080,
  "missing_in_qdrant": 40,
  "orphan_in_qdrant": 12,
  "stale_index_version": 8,
  "pending_outbox": 18,
  "dead_letter": 2,
  "checked_at": "RFC3339"
}
```

### Reconciliation job

```http
POST /internal/v1/media/reconcile
```

```json
{
  "dry_run": true,
  "repair": false,
  "scope": {
    "asset_ids": [],
    "workspace_id": "optional-admin-scope"
  }
}
```

### Cleanup job

```http
POST /internal/v1/media/cleanup
```

```json
{
  "dry_run": true,
  "mode": "tombstoned",
  "older_than": "P30D"
}
```

Questi endpoint accodano job. Non eseguono scansioni lunghe nel processo HTTP.

## Scope consentito

- application reconciler/cleanup
- API admin/internal media index
- outbox/job handlers canonici
- Qdrant diagnostics adapter
- SQLite repositories read/write necessari
- storage/Drive existence ports già esistenti
- health service
- metrics/logging
- testcontainer/docker test environment
- rimozione script legacy
- documentazione operativa finale

## Fuori scope

- Nuova UI dashboard.
- Nuovo sistema metriche alternativo.
- Nuovo scheduler se cron/job scheduler canonico esiste già.
- Nuovo database analytics.
- Cancellazione dati production durante test.

## Sequenza operativa A–Z

### A. Preparazione

- [ ] Sincronizzare `main`.
- [ ] Verificare completamento dei quattro ticket precedenti.
- [ ] Verificare alias e collection target attivi.
- [ ] Verificare outbox e job handler operativi.
- [ ] Inventariare cleaner, ghost sweeper, scanner e health report esistenti.
- [ ] Cercare script che modificano direttamente Qdrant/SQLite/Drive.
- [ ] Identificare timer e goroutine esistenti per non duplicare scheduler.

### B. Separare readiness, liveness e consistency

#### Readiness

- [ ] Qdrant `/readyz` risponde.
- [ ] Alias runtime risolve una collection.
- [ ] Collection schema è compatibile.
- [ ] Embedding provider richiesto è disponibile secondo capability policy.
- [ ] SQLite è raggiungibile.
- [ ] Se vector search è opzionale e disabilitato, `applicable=false` esplicito.

#### Liveness

- [ ] Processo server vivo.
- [ ] Worker heartbeat recente.
- [ ] Outbox dispatcher heartbeat recente.
- [ ] Nessuna scansione consistency costosa nel liveness path.

#### Consistency

- [ ] Endpoint separato.
- [ ] Dati reali SQLite, Qdrant, outbox e dead-letter.
- [ ] Timeout dedicato.
- [ ] Possibile risultato degraded senza rendere morto il processo.

### C. Correggere IndexHealth

- [ ] Eliminare assegnazioni sintetiche `DBTotal = QdrantPoints`.
- [ ] Contare asset SQLite reali.
- [ ] Contare asset indicizzabili reali.
- [ ] Contare point Qdrant reali.
- [ ] Contare outbox pending.
- [ ] Contare dead-letter.
- [ ] Contare versioni embedding/index.
- [ ] Distinguere full scan e fast summary.
- [ ] Non restituire delta zero senza confronto.
- [ ] Testare report con divergenza nota.

### D. Reconciler application

- [ ] Definire una porta SQLite per scorrere asset in batch.
- [ ] Definire una porta VectorIndex diagnostics per scorrere point ID e payload minimo.
- [ ] Evitare N+1: scroll deve restituire ID e payload necessari nello stesso batch.
- [ ] Confrontare set per batch o con strategia memory-bounded.
- [ ] Classificare ogni drift con codice canonico.
- [ ] Produrre report persistito con run ID.
- [ ] Supportare resume/cursor.
- [ ] Supportare cancellation.
- [ ] Non riparare durante dry-run.

### E. Rimozione N+1 dal cleaner

- [ ] Modificare lo scroll adapter affinché restituisca payload necessari.
- [ ] Eliminare `getPoint` per ogni ID durante cleanup.
- [ ] Batchizzare verifiche remote quando API provider lo consente.
- [ ] Limitare concurrency.
- [ ] Applicare timeout per batch.
- [ ] Misurare numero richieste per 1.000 asset.
- [ ] Aggiungere test che fallisce se compare N+1 evidente.

### F. Repair planner

- [ ] Separare detection da repair.
- [ ] Creare un piano di azioni ordinato.
- [ ] Ogni azione contiene precondition e expected version.
- [ ] Verificare precondition prima dell'esecuzione.
- [ ] Se il dato cambia tra scan e repair, non applicare azione obsoleta.
- [ ] Persistire outcome per azione.
- [ ] Rendere ogni azione retry-safe.

Mappatura consigliata:

```text
MISSING_IN_QDRANT       → enqueue reindex
ORPHAN_IN_QDRANT        → tombstone/delete point dopo policy
STALE_INDEX_VERSION     → enqueue reindex target
INVALID_PAYLOAD         → rebuild point da SQLite
MISSING_REMOTE_FILE     → mark asset unavailable / delete workflow
DEAD_LETTER_PRESENT     → non auto-riparare senza classificazione
```

### G. Cleanup a stati

Macchina a stati:

```text
ACTIVE
  ↓ richiesta delete
DELETE_PENDING
  ↓ storage/Qdrant cleanup completato
DELETED
```

- [ ] Introdurre tombstone timestamp.
- [ ] Applicare retention prima della cancellazione fisica.
- [ ] Delete Qdrant idempotente.
- [ ] Delete file locale tramite storage port.
- [ ] Delete/trashed remote secondo policy esplicita, non implicita.
- [ ] Aggiornare SQLite dopo outcome verificato.
- [ ] Non cancellare asset ACTIVE perché Drive non risponde temporaneamente.
- [ ] Distinguere `not found` da `permission denied` e `timeout`.

### H. Dry-run obbligatorio

- [ ] Default `dry_run=true`.
- [ ] `repair=true` richiesto per modificare.
- [ ] Per cleanup distruttivo richiedere anche policy/confirmation token se già esiste un sistema simile.
- [ ] Report dry-run contiene azioni previste.
- [ ] Report repair contiene azioni applicate, saltate e fallite.
- [ ] Testare che dry-run non modifichi SQLite, Qdrant o storage.

### I. Scheduling

- [ ] Riutilizzare scheduler/lifecycle esistente.
- [ ] Nessuna goroutine avviata durante composition.
- [ ] Start attraverso lifecycle.
- [ ] Stop context-aware.
- [ ] Una sola istanza leader/lease se esistono più server.
- [ ] Evitare reconciliation simultanee sullo stesso scope.
- [ ] Configurare frequenza e batch size.
- [ ] Possibilità di esecuzione manuale API/job.

### J. Metriche Qdrant client

- [ ] `velox_qdrant_requests_total{operation,status}`.
- [ ] `velox_qdrant_request_duration_seconds{operation}`.
- [ ] `velox_qdrant_errors_total{operation,code}`.
- [ ] `velox_qdrant_upsert_points_total`.
- [ ] `velox_qdrant_delete_points_total`.
- [ ] `velox_qdrant_search_results_total`.
- [ ] `velox_qdrant_retries_total{operation}`.
- [ ] Label cardinalità controllata: non usare asset ID come label.

### K. Metriche indexing/outbox

- [ ] `velox_media_index_pending`.
- [ ] `velox_media_index_failed`.
- [ ] `velox_media_index_dead_letter`.
- [ ] `velox_media_index_duration_seconds`.
- [ ] `velox_media_index_version_count{version}` con versioni limitate.
- [ ] `velox_media_index_missing_points`.
- [ ] `velox_media_index_orphan_points`.
- [ ] `velox_media_index_stale_version`.

### L. Logging strutturato

Ogni operazione deve includere quando applicabile:

```text
request_id
correlation_id
job_id
event_id
reconciliation_run_id
asset_id
workspace_id
index_version
collection_alias
physical_collection
operation
attempt
```

- [ ] Non loggare vector.
- [ ] Non loggare API key o token.
- [ ] Sanitizzare body error Qdrant.
- [ ] Livelli coerenti: info outcome, warn degraded/retry, error terminal failure.

### M. Alerting minimo

- [ ] Qdrant readiness failed.
- [ ] Alias missing/schema mismatch.
- [ ] Outbox pending sopra soglia.
- [ ] Dead-letter > 0.
- [ ] Missing/orphan sopra soglia.
- [ ] Indexing failure rate sopra soglia.
- [ ] Reconciliation job non completato entro finestra.
- [ ] Worker heartbeat stale.
- [ ] Documentare runbook per ogni alert.

### N. Test isolati e controtest sicuri

- [ ] Eliminare test che trashano file Drive reali.
- [ ] Eliminare point ID e Drive ID personali hardcoded.
- [ ] Usare SQLite temporaneo.
- [ ] Usare collection `media_assets_test_<run_id>`.
- [ ] Usare alias test isolato.
- [ ] Usare fake Drive/storage o cartella test dedicata.
- [ ] Cleanup automatico risorse test.
- [ ] Nessun test dipende dalla workstation `/home/pierone/...`.

### O. Test end-to-end obbligatori

#### E2E 1 — Ingestione

- [ ] API ingest restituisce 202.
- [ ] Asset `INDEX_PENDING`.
- [ ] Evento outbox presente.
- [ ] Worker produce embedding reale.
- [ ] Point Qdrant presente.
- [ ] Asset `INDEXED`.

#### E2E 2 — Search

- [ ] API search restituisce asset atteso.
- [ ] Filtri workspace e status applicati.
- [ ] Nessun path locale esposto.
- [ ] Delivery URL autorizzato.

#### E2E 3 — Qdrant outage

- [ ] Fermare Qdrant test.
- [ ] Ingestione non perde asset.
- [ ] Evento resta pending/retry.
- [ ] Riavviare Qdrant.
- [ ] Retry completa indicizzazione.

#### E2E 4 — Worker crash/replay

- [ ] Crash dopo upsert prima dell'ack.
- [ ] Replay evento.
- [ ] Nessun point duplicato.
- [ ] Stato finale coerente.

#### E2E 5 — Update

- [ ] Modificare source version/search text.
- [ ] Creare nuovo evento.
- [ ] Point aggiornato.
- [ ] Versione precedente non riapplicata.

#### E2E 6 — Delete

- [ ] Asset → DELETE_PENDING.
- [ ] Point rimosso.
- [ ] Storage trattato secondo policy.
- [ ] Asset → DELETED.
- [ ] Search non restituisce asset.

#### E2E 7 — Schema migration

- [ ] Creare vNext collection.
- [ ] Reindex.
- [ ] Verify.
- [ ] Alias switch.
- [ ] Smoke search.
- [ ] Rollback alias.

#### E2E 8 — Reconciliation

- [ ] Creare missing point controllato.
- [ ] Creare orphan controllato.
- [ ] Dry-run rileva entrambi senza modifiche.
- [ ] Repair corregge entrambi.
- [ ] Secondo run è clean e idempotente.

### P. Performance baseline

- [ ] Misurare throughput upsert batch.
- [ ] Misurare search P50/P95/P99.
- [ ] Misurare reconciliation per 1k/10k asset.
- [ ] Misurare memoria durante set comparison.
- [ ] Verificare limiti CPU-only.
- [ ] Documentare batch size e concurrency consigliati.
- [ ] Nessun requisito numerico inventato: salvare baseline osservata.

### Q. Security review

- [ ] Tutti gli endpoint sotto auth service/admin.
- [ ] Workspace enforcement.
- [ ] API key Qdrant soltanto in secret/env.
- [ ] Qdrant non esposto pubblicamente se non necessario.
- [ ] Network policy/firewall documentata.
- [ ] URL delivery con autorizzazione.
- [ ] Nessun path assoluto o credenziale nei repository.
- [ ] Error response non espone internals.
- [ ] Audit log per repair e cleanup.

### R. Backup e rollback

- [ ] Backup SQLite prima di repair massivo.
- [ ] Snapshot Qdrant prima di alias migration distruttiva.
- [ ] Conservare old collection per retention.
- [ ] Documentare rollback alias.
- [ ] Documentare restore SQLite/outbox.
- [ ] Testare almeno un restore in ambiente isolato.
- [ ] Non dichiarare backup valido senza prova di restore.

### S. Rimozione legacy definitiva

- [ ] Eliminare `scripts/tools/sync_drive_qdrant.py` se completamente sostituito, oppure mantenerlo solo come client API con nome/documentazione corretti.
- [ ] Eliminare `run_countertests.py` distruttivo o riscriverlo su ambiente isolato/API.
- [ ] Eliminare import Python `sqlite3` per il flusso media.
- [ ] Eliminare chiamate Python dirette a Qdrant.
- [ ] Eliminare path, token, collection e Drive ID hardcoded.
- [ ] Eliminare vector mock/sintetici.
- [ ] Eliminare `IndexHealth` sintetico.
- [ ] Eliminare cleaner N+1.
- [ ] Eliminare route duplicate di search/cleanup.
- [ ] Eliminare config Qdrant non utilizzata.
- [ ] Eliminare commenti che descrivono capability non vere.
- [ ] Aggiungere gate CI che impedisce la ricomparsa dei writer diretti.

### T. Documentazione operativa

Creare un solo documento runbook canonico con:

- [ ] architettura e ownership;
- [ ] env/config richieste;
- [ ] avvio Qdrant;
- [ ] collection alias attivo;
- [ ] esecuzione ingest;
- [ ] search curl;
- [ ] report consistency;
- [ ] dry-run reconciliation;
- [ ] repair;
- [ ] reindex e alias switch;
- [ ] rollback;
- [ ] backup/restore;
- [ ] alert troubleshooting.

Non lasciare documenti concorrenti con istruzioni incompatibili.

### U. Gate architetturali

Aggiungere controlli che falliscono se:

- [ ] script Python importa `sqlite3` nel flusso media;
- [ ] script Python contiene `/collections/` Qdrant;
- [ ] API handler importa infrastructure/qdrant;
- [ ] application importa database/sql;
- [ ] compare `generate_normalized_vector` per visual/audio production;
- [ ] compaiono path assoluti workstation;
- [ ] compare una seconda implementazione registry/vector index;
- [ ] compare una route legacy vietata.

### V. Validazione finale

- [ ] `gofmt`.
- [ ] Test mirati reconciler/cleanup/health.
- [ ] Tutti gli E2E isolati.
- [ ] Test restore.
- [ ] Performance baseline.
- [ ] Security review completata.
- [ ] `go test ./...`.
- [ ] `go vet ./...`.
- [ ] `go build ./...`.
- [ ] `go run ./scripts/archcheck --ratchet`.
- [ ] Vero strict quando disponibile.
- [ ] `git diff --check`.
- [ ] Rebase su `origin/main`.
- [ ] Commit limitato al ticket.
- [ ] `git push origin main`.
- [ ] `git log -n 5 --oneline`.
- [ ] Verificare commit remoto e CI/check disponibili.

## Stop conditions

Fermarsi se:

- uno dei ticket precedenti non è realmente completato;
- dry-run modifica dati;
- il reconciler considera Qdrant fonte canonica;
- repair cancella dati senza tombstone/retention;
- i test usano risorse production;
- il report consistency usa conteggi sintetici;
- si propone un secondo scheduler, outbox, registry o client Qdrant;
- un altro agente modifica gli stessi package.

## Definition of Done finale

- [ ] Readiness, liveness e consistency sono separati.
- [ ] IndexHealth usa dati reali.
- [ ] Reconciler rileva missing, orphan, stale version e invalid payload.
- [ ] Dry-run non modifica nulla.
- [ ] Repair è idempotente e auditable.
- [ ] Cleanup usa stati, tombstone e retention.
- [ ] Nessun N+1 durante scroll/cleanup.
- [ ] Metriche e alert coprono l'intero flusso.
- [ ] Test E2E coprono outage, crash, replay, update, delete e migration.
- [ ] Backup e restore sono provati.
- [ ] Nessun writer Python diretto resta.
- [ ] Nessun vector fake resta.
- [ ] Nessuna route o config legacy resta.
- [ ] Qdrant è completamente ricostruibile da SQLite/outbox/storage.
- [ ] Tutti i test, vet, build e gate passano.

## Chiusura del programma Qdrant

Il programma è chiuso soltanto quando tutti e cinque i file risultano completati e verificati sul `main` corrente:

- [ ] QDRANT-001 completato.
- [ ] QDRANT-002 completato.
- [ ] QDRANT-003 completato.
- [ ] QDRANT-004 completato.
- [ ] QDRANT-005 completato.

Non marcare il programma concluso sulla base della sola presenza delle API o del solo funzionamento di una query manuale.
