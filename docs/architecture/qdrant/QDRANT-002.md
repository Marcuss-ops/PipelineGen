# QDRANT-002 — outbox atomico, writer unificati e route internal reali

> **Stato:** `BLOCKED / NON CHIUDIBILE`  
> **Audit baseline:** `main@e20d5e7fc4afd9f446d9d9e92703db639008b37f` — 26 giugno 2026  
> **Tipo verifica:** audit statico; nessuna prova CI verde associata all'HEAD.

## OBIETTIVO

Ogni mutazione indicizzabile di `media_assets` deve creare l'evento outbox nella stessa transazione. Gli endpoint outbox e mediasearch devono essere realmente montati su `/internal/v1/*` prima di `Router.Setup()` e protetti da `WorkerAuth`.

## COMPLETATO

- esiste un `outbox.Dispatcher` canonico per UPSERT/DELETE atomici;
- `UpdateClip` fallisce quando il dispatcher manca;
- catalog sync e stock pipeline sono fail-closed sul dispatcher;
- `/api/internal/v1/*` non viene registrato dal module registry;
- retry, lease e dead-letter hanno infrastruttura dedicata.

## BLOCKER ATTUALI

### 1. Route production montate dopo `Setup()`

`NewServerWithHealth` costruisce il router e chiama `Setup()`. Solo dopo, `cmd/server/main.go` chiama `SetOutboxHandler` e `SetMediasearchHandler`. I setter modificano campi del `Router`, ma non registrano route nel `gin.Engine` già costruito.

Il test esistente non riproduce production: imposta gli handler prima di `Setup()`.

### 2. Writer raw ancora raggiungibili

Restano fallback eseguibili senza outbox:

- Artlist `SearchLiveAndSave` -> `assetStore.Upsert`;
- immagini `RegisterVideoAsset` -> `stockRepo.Upsert`;
- immagini `registerAudioClip` -> `stockRepo.UpsertClip`;
- sourcing `RegisterFromYouTube` -> `clips.UpsertClip`, dichiarato esplicitamente “backward compatibility”.

### 3. Lifecycle mutation senza evento Qdrant

- `ClipsRepository.Restore` cambia direttamente `lifecycle_state='ready'` senza evento di reindex;
- `ClipsRepository.HardDelete` elimina direttamente `media_assets` senza evento Qdrant delete;
- i metodi low-level `Upsert`/`UpsertClip` restano pubblici e protetti soltanto da commenti.

### 4. Gate writer-ownership assente

La CI non dimostra che ogni writer applicativo indicizzabile passi dal dispatcher. Un grep nel Markdown non è un gate sufficiente: serve controllo AST/ownership nel checker architetturale.

## TASK RESIDUI

### A. Wiring route pre-Setup

Passare `OutboxHandler` e `MediasearchHandler` al costruttore del server, o tramite un unico bundle tipizzato, e impostarli sul `Router` prima dell'unica chiamata a `Setup()`.

Rimuovere o rendere non utilizzabili i setter post-Setup.

### B. Test del percorso production

Costruire il server tramite `NewServerWithHealth` e verificare:

```text
GET  /internal/v1/outbox/status
GET  /internal/v1/outbox/events
POST /internal/v1/media/search
```

Verificare anche assenza di `/api/internal/*`, worker token accettato e token admin/mancante rifiutato.

### C. Cutover completo dei writer

Eliminare ogni fallback raw. Dispatcher assente deve produrre errore di startup o errore dell'operazione prima di qualsiasi scrittura.

Aggiungere operazioni canoniche per restore e hard delete, entrambe atomiche con il relativo evento outbox.

### D. Restringere le API low-level

Portare `Upsert`, `UpsertClip`, `Restore` e `HardDelete` dietro porte/repository autorizzati, oppure introdurre un gate AST che vieti chiamate fuori dall'allowlist del dispatcher e degli strumenti offline espliciti.

## LEGACY DA ELIMINARE

| Legacy | Dove | Azione |
|---|---|---|
| setter route post-Setup | `cmd/server/main.go`, `internal/api/server.go` | injection pre-Setup |
| test Router manuale | `internal/api/routes_test.go` | test production constructor |
| Artlist raw fallback | provider Artlist | dispatcher obbligatorio |
| video/audio raw fallback | images service | dispatcher obbligatorio |
| sourcing raw fallback | sourcing service | dispatcher obbligatorio |
| restore senza outbox | clips repository | `EnqueueAndRestore` o equivalente |
| hard delete senza outbox | clips repository | delete atomico + evento |
| writer low-level pubblici | repository surface | restringere ownership |
| gate solo documentato | CI/archcheck | gate AST obbligatorio |

## DEFINITION OF DONE

Il ticket può essere marcato `CLOSED` soltanto quando:

- le route internal sono presenti nel server costruito dal percorso production;
- nessuna route `/api/internal/*` esiste;
- worker auth è verificata comportamentalmente;
- nessun writer applicativo può mutare un asset indicizzabile senza evento outbox atomico;
- restore e delete sincronizzano Qdrant tramite il percorso canonico;
- dispatcher mancante blocca prima della mutazione;
- un gate automatico impedisce la reintroduzione di writer raw e setter post-Setup.

## GATE MINIMO

```bash
set -euo pipefail

go test ./internal/api/... -run 'Test.*Production.*InternalRoutes|TestRoutes_NoApiInternalV1Prefix' -count=1
! rg -n 'Falls back to raw|Legacy path: raw|backward compatibility when dispatcher' internal/application
! rg -n 'SetOutboxHandler|SetMediasearchHandler' cmd/server/main.go
go test ./internal/infrastructure/database/sqlite/outbox/... ./internal/application/assets/...
```

## NON CHIUDERE SE

- le route esistono soltanto in un test che configura il Router prima di `Setup()`;
- un solo fallback raw resta raggiungibile;
- restore o hard delete bypassano outbox;
- l'ownership è affidata soltanto ai commenti;
- il gate non è required in CI.
