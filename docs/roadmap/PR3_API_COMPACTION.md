# PR3 — API compaction per capability

## Obiettivo

Ridurre la frammentazione del trasporto HTTP consolidando i package API per capability, senza cambiare route, payload o semantica degli endpoint.

## Stato iniziale verificato

Restano package separati:

- `internal/api/drive`
- `internal/api/realtime`
- `internal/api/searchqueries`
- `internal/api/sources`
- `internal/api/fullimages`
- `internal/api/workers`
- `internal/api/script`

Target:

```text
internal/api/assets/
internal/api/images/
internal/api/jobs/
internal/api/scripts/
internal/api/content/
internal/api/channels/
internal/api/system/
internal/api/middleware/
internal/api/transport/
```

## Regole di compatibilità

- nessuna route pubblica cambia path;
- nessun campo JSON viene rimosso o rinominato senza contract test;
- i package API non costruiscono service o repository;
- nessun accesso SQL, filesystem, processo o goroutine orchestration nel transport;
- ogni capability espone un solo punto di registrazione route.

## Checklist operativa

### PR3.0 — Congelare il contratto HTTP

- [ ] Generare `docs/api/ACTIVE_API_GENERATED.md` dallo stato iniziale.
- [ ] Salvare un inventario di metodo, path, handler e middleware.
- [ ] Aggiungere contract test per le route che verranno spostate.
- [ ] Identificare route duplicate o registrate da più moduli.
- [ ] Identificare handler che costruiscono dipendenze concrete.
- [ ] Identificare handler con business logic, SQL o process invocation.

**Accettazione PR3.0**

Esiste una baseline testabile delle route. Ogni spostamento successivo deve produrre lo stesso output API generato.

### PR3.1 — Consolidare `api/drive` in `api/assets`

- [ ] Spostare handler e module registration sotto `internal/api/assets`.
- [ ] Mantenere invariati i path `/api/...` esistenti.
- [ ] Sostituire import interni tra package API con dipendenze verso use case/porte.
- [ ] Eliminare dipendenze da `api/sources/clips` se possono essere sostituite da un use case asset.
- [ ] Unificare error mapping e response helpers con `api/transport`.
- [ ] Eliminare `internal/api/drive` quando gli import sono zero.

**Exit gate PR3.1**

```bash
test ! -d internal/api/drive
rg 'internal/api/drive' --type go
go test ./internal/api/assets/... -count=1
```

### PR3.2 — Consolidare `api/realtime` e `api/searchqueries` in `api/assets`

- [ ] Spostare route realtime asset/search sotto `api/assets`.
- [ ] Spostare search query handlers sotto `api/assets`.
- [ ] Mantenere SSE/websocket lifecycle separato dal business logic.
- [ ] Non avviare goroutine nel costruttore del modulo API.
- [ ] Iniettare service già costruiti dal composition root.
- [ ] Eliminare i due package originari quando gli import sono zero.

**Exit gate PR3.2**

```bash
test ! -d internal/api/realtime
test ! -d internal/api/searchqueries
rg 'internal/api/(realtime|searchqueries)' --type go
go test ./internal/api/assets/... -count=1
```

### PR3.3 — Consolidare `api/sources` in `api/assets`

- [ ] Inventariare i sotto-package `artlist`, `youtube`, `clips`, `internal`, `root`.
- [ ] Spostare gli handler per provider/search sotto file capability-specifici in `api/assets`.
- [ ] Mantenere provider registry come unico punto di dispatch.
- [ ] Eliminare fallback diretti verso service YouTube/Artlist.
- [ ] Eliminare dipendenze tra sotto-package API.
- [ ] Conservare paginazione, diagnostics e payload esistenti.
- [ ] Eliminare `internal/api/sources` quando gli import sono zero.

**Exit gate PR3.3**

```bash
test ! -d internal/api/sources
rg 'internal/api/sources' --type go
go test ./internal/api/assets/... -count=1
go test ./internal/application/assets/providers/... -count=1
```

### PR3.4 — Consolidare `api/fullimages` in `api/images`

- [ ] Spostare route full-image sotto `internal/api/images`.
- [ ] Rimuovere import diretto di `internal/media/fullimages` dal transport.
- [ ] Iniettare un use case immagini già costruito.
- [ ] Unificare validazione e response mapping con `api/transport`.
- [ ] Eliminare `internal/api/fullimages`.

**Exit gate PR3.4**

```bash
test ! -d internal/api/fullimages
rg 'internal/api/fullimages' --type go
go test ./internal/api/images/... -count=1
```

### PR3.5 — Consolidare `api/workers` in `api/jobs`

- [ ] Spostare registrazione worker, heartbeat, capability e asset transfer sotto `api/jobs`.
- [ ] Mantenere separati DTO HTTP e modelli `domain/job`.
- [ ] Non importare repository SQLite dal transport.
- [ ] Conservare autenticazione worker e middleware esistenti.
- [ ] Eliminare `internal/api/workers`.

**Exit gate PR3.5**

```bash
test ! -d internal/api/workers
rg 'internal/api/workers' --type go
go test ./internal/api/jobs/... -count=1
go test ./internal/application/jobs/... -count=1
```

### PR3.6 — Rinominare `api/script` in `api/scripts`

- [ ] Spostare package root e sotto-package `catalog`/`curation` sotto `internal/api/scripts`.
- [ ] Mantenere tutti i path `/api/script/...` invariati.
- [ ] Eliminare logica applicativa residua dagli handler.
- [ ] Usare `api/transport` per bind/validate/invoke/map/respond dove applicabile.
- [ ] Mantenere job handlers applicativi fuori dal transport quando non sono route HTTP.
- [ ] Eliminare `internal/api/script`.

**Exit gate PR3.6**

```bash
test ! -d internal/api/script
rg 'internal/api/script' --type go
go test ./internal/api/scripts/... -count=1
go test ./internal/application/scripts/... -count=1
```

### PR3.7 — Uniformare i moduli API

- [ ] Ogni capability espone al massimo un `RegisterRoutes` pubblico.
- [ ] Ogni capability ha un handler root o un gruppo piccolo di handler interni non esportati.
- [ ] Eliminare `NewService` e `NewRepository` dai package API.
- [ ] Eliminare accessi diretti a config globale quando i valori possono essere iniettati.
- [ ] Eliminare helper duplicati di binding, validation e response.
- [ ] Aggiungere `transport.Query` o equivalente soltanto se sostituisce duplicazione reale in almeno due handler.
- [ ] Non creare una nuova astrazione generica senza consumer immediati.

### PR3.8 — Rigenerare documentazione e contract test

- [ ] Rigenerare `docs/api/ACTIVE_API_GENERATED.md`.
- [ ] Confrontare metodo e path con la baseline PR3.0.
- [ ] Verificare middleware auth per admin e worker.
- [ ] Verificare status code e body per errori di validazione.
- [ ] Verificare endpoints asincroni e job IDs.
- [ ] Verificare SSE/realtime shutdown.

### PR3.9 — Validazione finale

- [ ] Eseguire:

```bash
go test ./internal/api/... -count=1
go test -race ./internal/api/...
go vet ./internal/api/...
go build ./...
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
git diff --exit-code docs/api/ACTIVE_API_GENERATED.md
go run ./scripts/archcheck
```

- [ ] Cercare violazioni di layer:

```bash
rg 'database/sql|internal/infrastructure/database/sqlite|os/exec' internal/api
```

- [ ] Verificare che non esistano i sette package originari.

## Exit gate finale

PR3 è completata quando:

- le route sono organizzate per capability;
- le route pubbliche sono compatibili con la baseline;
- nessun package API costruisce dipendenze concrete;
- nessun package API accede a SQL o processi;
- `api/drive`, `api/realtime`, `api/searchqueries`, `api/sources`, `api/fullimages`, `api/workers` e `api/script` non esistono più;
- documentazione API, test e build sono verdi.
