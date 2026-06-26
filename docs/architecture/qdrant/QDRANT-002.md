# QDRANT-002 — outbox atomico, writer unificati e route internal reali

> **Stato:** `BLOCKED / DA RIAPRIRE`  
> **Audit baseline:** `main@c72949a362656f05222f333adf67b1b0eee973ae` — 26 giugno 2026  
> **Owner suggerito:** composition root + API routing + asset ingestion  
> **Branch suggerito:** `codex/qdrant-002-outbox-cutover`

## OBIETTIVO

Garantire che ogni mutazione indicizzabile di `media_assets` avvenga nello stesso confine transazionale che crea l'evento outbox, e che gli endpoint operativi outbox siano montati esclusivamente su `/internal/v1/*` dietro `WorkerAuth`.

Il ticket non è chiuso quando il dispatcher esiste: è chiuso quando nessun writer legacy è più eseguibile e il percorso HTTP production è realmente raggiungibile.

## STATO REALE

### Completato

- esiste un `outbox.Dispatcher` canonico per UPSERT/DELETE + evento outbox;
- `UpdateClip` rifiuta la richiesta quando il dispatcher non è cablato, invece di scrivere direttamente;
- outbox e mediasearch non vengono più registrati nel module registry pubblico `/api`;
- `RegistryWiring` e `AppDeps` trasportano handler tipizzati per outbox e mediasearch;
- esiste un test che vieta route con prefisso `/api/internal/*`.

### Blocker attuali

1. **Mount production eseguito troppo tardi.** `NewServerWithHealth` chiama `Router.Setup()` durante la costruzione; `cmd/server/main.go` invoca `SetOutboxHandler` e `SetMediasearchHandler` soltanto dopo. I setter modificano il `Router`, ma il motore Gin è già stato costruito: le route canoniche possono restare 404.
2. **Il test non percorre il wiring production.** Il test corrente imposta gli handler prima di `Setup()`, quindi non intercetta l'ordine reale usato da `cmd/server`.
3. **Writer diretti ancora eseguibili.** Restano fallback raw almeno in:
   - `internal/application/assets/providers/artlist/search_core.go`
   - `internal/application/images/google_vids_assets.go::RegisterVideoAsset`
   - `internal/application/images/google_vids_assets.go::registerAudioClip`
4. **Gate globale incompleto.** Non esiste ancora una scansione CI che dimostri l'assenza di tutti i writer `media_assets` fuori dal dispatcher/transaction manager autorizzato.
5. **Runbook legacy.** Documentazione e smoke script possono ancora riferirsi a `/api/internal/v1/*`.

## TASK DI HANDOFF

### A. Correggere il wiring delle route prima di `Setup()`

Scegliere una sola soluzione canonica:

- estendere `NewServerWithHealth` con `OutboxHandler` e `MediasearchHandler`; oppure
- costruire/configurare il `Router` nel composition root, impostare tutti gli handler, poi chiamare `Setup()` una volta sola.

Vincoli:

- vietati setter post-`Setup()` per route;
- `/internal/v1/outbox/status`, `/internal/v1/outbox/events` e `/internal/v1/media/search` devono comparire in `server.GetRouter().Routes()` nel percorso production;
- `/api/internal/v1/*` deve restare assente;
- le route devono ereditare `WorkerAuth`.

### B. Aggiungere un test sul percorso production

Il test deve costruire il server tramite lo stesso costruttore usato da `cmd/server/main.go`, iniettare stub tipizzati e verificare:

```text
GET  /internal/v1/outbox/status
GET  /internal/v1/outbox/events
POST /internal/v1/media/search
```

Il test deve fallire con l'ordine attuale post-`Setup()` e passare soltanto dopo il cutover.

### C. Eliminare tutti i writer senza outbox

Rimuovere ogni forma:

```go
if dispatcher != nil {
    dispatcher.EnqueueAndIndex(...)
} else {
    repo.Upsert(...)
}
```

Comportamento richiesto quando il dispatcher è obbligatorio ma assente:

- errore di startup nel composition root, preferito; oppure
- errore esplicito della capability senza alcuna mutazione DB.

Copertura minima:

- aggiornamento clip;
- Artlist live save;
- registrazione video generato;
- registrazione audio derivato;
- sourcing/import;
- delete e lifecycle transitions che richiedono sincronizzazione Qdrant.

### D. Introdurre un gate writer-ownership

Il gate deve distinguere:

- repository SQL autorizzati che implementano il dispatcher;
- use case/handler vietati che chiamano direttamente `Upsert`, `UpsertClip`, `SetIndexState` o DELETE indicizzabili.

Preferire un controllo Go/AST in `cmd/archcheck` rispetto a un grep fragile.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione richiesta |
|---|---|---|
| setter route chiamati dopo `Router.Setup()` | `cmd/server/main.go`, `internal/api/server.go` | spostare gli handler nel costruttore/configurazione pre-Setup |
| fallback `assetStore.Upsert` senza outbox | `internal/application/assets/providers/artlist/search_core.go` | fail-closed e dispatcher obbligatorio |
| fallback `stockRepo.Upsert` / `UpsertClip` | `internal/application/images/google_vids_assets.go` | fail-closed e dispatcher obbligatorio |
| test che configura direttamente `Router` ma non il server production | `internal/api/routes_test.go` | aggiungere integration test del costruttore reale |
| URL `/api/internal/v1/*` | smoke test e runbook operativi | aggiornare o eliminare |
| gate documentato ma non obbligatorio | CI | promuovere a check richiesto |

## DEFINITION OF DONE

Il ticket può essere marcato `CLOSED` soltanto quando:

- le tre route internal sono presenti nel router costruito dal percorso production;
- nessuna route `/api/internal/*` è registrata;
- tutte le route internal sono protette da worker token;
- nessun writer applicativo indicizzabile può mutare `media_assets` senza evento outbox nella stessa transazione;
- dispatcher mancante blocca startup o operazione senza scrittura parziale;
- retry, lease reclaim, supersede, dead-letter, idempotenza e delete restano coperti;
- il gate automatico impedisce di reintrodurre writer raw o route pubbliche.

## GATE ANTI-REGRESSIONE

```bash
set -euo pipefail

# Routing: test sul percorso production, non soltanto Router.Setup manuale.
go test ./internal/api/... ./internal/app/... \
  -run 'Test.*Production.*InternalRoutes|TestRoutes_NoApiInternalV1Prefix' \
  -count=1

# Nessun riferimento operativo al vecchio URL.
! rg -n --glob '!docs/architecture/qdrant/QDRANT-002.md' \
  '/api/internal/v1/(outbox|media)' \
  internal cmd scripts tests docs/operations

# Nessun fallback raw nei writer già identificati.
! rg -n -U 'dispatcher\s*!=\s*nil[\s\S]{0,500}else[^\n]*\{?[\s\S]{0,300}(Upsert|UpsertClip)\(' \
  internal/application internal/api

# Suite outbox e asset ingestion.
go test ./internal/infrastructure/database/sqlite/outbox/... \
  ./internal/infrastructure/database/sqlite/outboxevents/... \
  ./internal/application/assets/... \
  ./internal/api/assets/...
```

## NON CHIUDERE SE

- le route esistono soltanto nel test che chiama `Set*Handler` prima di `Setup()`;
- anche un solo writer mantiene il fallback raw;
- il dispatcher viene creato ma non è obbligatorio nel composition root;
- il gate è soltanto un comando copiato nel Markdown e non viene eseguito dalla CI.
