# PR2 — Artlist infrastructure residua

## Obiettivo

Completare la separazione Artlist lasciando in `internal/application/artlist` soltanto orchestrazione, policy e porte consumer-side. Scraper Node/Playwright, process management, download HTTP/HLS, FFmpeg, filesystem, Drive SDK e persistenza concreta devono vivere in infrastructure.

## Stato verificato

Le porte consumer-side `Searcher`, `Downloader`, `AssetStore`, `Uploader`, `Indexer` e `MetadataWriter` esistono già. Sono stati rimossi alcuni setter, ma non esiste ancora un owner completo `internal/infrastructure/artlist`; il service applicativo conserva `database/sql`, Google Drive SDK, outbox SQLite e un dependency bundle sopra il budget.

## Checklist residua

### PR2.0 — Creare gli adapter infrastructure reali

- [ ] Creare `internal/infrastructure/artlist/scraper` come owner di avvio, health, shutdown, endpoint e parsing Node/Playwright.
- [ ] Creare `internal/infrastructure/artlist/downloader` come owner di download HTTP/HLS, header, retry, timeout e cleanup.
- [ ] Riutilizzare il process runner e FFmpeg canonici; nessun secondo wrapper shell o command builder.
- [ ] Mantenere private le risposte raw dello scraper e convertirle una sola volta in `artlist.Candidate`.
- [ ] Implementare error mapping coerente verso `ErrUnavailable`, `ErrTimeout`, `ErrInvalidResponse`, `ErrEmptyResult` ed eventuale fallback esplicito.
- [ ] Spostare `provider_scraper.go`, process lifecycle e filesystem fuori dall'application.

### PR2.1 — Ridurre le porte legacy

- [ ] Ridurre `AssetStore` ai metodi realmente consumati dai singoli use case.
- [ ] Eliminare la duplicazione `Upsert`/`UpsertClip` scegliendo un unico write contract canonico.
- [ ] Eliminare la duplicazione `SearchByTerms`/`SearchClips` quando la search unification è disponibile.
- [ ] Spostare `JobStats`, dettagli SQL e timestamp tecnici nel layer proprietario.
- [ ] Evitare che una porta applicativa replichi tutta la superficie di un repository concreto.
- [ ] Conservare `asset.Metadata` come unico tipo metadata condiviso.

### PR2.2 — Rimuovere concreti da `application/artlist`

- [ ] Rimuovere `database/sql` e il campo `MainDB` dal service applicativo.
- [ ] Rimuovere Google Drive SDK e sostituirlo con porte piccole per folder e upload.
- [ ] Rimuovere `*outbox.Dispatcher` concreto usando un contratto applicativo o dominio già esistente.
- [ ] Rimuovere repository lifecycle concreti quando il consumer necessita di un solo metodo.
- [ ] Portare `ServiceDependencies` entro il budget massimo di 8–10 dipendenze coerenti.
- [ ] Non nascondere il superamento del budget tramite embedding di mega-struct.

### PR2.3 — Ridurre facade e pass-through

- [ ] Verificare `SearchService`, `RunOrchestratorService`, `DestinationService`, `JobAdapter` e `DiagnosticsService`.
- [ ] Eliminare componenti che ricevono l'intero `*Service` e fanno soltanto pass-through.
- [ ] Iniettare in ogni componente soltanto le porte realmente usate.
- [ ] Conservare `verify`, `skip` e `replace` in un solo owner applicativo.
- [ ] Mantenere anti-zombie, lease e retry nel job system canonico.
- [ ] Usare una sola cache live con owner, TTL e invalidazione espliciti.

### PR2.4 — Eliminare fallback e percorsi paralleli

- [ ] Rimuovere il fallback `dispatcher nil → UpsertClip + IndexClip`.
- [ ] Rendere outbox/indicizzazione un percorso canonico e deterministico.
- [ ] Eliminare chiamate dirette allo scraper fuori dall'adapter infrastructure.
- [ ] Fare dipendere il provider registry dalla porta `Searcher`, non dal service completo.
- [ ] Eliminare switch provider fuori dal registry.

### PR2.5 — Test application e infrastructure

- [ ] Testare ricerca, download, upload e persistenza con fake separati.
- [ ] Testare `verify`, `skip` e `replace` senza scraper o Drive reali.
- [ ] Testare deduplicazione, active key e stato terminale dopo errori intermedi.
- [ ] Testare parser scraper con fixture e server HTTP locale.
- [ ] Testare timeout, risposta non valida, HLS detection, comando FFmpeg e cleanup.
- [ ] Proteggere gli E2E esterni con env flag esplicito.

### PR2.6 — Validazione finale

- [ ] Eseguire:

```bash
rg 'database/sql|os/exec|google.golang.org/api/drive|internal/infrastructure/database/sqlite' internal/application/artlist
rg 'UpsertClip|SearchByTerms|SearchClips|dispatch.*fallback' internal/application/artlist
go test ./internal/application/artlist/... -count=1
go test ./internal/infrastructure/artlist/... -count=1
go test ./internal/application/assets/providers/artlist/... -count=1
go test -race ./internal/application/artlist/... ./internal/infrastructure/artlist/...
go vet ./internal/application/artlist/... ./internal/infrastructure/artlist/...
go build ./...
go run ./scripts/archcheck
```

## Exit gate

PR2 è chiusa quando `application/artlist` non gestisce SQL, SDK Drive, processi, HLS o filesystem, `internal/infrastructure/artlist` possiede gli adapter tecnici e rimane un solo percorso per ricerca, persistenza, indicizzazione e metadata.
