# PR3 — API compaction residua

## Obiettivo

Eliminare i sette package API legacy ancora presenti, consolidando il trasporto per capability senza cambiare method, path, payload, autenticazione o semantica degli endpoint.

## Stato verificato

Sono già stati consolidati `books`/`lessons` in `api/content` e `scraper`/`mediaingest` in `api/assets`; esiste `api/transport`. Restano:

- `internal/api/drive`;
- `internal/api/realtime`;
- `internal/api/searchqueries`;
- `internal/api/sources`;
- `internal/api/fullimages`;
- `internal/api/workers`;
- `internal/api/script`.

## Checklist residua

### PR3.0 — Congelare e verificare il contratto HTTP

- [ ] Rigenerare `docs/api/ACTIVE_API_GENERATED.md` dallo stato precedente agli spostamenti.
- [ ] Salvare una matrice method, path, handler, middleware e auth.
- [ ] Aggiungere contract test per tutte le route dei sette package residui.
- [ ] Identificare route duplicate, handler con business logic e costruzioni concrete nel transport.
- [ ] Impedire che ogni spostamento modifichi l'output API generato.

### PR3.1 — Consolidare asset transport

- [ ] Spostare `api/drive` in `api/assets` e usare use case già costruiti.
- [ ] Spostare `api/realtime` e `api/searchqueries` in `api/assets` mantenendo lifecycle realtime separato.
- [ ] Spostare `api/sources` in `api/assets` per vertical slice, senza ricreare sottopackage paralleli.
- [ ] Usare provider registry come unico dispatch per YouTube, Artlist e stock.
- [ ] Eliminare dipendenze tra package API e fallback diretti verso service concreti.
- [ ] Eliminare fisicamente i quattro package originari quando gli import sono zero.

### PR3.2 — Consolidare images, jobs e scripts

- [ ] Spostare `api/fullimages` in `api/images` e dipendere da un use case immagini.
- [ ] Spostare `api/workers` in `api/jobs`, mantenendo DTO HTTP distinti dai modelli `domain/job`.
- [ ] Rinominare `api/script` in `api/scripts`, mantenendo invariati i path `/api/script/...`.
- [ ] Tenere job handler applicativi fuori dal transport.
- [ ] Eliminare fisicamente i tre package originari quando gli import sono zero.

### PR3.3 — Rendere il transport sottile e uniforme

- [ ] Ogni capability espone un solo punto pubblico di registrazione route.
- [ ] Eliminare `NewService`, `NewRepository`, SQL, filesystem, process execution e goroutine orchestration dai package API.
- [ ] Iniettare dipendenze già costruite dal composition root.
- [ ] Centralizzare bind, validation, error mapping e response soltanto quando sostituiscono duplicazione reale.
- [ ] Non introdurre framework generici, reflection o mega handler condivisi.
- [ ] Propagare sempre il request context ai use case.

### PR3.4 — Chiudere il debito `internal/media` richiesto dalle route

- [ ] Rimuovere gli import transport verso `internal/media/fullimages`, `stockpipeline`, `clipresolver`, `vectorstore` e altri package coinvolti.
- [ ] Collegare le route ai nuovi owner application/infrastructure.
- [ ] Non spostare business logic dentro `api/assets` o `api/images` per eliminare un import.
- [ ] Aggiornare `architecture/migration.yaml` con i package `internal/media` eliminati dalla stessa vertical slice.

### PR3.5 — Contract e regression test

- [ ] Verificare auth admin e worker.
- [ ] Verificare bind error, validation error, not found, conflict e internal error.
- [ ] Verificare endpoint asincroni, job IDs e progress.
- [ ] Verificare SSE/realtime start e shutdown senza goroutine leak.
- [ ] Confrontare documentazione API prima/dopo e richiedere zero variazioni non approvate.

### PR3.6 — Validazione finale

- [ ] Eseguire:

```bash
test ! -d internal/api/drive
test ! -d internal/api/realtime
test ! -d internal/api/searchqueries
test ! -d internal/api/sources
test ! -d internal/api/fullimages
test ! -d internal/api/workers
test ! -d internal/api/script
rg 'database/sql|internal/infrastructure/database/sqlite|os/exec' internal/api
go test ./internal/api/... -count=1
go test -race ./internal/api/...
go vet ./internal/api/...
go build ./...
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
git diff --exit-code docs/api/ACTIVE_API_GENERATED.md
go run ./scripts/archcheck
```

## Exit gate

PR3 è chiusa quando i sette package originari non esistono più, ogni capability ha un unico route registrar, il transport non contiene business logic o adapter concreti e il contratto HTTP è invariato e coperto da test.
