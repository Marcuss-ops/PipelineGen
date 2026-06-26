# ANNULLATO — SUPERATO DA PG-034 (June 2026)

PG-034 ha rimosso integralmente la capability Qdrant. I ticket QDRANT-001..005
sono tombstonati (vedi commit series di questa chiusura). L'audit trail di
ciò che la capability doveva essere è preservato qui sotto come sola
traccia documentale; nessuna implementazione sarà mai prodotta perché
l'obiettivo del ticket è già risolto dall'assenza della capability.

# QDRANT-002 — Ingestione transazionale, outbox, retry e idempotenza

## Stato

BLOCCATO da QDRANT-001.

## Obiettivo

Eliminare la doppia scrittura non transazionale tra SQLite e Qdrant. SQLite deve registrare l'asset e l'evento di indicizzazione nella stessa transazione; un worker deve consumare l'evento e aggiornare Qdrant in modo idempotente.

Flusso finale:

```text
API ingest
   ↓
transazione SQLite
   ├─ media_assets = INDEX_PENDING
   └─ outbox_events = media.index.requested
   ↓ commit
worker outbox
   ↓
embedding + upsert Qdrant
   ↓
transazione SQLite
   ├─ media_assets = INDEXED
   └─ outbox event = DELIVERED
```

Se Qdrant è indisponibile, SQLite conserva l'asset e l'evento viene ritentato. Nessun dato viene perso e nessun client deve risincronizzare manualmente l'intero archivio.

## Problemi attuali da eliminare

- Upsert Qdrant e `INSERT OR REPLACE` SQLite avvengono separatamente.
- Lo stato dell'asset non rappresenta chiaramente `pending`, `indexed`, `failed`, `deleting`.
- Retry, timeout e dead-letter non sono governati da un solo contratto.
- Il payload dell'evento può diventare un dump instabile di dati.
- La stessa sorgente può creare asset o point duplicati.
- Delete e update non seguono lo stesso percorso affidabile dell'ingestione.
- La riconciliazione oggi può essere usata come sostituto della consistenza transazionale.

## Decisioni architetturali

1. SQLite è la fonte canonica.
2. Qdrant è eventualmente consistente e completamente ricostruibile.
3. Riutilizzare l'outbox canonica del repository; non crearne una seconda.
4. Riutilizzare il job service e il dispatcher esistenti.
5. L'evento contiene riferimenti e versioni, non vector completi.
6. Ogni operazione è idempotente.
7. I retry hanno backoff, jitter e limite; gli errori permanenti vanno in dead-letter.
8. Update e delete usano eventi specifici, non chiamate laterali dirette.
9. Nessun handler HTTP attende Qdrant.

## Eventi canonici

Usare nomi già presenti se equivalenti.

```text
media.index.requested
media.index.delete_requested
media.index.rebuild_requested
media.index.completed
media.index.failed
```

Payload versione 1:

```json
{
  "schema_version": 1,
  "event_id": "uuid",
  "asset_id": "asset-id",
  "operation": "UPSERT",
  "source_version": "drive-etag-or-hash",
  "target_index_version": "v3",
  "requested_vectors": ["text", "transcript"],
  "requested_at": "RFC3339"
}
```

Non inserire nel payload:

- embedding completi;
- token;
- path segreti;
- configurazione Qdrant;
- client-specific fields non necessari.

## Stati asset canonici

```text
DISCOVERED
INDEX_PENDING
INDEXING
INDEXED
INDEX_FAILED
DELETE_PENDING
DELETED
```

Transizioni valide:

```text
DISCOVERED → INDEX_PENDING
INDEX_PENDING → INDEXING
INDEXING → INDEXED
INDEXING → INDEX_FAILED
INDEX_FAILED → INDEX_PENDING
INDEXED → INDEX_PENDING      aggiornamento sorgente
INDEXED → DELETE_PENDING
DELETE_PENDING → DELETED
```

Vietare transizioni arbitrarie e stringhe alternative come `ready`, `done`, `processed`, `uploaded` per rappresentare lo stesso stato di indicizzazione.

## Scope consentito

- repository e migration per `media_assets` e outbox
- application media/indexing
- worker/dispatcher canonico
- adapter Qdrant per upsert/delete
- composition root
- test unitari, integration e migration
- metriche outbox/indexing

## Fuori scope

- Nuovo modello embedding.
- Nuovo schema collection completo: QDRANT-003.
- Nuova API search: QDRANT-004.
- Cleanup e reconciler completo: QDRANT-005.
- Refactor generale del job system.

## Sequenza operativa A–Z

### A. Preparazione

- [ ] Sincronizzare `main` e verificare working tree pulita.
- [ ] Confermare che QDRANT-001 sia completato.
- [ ] Cercare outbox, dispatcher, job registry e repository media esistenti.
- [ ] Non duplicare tabelle o package già canonici.

### B. Audit schema corrente

- [ ] Inventariare colonne `media_assets` relative a status, embedding e versioni.
- [ ] Inventariare tabella outbox e dead-letter esistente.
- [ ] Verificare indici unici su asset ID, source provider ed external ID.
- [ ] Identificare eventuali `INSERT OR REPLACE` pericolosi.
- [ ] Identificare codice che aggiorna SQLite dopo Qdrant senza transazione.

### C. Migrazione database

- [ ] Aggiungere soltanto colonne mancanti.
- [ ] Introdurre `index_state` con valori canonici.
- [ ] Introdurre `index_version` e `indexed_at` se assenti.
- [ ] Introdurre `source_version` o content hash se assente.
- [ ] Introdurre `index_error_code` e messaggio sanitizzato se necessari.
- [ ] Creare unique constraint su `(source_provider, source_external_id)`.
- [ ] Non usare `INSERT OR REPLACE`: usare UPSERT esplicito che non distrugga metadata non inclusi.
- [ ] Scrivere migration forward-only e testarla su DB vuoto e DB esistente.

### D. Repository transazionale

- [ ] Definire un metodo application-oriented per creare asset ed evento insieme.
- [ ] Implementare la transazione sotto `internal/infrastructure/database`.
- [ ] Inserire/aggiornare asset in `INDEX_PENDING`.
- [ ] Inserire evento outbox nella stessa transazione.
- [ ] Commit soltanto quando entrambe le scritture riescono.
- [ ] Rollback completo in caso di errore.
- [ ] Non esporre `*sql.DB` all'application.

Esempio di porta:

```go
type MediaIndexCommandStore interface {
    CreateAssetAndRequestIndex(ctx context.Context, asset MediaAsset, event IndexRequested) error
    MarkIndexing(ctx context.Context, assetID, eventID string) error
    MarkIndexed(ctx context.Context, result IndexResult) error
    MarkIndexFailed(ctx context.Context, failure IndexFailure) error
}
```

### E. Idempotenza ingest

- [ ] Usare `Idempotency-Key` come chiave request canonica.
- [ ] Persistire chiave, request hash e risorsa risultante.
- [ ] Stessa chiave e stesso hash: restituire la risorsa esistente.
- [ ] Stessa chiave e hash diverso: restituire `409 Conflict`.
- [ ] Non creare nuovi job duplicati se esiste già un evento attivo equivalente.
- [ ] Rendere deterministico il point ID a partire dall'asset ID canonico, non dal path locale.

### F. Worker outbox

- [ ] Registrare un solo handler per `media.index.requested`.
- [ ] Caricare l'asset corrente da SQLite usando `asset_id`.
- [ ] Verificare che `source_version` dell'evento sia ancora corrente.
- [ ] Se l'evento è obsoleto, marcarlo `SUPERSEDED` senza indicizzare dati vecchi.
- [ ] Passare asset a embedding/index pipeline.
- [ ] Upsert Qdrant con point ID deterministico.
- [ ] Aggiornare asset a `INDEXED` e outbox a delivered in una transazione applicativa coerente.
- [ ] Rendere sicura la ripetizione dello stesso evento.

### G. Retry e classificazione errori

- [ ] Definire errori retryable: timeout, reset, 429, 502, 503, 504.
- [ ] Definire errori permanenti: vector dimension errata, schema incompatibile, payload invalido, asset inesistente.
- [ ] Usare exponential backoff con jitter.
- [ ] Stabilire massimo tentativi dal registry/config canonico.
- [ ] Registrare `next_attempt_at`.
- [ ] Dopo il massimo tentativi, spostare in dead-letter.
- [ ] Non perdere l'asset: marcarlo `INDEX_FAILED`.
- [ ] Non includere secret nei messaggi errore.

### H. Update e delete

- [ ] Ogni modifica che cambia search text o metadata indicizzati crea un nuovo `media.index.requested`.
- [ ] Ogni delete crea `media.index.delete_requested`.
- [ ] Il worker delete rimuove il point Qdrant in modo idempotente.
- [ ] Se il point è già assente, considerare l'operazione completata.
- [ ] Aggiornare l'asset a `DELETED` soltanto dopo la policy stabilita.
- [ ] Non cancellare file fisici in questo ticket.

### I. Versionamento eventi

- [ ] Richiedere `schema_version`.
- [ ] Implementare decoder esplicito per versione 1.
- [ ] Rifiutare versioni sconosciute con errore permanente.
- [ ] Aggiungere test di backward compatibility soltanto per versioni realmente supportate.
- [ ] Non usare `map[string]any` come contratto interno definitivo.

### J. Concorrenza

- [ ] Impedire due indicizzazioni simultanee dello stesso asset/versione.
- [ ] Usare lease/claim già supportato dall'outbox o job system.
- [ ] Rendere il claim atomico.
- [ ] Gestire lease scaduti dopo crash del worker.
- [ ] Non usare mutex in-memory come unica protezione distribuita.

### K. Test unitari

- [ ] Asset + outbox commit insieme.
- [ ] Rollback quando outbox insert fallisce.
- [ ] Idempotency replay.
- [ ] Idempotency conflict.
- [ ] Evento obsoleto/superseded.
- [ ] Retryable error.
- [ ] Permanent error.
- [ ] Dead-letter dopo limite.
- [ ] Delete già eseguito.

### L. Test integrazione

- [ ] SQLite reale temporaneo con migration complete.
- [ ] Qdrant container reale o testcontainer.
- [ ] Creazione ingest → evento → worker → point presente → asset `INDEXED`.
- [ ] Qdrant fermo → evento pending/retry → nessuna perdita asset.
- [ ] Riavvio Qdrant → retry → completamento.
- [ ] Worker crash dopo upsert ma prima dell'ack → replay senza duplicati.
- [ ] Update source version → evento vecchio ignorato.

### M. Metriche e log

- [ ] `media_index_outbox_pending`.
- [ ] `media_index_outbox_dead_letter`.
- [ ] `media_index_attempts_total`.
- [ ] `media_index_failures_total{code}`.
- [ ] `media_index_duration_seconds`.
- [ ] Log con `event_id`, `job_id`, `asset_id`, `source_version`, `index_version`.
- [ ] Nessun embedding completo nei log.

### N. Rimozione legacy

- [ ] Eliminare dual write sincrono.
- [ ] Eliminare update Qdrant da handler API.
- [ ] Eliminare SQL diretto da script.
- [ ] Eliminare funzioni ad hoc di retry fuori dal worker canonico.
- [ ] Eliminare status duplicati equivalenti.
- [ ] Eliminare job/event type non più consumati.
- [ ] Non mantenere percorsi `sync_now` che bypassano outbox.

### O. Validazione finale

- [ ] `gofmt`.
- [ ] Test repository/outbox/media mirati.
- [ ] Test migration.
- [ ] Test integrazione Qdrant reale.
- [ ] `go test ./...`.
- [ ] `go vet ./...`.
- [ ] `go build ./...`.
- [ ] archcheck ratchet.
- [ ] `git diff --check`.
- [ ] Rebase su `origin/main`.
- [ ] Commit unico e mirato.
- [ ] Push diretto su `main`.
- [ ] Verifica ultimi cinque commit.

## Stop conditions

Fermarsi se:

- emerge una seconda outbox già usata per media;
- il job system non offre claim/lease affidabile;
- servirebbe memorizzare embedding completi nell'evento;
- una migration distruttiva richiede downtime non pianificato;
- un altro agente modifica gli stessi repository o migration.

## Definition of Done

- [ ] Asset e evento outbox vengono persistiti atomicamente.
- [ ] Qdrant può essere indisponibile senza perdita dati.
- [ ] Retry e dead-letter sono testati.
- [ ] Upsert e delete sono idempotenti.
- [ ] Nessun dual write sincrono resta.
- [ ] Stati asset canonici e verificati.
- [ ] Eventi versionati.
- [ ] Worker replay-safe.
- [ ] Test unitari, integrazione, vet e build passano.

## Dipendenze

- Richiede QDRANT-001 completato.
- Deve precedere QDRANT-003, QDRANT-004 e QDRANT-005.
