# ANNULLATO — SUPERATO DA PG-034 (June 2026)

PG-034 ha rimosso integralmente la capability Qdrant. I ticket QDRANT-001..005
sono tombstonati (vedi commit series di questa chiusura). L'audit trail di
ciò che la capability doveva essere è preservato qui sotto come sola
traccia documentale; nessuna implementazione sarà mai prodotta perché
l'obiettivo del ticket è già risolto dall'assenza della capability.

# QDRANT-001 — Ownership, API Gateway e rimozione della legacy diretta

## Stato

APERTO — eseguire per primo.

## Obiettivo

Rendere il backend Go l'unico proprietario delle scritture su SQLite, Qdrant e stato media. Tutti gli script Python, gli agenti IA, VeloxEditing e gli altri servizi devono comunicare esclusivamente tramite API private Go.

La situazione finale deve rispettare questa regola:

```text
SQLite = fonte canonica
Qdrant = indice derivato e ricostruibile
Go DataServer = unico writer e coordinatore
Python/C++/frontend = client HTTP
```

## Problemi attuali da eliminare

- `scripts/tools/sync_drive_qdrant.py` apre direttamente SQLite.
- Lo stesso script invia richieste HTTP direttamente a Qdrant.
- Lo script contiene path assoluti, URL, collection, Drive folder ID e token path hardcoded.
- Lo script carica direttamente il modello `sentence-transformers` e decide autonomamente lo schema degli embedding.
- SQLite e Qdrant vengono aggiornati con due operazioni indipendenti.
- Esistono più writer capaci di creare divergenza tra database e indice.
- I client ricevono o usano path fisici locali che non sono validi su worker remoti.

## Decisioni architetturali già prese

1. Non creare un secondo database o un secondo job system.
2. Riutilizzare il job service, l'outbox e il composition root esistenti.
3. Le API server-to-server devono vivere sotto `/internal/v1` e usare il middleware worker/service authentication già presente.
4. Gli handler API non importano `internal/infrastructure/qdrant` o `database/sql`.
5. Le porte appartengono ad application/domain; gli adapter concreti appartengono a infrastructure.
6. Nessun alias o wrapper permanente per gli accessi Python legacy.
7. Lo script Python può ancora scansionare Drive durante la migrazione, ma non può persistere direttamente.

## Scope consentito

- `internal/domain/asset/**`
- `internal/application/media/**` o package application canonico già esistente per asset/media
- `internal/api/media/**`
- `internal/app/**` per il solo wiring
- `internal/infrastructure/qdrant/**` soltanto per implementare le porte
- `internal/infrastructure/database/**` soltanto per repository e transazioni
- `scripts/tools/sync_drive_qdrant.py`
- test relativi ai package toccati
- documentazione Qdrant canonica

## Fuori scope

- Nuova UI.
- Nuovo database.
- Nuovo sistema di autenticazione.
- Refactor generale di tutti gli asset package.
- Modifica del motore C++.
- Nuovi modelli visual/audio: verranno trattati in QDRANT-003.
- Cleanup distruttivo: verrà trattato in QDRANT-005.

## Contratti da creare o consolidare

Usare nomi già presenti se esistono contratti equivalenti. Non duplicarli.

```go
type MediaIngestionService interface {
    Enqueue(ctx context.Context, cmd IngestMediaCommand) (IngestionReceipt, error)
}

type MediaQueryService interface {
    Get(ctx context.Context, assetID string) (MediaAssetView, error)
}

type MediaRepository interface {
    CreatePending(ctx context.Context, asset MediaAsset) error
    FindByExternalRef(ctx context.Context, provider, externalID string) (*MediaAsset, error)
    Get(ctx context.Context, assetID string) (*MediaAsset, error)
}
```

Qdrant non deve comparire in questi contratti pubblici.

## API private da introdurre

### Creazione ingestione

```http
POST /internal/v1/media/ingestions
Authorization: Bearer <service-token>
Idempotency-Key: <provider>:<external-id>:<source-version>
Content-Type: application/json
```

Payload minimo:

```json
{
  "source": {
    "provider": "google_drive",
    "external_id": "drive-file-id"
  },
  "name": "Video esempio",
  "media_type": "video",
  "metadata": {
    "tags": ["night", "computer"],
    "language": "it"
  },
  "requested_vectors": ["text", "transcript"]
}
```

Risposta:

```json
{
  "ok": true,
  "asset_id": "asset-id",
  "job": {
    "id": "job-id",
    "status": "QUEUED"
  }
}
```

### Lettura asset

```http
GET /internal/v1/media/assets/:asset_id
```

Non restituire credenziali, token, path interni non necessari o configurazione Qdrant.

## Sequenza operativa A–Z

### A. Preparazione

- [ ] Eseguire `git fetch origin`.
- [ ] Lavorare esclusivamente su `main` aggiornato.
- [ ] Verificare `git status -sb` pulito.
- [ ] Leggere gli ultimi cinque commit.
- [ ] Cercare porte, repository e API media già esistenti prima di aggiungere codice.

### B. Inventario dei writer

- [ ] Cercare ogni import Python di `sqlite3`, `qdrant_client` e chiamata HTTP a Qdrant.
- [ ] Cercare tutti i comandi Go o script che aprono direttamente `media.db.sqlite`.
- [ ] Cercare ogni chiamata diretta a `/collections/*/points` fuori dall'adapter Go canonico.
- [ ] Creare nel ticket o nei test una lista verificabile dei writer trovati.

### C. Definizione dell'ownership

- [ ] Identificare il repository SQLite canonico per `media_assets`.
- [ ] Identificare il job service canonico.
- [ ] Identificare il middleware di autenticazione per `/internal/v1`.
- [ ] Definire un solo application service per creare l'ingestione.
- [ ] Non costruire Qdrant o SQLite negli handler.

### D. API handler

- [ ] Creare o estendere il modulo API media canonico.
- [ ] Validare `provider`, `external_id`, `media_type` e `requested_vectors`.
- [ ] Richiedere `Idempotency-Key` per ogni ingestione automatica.
- [ ] Restituire `400` per payload invalido.
- [ ] Restituire `401/403` quando il token service non è valido.
- [ ] Restituire `202 Accepted` per ingestione accodata.
- [ ] Restituire lo stesso asset/job quando la stessa chiave idempotente è ripetuta.

### E. Application service

- [ ] Risolvere o creare l'asset canonico senza duplicati.
- [ ] Salvare soltanto metadata applicativi, non vector raw nel payload API.
- [ ] Creare il job di indicizzazione tramite il job service esistente.
- [ ] Non chiamare Qdrant sincronicamente dall'handler.
- [ ] Non generare embedding nel processo HTTP.

### F. Composition root

- [ ] Costruire repository, service e handler soltanto in `internal/app`.
- [ ] Riutilizzare il registry API esistente.
- [ ] Aggiungere assertion compile-time per le porte.
- [ ] Verificare che API application non importi infrastructure.

### G. Conversione dello script Python

- [ ] Rimuovere `sqlite3`.
- [ ] Rimuovere ogni URL Qdrant.
- [ ] Rimuovere `SentenceTransformer` dallo script di sincronizzazione.
- [ ] Rimuovere path assoluti e Drive folder ID dal sorgente.
- [ ] Leggere URL DataServer, token e folder ID da env/argomenti.
- [ ] Chiamare soltanto `POST /internal/v1/media/ingestions`.
- [ ] Inviare `Idempotency-Key` deterministica.
- [ ] Implementare retry solo per timeout, 429 e 5xx.
- [ ] Non ritentare errori 4xx di validazione.
- [ ] Stampare `asset_id`, `job_id` e request correlation ID.

### H. Rimozione legacy

- [ ] Eliminare funzioni Python di upsert Qdrant.
- [ ] Eliminare query SQL Python.
- [ ] Eliminare vector fake prodotti nello script.
- [ ] Eliminare costanti con path macchina e collection.
- [ ] Eliminare controtest che modificano direttamente dati reali.
- [ ] Non lasciare flag `legacy_mode` o `direct_qdrant`.

### I. Test

- [ ] Test handler senza token.
- [ ] Test handler con token valido.
- [ ] Test payload invalido.
- [ ] Test idempotenza: stessa chiave, stesso asset/job.
- [ ] Test application: nessuna chiamata Qdrant durante HTTP create.
- [ ] Test composition: modulo montato realmente sotto `/internal/v1`.
- [ ] Test script Python con server HTTP finto, senza SQLite/Qdrant locali.
- [ ] Test architetturale che vieta `sqlite3` e Qdrant direct access nello script migrato.

### J. Validazione finale

- [ ] `gofmt` sui file Go.
- [ ] Test mirati dei package media/API/app.
- [ ] `go test ./...`.
- [ ] `go vet ./...`.
- [ ] `go build ./...`.
- [ ] `go run ./scripts/archcheck --ratchet`.
- [ ] Cercare nuovamente writer SQLite/Qdrant non autorizzati.
- [ ] `git diff --check`.
- [ ] Rebase su `origin/main` prima del push.
- [ ] Commit limitato al ticket.
- [ ] `git push origin main`.
- [ ] `git log -n 5 --oneline` e verifica commit remoto.

## Stop conditions

Fermarsi senza inventare workaround se:

- esiste già un API media canonica equivalente;
- il job service non supporta idempotenza e serve prima completare il relativo contratto;
- un altro writer modifica gli stessi file;
- per completare il ticket sarebbe necessario mantenere scritture dirette Python;
- emergono migrazioni schema invasive appartenenti a QDRANT-002.

## Definition of Done

- [ ] Go è l'unico writer di SQLite e Qdrant.
- [ ] Lo script Python è un puro client HTTP.
- [ ] Nessun path, token, collection o Drive ID è hardcoded nello script.
- [ ] L'ingestione restituisce `202` con asset e job.
- [ ] Idempotenza verificata.
- [ ] Nessun handler importa Qdrant o SQL concreto.
- [ ] Nessun compatibility layer diretto resta in produzione.
- [ ] Test, vet, build e gate architetturali passano.

## Dipendenze

Nessuna. Questo ticket deve essere completato prima di QDRANT-002, QDRANT-003, QDRANT-004 e QDRANT-005.
