# PR4 — Composition root residua

## Obiettivo

Chiudere il lavoro rimasto dopo la rimozione di `type services struct` e `CoreDeps`. Il composition root deve costruire adapter concreti una sola volta, esporre bundle capability-owned piccoli e separare completamente costruzione, avvio e shutdown.

## Stato verificato

Sono già completati e quindi rimossi dalla checklist:

- eliminazione di `type services struct`;
- eliminazione di `CoreDeps`;
- introduzione di `ComposeRoot` e bundle principali;
- migrazione dei `Wire<Module>` a firme più strette;
- `WireRegistry` sul root corrente;
- freeze di registry/dispatcher prima dell'avvio del job runner;
- introduzione di `bootstrap.go`, `lifecycle.go` e `shutdown.go`.

Restano alias, helper condivisi, alcuni late binding, lifecycle basato su closure, goroutine avviate durante la costruzione e test di composizione incompleti.

## Checklist residua

### PR4.0 — Eliminare alias e re-export residui

- [ ] Eliminare `type JobsWireBundle = JobsBundle`.
- [ ] Eliminare `appjobs.SQLiteStore`, `JobStats` ed `ErrLeaseLost` come alias/re-export infrastructure.
- [ ] Fare importare al composition root l'implementazione SQLite concreta e ai consumer il contratto `domain/job.Store`.
- [ ] Spostare `JobStats` in un DTO/application owner appropriato.
- [ ] Cercare ed eliminare altri alias, function rebinding e compat wrapper dentro `internal/app` e `internal/application/jobs`.

### PR4.1 — Rendere i bundle realmente capability-owned

- [ ] Verificare che ogni bundle contenga solo campi della propria capability.
- [ ] Rimuovere riferimenti mirror come registry posseduti da un bundle ma copiati in un altro.
- [ ] Costruire asset repository, provider registry, indexer e resolver nel modulo assets.
- [ ] Portare Books/Lessons in un builder content dedicato.
- [ ] Portare images e voiceover fuori dagli helper generici rimasti in `dependencies.go`.
- [ ] Portare scripts, system e observability in builder espliciti e piccoli.
- [ ] Eliminare helper condivisi rimasti in `dependencies.go` quando fanno composizione di capability.

### PR4.2 — Ridurre dipendenze e late binding

- [ ] Verificare un budget massimo di 8–10 dipendenze dirette per builder.
- [ ] Non aggirare il budget tramite struct `Dependencies` eterogenee o embedding.
- [ ] Eliminare `SetBatchService`, `SetIngestService`, `SetHarvestService` e setter equivalenti quando il ciclo può essere risolto con ordine topologico o interfacce più piccole.
- [ ] Documentare gli eventuali cicli reali rimasti e aggiungere test che ne proteggano il lifecycle.
- [ ] Non passare `ComposeRoot` o un intero bundle ai use case e agli handler.

### PR4.3 — Separare costruzione e lifecycle

- [ ] Nessun `Build*Bundle` o costruttore deve avviare goroutine.
- [ ] Spostare `ensureStyleDriveFolders` e attività equivalenti in un componente `Start(ctx)` esplicito.
- [ ] Sostituire la closure `startJobRunner func()` con un lifecycle component tipizzato.
- [ ] Rendere `Start` e `Stop` idempotenti o protetti esplicitamente.
- [ ] Propagare il context root a worker, scheduler, monitor, outbox e maintenance.
- [ ] Eliminare `context.Background()` dai path lifecycle quando esiste il context applicativo.
- [ ] Fermare i componenti in ordine inverso e verificare cleanup parziale dopo bootstrap fallito.

### PR4.4 — Rimuovere SQL dal dominio

- [ ] Spostare `AssetStoreSQLite`, query, scan e JSON persistence fuori da `internal/domain/asset`.
- [ ] Lasciare nel dominio soltanto modelli, invarianti, errori e contratti repository.
- [ ] Usare `internal/infrastructure/database/sqlite/assets` come unico owner SQLite.
- [ ] Aggiornare i consumer verso porte domain/application senza wrapper di compatibilità.
- [ ] Verificare zero import `database/sql` e SQLite dentro `internal/domain`.

### PR4.5 — Chiudere i moduli ancora legati a `internal/media`

- [ ] Assegnare un owner finale a catalog, tree, index, resolver, semantic, vectorstore, stockpipeline, books, lessons, generation e voiceover sync.
- [ ] Migrare soltanto i package richiesti dalle capability PR1–PR4, senza mega spostamenti non testabili.
- [ ] Eliminare il package originario e i relativi import nella stessa vertical slice.
- [ ] Non creare package nuovi che duplicano temporaneamente la stessa logica.

### PR4.6 — Test di composizione

- [ ] Testare ogni builder con dipendenze obbligatorie nil.
- [ ] Testare campi obbligatori dei bundle.
- [ ] Testare che la costruzione non avvii goroutine o processi.
- [ ] Testare duplicate registration e freeze di provider/job handler.
- [ ] Testare Start/Stop, shutdown parziale e capability opzionali.
- [ ] Testare che API e use case non ricevano `ComposeRoot` come service locator.

### PR4.7 — Validazione finale

- [ ] Eseguire:

```bash
rg 'type JobsWireBundle =|type SQLiteStore =|type JobStats =|var ErrLeaseLost =' internal/app internal/application/jobs
rg 'type services struct|CoreDeps' internal/app
rg 'database/sql|internal/infrastructure/database/sqlite' internal/domain
rg 'go ensureStyleDriveFolders|startJobRunner func\(' internal/app
go test ./internal/app/... -count=1
go test -race ./internal/app/...
go test ./internal/api/... ./internal/application/... -count=1
go vet ./internal/app/... ./internal/application/... ./internal/domain/...
go build ./...
go run ./scripts/archcheck
```

- [ ] Avviare il server e verificare `/api/health/deep`.
- [ ] Verificare documentazione API invariata.
- [ ] Verificare zero nuovi alias, wrapper pass-through e service locator.

## Exit gate

PR4 è chiusa quando ogni capability ha un owner di composizione chiaro, non esistono alias o mega dependency bundle, nessun costruttore avvia goroutine, il dominio non contiene SQL e lifecycle/shutdown sono espliciti e coperti da test.
