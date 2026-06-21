# PR4 — Composition root modulare

> **Prerequisites (June 2026, post-PR4d-final)** — Molti item di PR4 sono
> gi\u00e0 **completati da Wave 15 PR4d-final**, landed prima di PR #26.
> Prima di procedere marcare come `[x] (fatto da Wave 15 PR4d-final)` i
> checkbox gi\u00e0 soddisfatti e lasciare solo i residui.
>
> **Gi\u00e0 completati da Wave 15 PR4d-final** (vedi
> `architecture/migration.yaml` sezione Wave 15 `completed_in_PR4d_final`):
>
> - `type services struct` rimosso da `internal/app/dependencies.go`
> - `type CoreDeps struct` rimosso da `internal/app/bootstrap.go`
> - Tutti i 9 `Wire<Module>()` migrati a narrow bundle signatures
> - `WireRegistry(ctx, cfg, log, root *ComposeRoot)` uniforme
> - `ComposeRoot` con `Ctx context.Context` field
> - `startJobRunner()` schedulato dopo `Registry.Freeze()`
>
> **Residuo effettivo di PR4** (PR4.0..PR4.12 sotto):
>
> - PR4.0 — mappatura completa del grafo (non ancora formalizzata)
> - PR4.1 — alias `appjobs.SQLiteStore` (Wave 5_PR3 deferred a Wave 16)
> - PR4.2 — refactor `BuildAssetBundle` (oggi inline in `composition.go`)
> - PR4.3 — refactor `BuildContentBundle` (libri/lezioni oggi shared helpers)
> - PR4.7 — riorganizzare `Modules` aggregato (oggi \u00e8 `*ComposeRoot`)
> - PR4.8 — separare bootstrap/lifecycle/shutdown (lifecycle ancora legato
>   a `*backgroundJobs` closure)
> - PR4.10 — test di composizione espliciti
> - PR4.11 — audit budget 8\u201310 dipendenze per builder
> - PR4.12 — smoke test server con `curl /api/health/deep`

## Obiettivo

Eliminare il contenitore globale `services`, ridurre `CoreDeps` e costruire moduli capability-owned. `internal/app` deve essere l'unico punto in cui vengono creati adapter concreti.

## Addendum (post code-review, June 2026, commit fd8e3a43+1)

> 5 bullet mancanti dall'elenco sopra, identificati dal code-reviewer del
> primo batch di modifiche PR0:

**Già fatti da Wave 15 PR4d-final ma non elencati sopra:**

- BREAKING API CHANGE: `app.InitComposition` return tuple è passata da
  `(*ComposeRoot, CleanupFunc, error)` → `(*ComposeRoot, *backgroundJobs,
  CleanupFunc, error)`.
- `WireServices` + `WireMinimal` in `bootstrap.go` sono stati migrati al
  path `initCompositionMinimal(...) → (ComposeRoot, backgroundJobs,
  CleanupFunc, error)`.

**Residuo PR4 non elencato sopra:**

- PR4.4 — refactor `BuildImagesBundle` + `BuildVoiceoverBundle` in
  `modules/{images,voiceover}.go` (oggi helpers in `dependencies.go`).
- PR4.5 — refactor `BuildScriptsBundle` in `modules/scripts.go` con
  `SetBatchService` rimosso (oggi partial inline in `composition.go`).
- PR4.6 — refactor `BuildSystemBundle` in `modules/system.go` con
  config+doctor+health+metrics isolati (oggi helper condiviso).

Questa PR non cambia il comportamento dei use case e non aggiunge nuove capability.

## Stato iniziale verificato

`internal/app/dependencies.go` contiene ancora:

- `type services struct` con decine di campi;
- composer che costruiscono più capability insieme;
- late binding tramite setter;
- dipendenze concrete SQLite, Drive, provider, media, jobs, scripts e content nello stesso grafo;
- `JobsBundle` come unico pilot parziale.

## Struttura target

```text
internal/app/
  app.go
  bootstrap.go
  lifecycle.go
  shutdown.go
  modules/
    assets.go
    content.go
    images.go
    jobs.go
    scripts.go
    system.go
    voiceover.go
```

Il nome dei file può adattarsi al codice esistente, ma ogni modulo deve possedere costruzione, superficie runtime e lifecycle della propria capability.

## Checklist operativa

### PR4.0 — Mappare il grafo attuale

- [ ] Elencare tutti i campi di `services`.
- [ ] Elencare tutti i campi di `CoreDeps` e altri contenitori globali.
- [ ] Per ogni campo, assegnare un owner:
  - assets;
  - content;
  - images;
  - jobs;
  - scripts;
  - system;
  - voiceover;
  - shared infrastructure.
- [ ] Identificare tutte le costruzioni concrete fuori da `internal/app`.
- [ ] Identificare tutti i setter di late binding.
- [ ] Identificare cicli reali e dipendenze soltanto accidentali.
- [ ] Creare un diagramma semplice nel documento o nei commenti del codice, senza introdurre un secondo framework DI.

**Accettazione PR4.0**

Ogni campo globale ha un owner e un piano di rimozione. Nessun modulo viene creato come semplice copia dell'intero `services`.

### PR4.1 — Finalizzare il modulo Jobs

- [ ] Rendere `JobsBundle` indipendente da `CoreDeps`.
- [ ] Costruire `sqljobs.SQLiteStore` direttamente nel composition root.
- [ ] Iniettare `job.Store` nei service applicativi.
- [ ] Eliminare l'alias `appjobs.SQLiteStore` se non più necessario.
- [ ] Spostare `JobStats` nel layer proprietario corretto o mappare a DTO applicativo.
- [ ] Eliminare la facade `job.Service` se è solo un pass-through a funzioni delegate.
- [ ] Iniettare `appjobs.Service` o una porta minima nei consumer.
- [ ] Mantenere dispatcher freeze dopo la registrazione di tutti gli handler.
- [ ] Separare costruzione da avvio worker.

**Exit gate PR4.1**

```bash
rg 'type SQLiteStore =|type JobStats =|var ErrLeaseLost =' internal/application/jobs
rg 'CoreDeps.*Jobs|JobsService' internal/app
go test ./internal/application/jobs/... ./internal/app/... -count=1
```

Gli alias temporanei devono essere zero oppure accompagnati da un owner e una motivazione non pass-through.

### PR4.2 — Creare modulo Assets

- [ ] Creare un bundle assets con solo le dipendenze realmente esportate.
- [ ] Costruire repository asset SQLite nel modulo assets.
- [ ] Costruire provider registry nel modulo assets.
- [ ] Costruire indexer, tree, catalog, ingest e artifact services nei rispettivi owner.
- [ ] Non esporre repository concreti a moduli che necessitano solo di porte.
- [ ] Non passare l'intero bundle assets ai consumer; passare il singolo contratto richiesto.
- [ ] Congelare il provider registry dopo il wiring.
- [ ] Separare lifecycle di monitor/indexer dalla costruzione.

### PR4.3 — Creare modulo Content

- [ ] Costruire Books e Lessons nel modulo content.
- [ ] Spostare le dipendenze condivise esplicite in parametri piccoli.
- [ ] Iniettare job registration senza accedere al contenitore globale.
- [ ] Rimuovere import diretti dei vecchi package media dal composition root quando esiste il nuovo owner application.
- [ ] Esporre soltanto use case/handler necessari al bootstrap API.

### PR4.4 — Creare moduli Images e Voiceover

- [ ] Creare il modulo images con repository, generator, style registry e handler.
- [ ] Creare il modulo voiceover con repository, service, sync e handler.
- [ ] Spostare integrazioni concrete Drive/FFmpeg nei builder app, non nei package API.
- [ ] Evitare dipendenze reciproche tra images e voiceover; usare porte se necessarie.
- [ ] Separare worker/job handler dal trasporto HTTP.

### PR4.5 — Creare modulo Scripts

- [ ] Costruire repository scripts SQLite nel modulo scripts.
- [ ] Costruire generator, memory, batch, curation e document services in ordine esplicito.
- [ ] Iniettare job service e asset provider tramite porte minime.
- [ ] Eliminare duplicazione tra `scripts` e alias come `scriptcore`.
- [ ] Rimuovere setter `SetBatchService` o equivalenti quando il costruttore può ricevere la dipendenza.
- [ ] Esporre handler API e job handlers come superfici separate.

### PR4.6 — Creare modulo System

- [ ] Raggruppare config read-only, doctor, health, metrics e diagnostics.
- [ ] Non trasformare System in un contenitore globale.
- [ ] Esporre health checks come funzioni/porte piccole.
- [ ] Mantenere logging e observability come shared infrastructure costruita una volta.
- [ ] Non permettere a System di importare tutti gli altri moduli per accedere ai loro internals.

### PR4.7 — Introdurre l'aggregato `Modules`

- [ ] Creare un aggregato usato soltanto da bootstrap/lifecycle/shutdown.
- [ ] Includere solo bundle capability-owned.
- [ ] Vietare l'uso di `Modules` come parametro dei use case.
- [ ] Vietare l'uso di `Modules` come service locator negli handler.
- [ ] Costruire i moduli in ordine topologico esplicito.
- [ ] Rendere opzionali solo capability realmente opzionali.
- [ ] Fallire rapidamente su dipendenze obbligatorie nil.

Esempio di forma:

```go
type Modules struct {
    Assets    *modules.Assets
    Content   *modules.Content
    Images    *modules.Images
    Jobs      *modules.Jobs
    Scripts   *modules.Scripts
    System    *modules.System
    Voiceover *modules.Voiceover
}
```

### PR4.8 — Separare bootstrap, lifecycle e shutdown

- [ ] `bootstrap.go` costruisce dipendenze e registra route/handler.
- [ ] `lifecycle.go` avvia worker, scheduler, monitor, outbox e maintenance.
- [ ] `shutdown.go` ferma componenti in ordine inverso.
- [ ] Nessun costruttore avvia goroutine.
- [ ] Start e Stop devono essere idempotenti o protetti esplicitamente.
- [ ] Propagare il context root a tutti i componenti long-running.
- [ ] Eliminare `context.Background()` dai lifecycle quando esiste il context applicativo.

### PR4.9 — Eliminare `services` e `CoreDeps`

- [ ] Migrare tutti i consumer di `*services` verso bundle/porte specifiche.
- [ ] Migrare tutti i consumer di `*CoreDeps` verso parametri espliciti.
- [ ] Eliminare `type services struct`.
- [ ] Eliminare `CoreDeps` se diventa vuoto o puramente pass-through.
- [ ] Eliminare composer che aggregano capability non correlate.
- [ ] Eliminare setter rimasti senza ciclo reale.
- [ ] Eliminare commenti “temporary”, “back-compat” e “follow-up” risolti dalla PR.

**Exit gate PR4.9**

```bash
rg 'type services struct|\*services|CoreDeps' internal/app
```

Il risultato deve essere zero, salvo un eventuale tipo nuovo con responsabilità chiaramente diversa e non service locator.

### PR4.10 — Test di composizione

- [ ] Testare ogni builder con dipendenze obbligatorie nil.
- [ ] Testare che ogni bundle abbia campi obbligatori non nil.
- [ ] Testare che costruire i moduli non avvii goroutine.
- [ ] Testare registrazione duplicata di job/provider.
- [ ] Testare freeze registry/dispatcher.
- [ ] Testare Start/Stop applicativo.
- [ ] Testare shutdown parziale dopo errore di bootstrap.
- [ ] Testare configurazione senza capability opzionali.

### PR4.11 — Budget di dipendenze

- [ ] Ogni builder modulo riceve massimo 8–10 dipendenze dirette.
- [ ] Se supera il limite, creare una porta coerente, non un generico `Dependencies`.
- [ ] Nessun bundle contiene campi di capability non propria.
- [ ] Nessun modulo importa package API di un'altra capability.
- [ ] Nessun package API costruisce adapter concreti.

### PR4.12 — Validazione finale

- [ ] Eseguire:

```bash
go test ./internal/app/... -count=1
go test -race ./internal/app/...
go test ./internal/api/... ./internal/application/... -count=1
go vet ./internal/app/... ./internal/application/...
go build ./cmd/server ./cmd/worker ./cmd/admin
go build ./...
go run ./scripts/archcheck
```

- [ ] Eseguire smoke test server:

```bash
go run ./cmd/server --mode all
curl -f http://127.0.0.1:${VELOX_PORT:-8080}/api/health/deep
```

- [ ] Verificare che la documentazione API sia invariata.
- [ ] Verificare assenza di nuovi alias e wrapper pass-through.

## Exit gate finale

PR4 è completata quando:

- `type services struct` non esiste;
- `CoreDeps` non è più un service locator;
- ogni capability ha un modulo proprietario;
- costruzione, avvio e shutdown sono separati;
- nessun handler costruisce service concreti;
- ogni builder rispetta il budget di dipendenze;
- test, race test, build, smoke test e archcheck sono verdi.
