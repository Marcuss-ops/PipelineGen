# PR2 — Separazione Artlist infrastructure

## Obiettivo

Lasciare in `internal/application/artlist` soltanto orchestrazione, policy e porte. Spostare scraper Node/Playwright, process management, download, filesystem, HLS/FFmpeg e persistenza tecnica in `internal/infrastructure/artlist`.

Questa PR non cambia le route Artlist, i payload pubblici o le strategie `verify`, `skip`, `replace`.

## Stato iniziale verificato

`internal/application/artlist` contiene ancora dipendenze concrete verso:

- `database/sql`;
- Google Drive SDK;
- repository SQLite;
- outbox SQLite;
- clip indexer concreto;
- processi Node/Playwright;
- downloader e filesystem.

## Struttura target

```text
internal/application/artlist/
  service.go
  search_usecase.go
  run_usecase.go
  ports.go
  requests.go
  results.go
  policies.go

internal/infrastructure/artlist/
  scraper/
    client.go
    process.go
    parser.go
  downloader/
    download.go
    hls.go
  storage.go
  metadata.go
```

## Checklist operativa

### PR2.0 — Inventario delle responsabilità

- [ ] Elencare tutti i file in `internal/application/artlist`.
- [ ] Classificare ogni file come:
  - use case;
  - policy;
  - porta;
  - scraper/processo;
  - downloader;
  - persistenza;
  - Drive/storage;
  - mapping provider.
- [ ] Cercare dipendenze concrete:

```bash
rg 'database/sql|os/exec|node|playwright|ffmpeg|google.golang.org/api/drive|internal/infrastructure/database/sqlite|internal/media' internal/application/artlist
```

- [ ] Elencare tutti i consumer diretti dei service Artlist.
- [ ] Identificare quali componenti sono già usati dal provider registry.

**Accettazione PR2.0**

Ogni file ha un target esplicito e nessun componente viene duplicato durante lo spostamento.

### PR2.1 — Definire porte applicative minime

- [ ] Definire una porta `Searcher` per la ricerca live.
- [ ] Definire una porta `Downloader` per il download del candidato selezionato.
- [ ] Definire una porta `AssetStore` o riusare il contratto canonico già presente.
- [ ] Definire una porta `Uploader` solo se il use case coordina Drive.
- [ ] Definire una porta `Indexer` solo se l'indicizzazione è parte del workflow applicativo.
- [ ] Non esporre `*sql.DB`, `drive.Service`, process handle o path interni nelle interfacce.
- [ ] Non creare un mega `ArtlistInfrastructure` interface.

Esempio:

```go
type Searcher interface {
    Search(context.Context, SearchRequest) ([]Candidate, error)
}

type Downloader interface {
    Download(context.Context, DownloadRequest) (DownloadedAsset, error)
}
```

### PR2.2 — Estrarre scraper e process management

- [ ] Creare `internal/infrastructure/artlist/scraper`.
- [ ] Spostare avvio, health-check e shutdown del processo Node/Playwright.
- [ ] Spostare gestione endpoint scraper e timeout.
- [ ] Spostare parsing della risposta tecnica dello scraper.
- [ ] Riutilizzare il process runner canonico; non aggiungere una seconda astrazione shell.
- [ ] Restituire errori contestuali distinguendo unavailable, timeout, invalid response e empty result.
- [ ] Evitare che application gestisca PID, porte, comandi o retry di processo.

### PR2.3 — Estrarre downloader e HLS

- [ ] Creare `internal/infrastructure/artlist/downloader`.
- [ ] Spostare download HTTP/HLS e chiamate FFmpeg.
- [ ] Spostare user-agent, header, retry e timeout di rete.
- [ ] Spostare creazione path temporanei e cleanup.
- [ ] Riutilizzare adapter media/process già presenti.
- [ ] Mantenere in application soltanto la scelta del candidato e della strategia.

### PR2.4 — Separare persistenza e Drive

- [ ] Rimuovere `database/sql` dai use case Artlist.
- [ ] Iniettare repository tramite contratto canonico.
- [ ] Rimuovere Google Drive SDK dal package application.
- [ ] Iniettare l'uploader tramite porta.
- [ ] Usare l'outbox tramite contratto esistente, non tramite repository SQLite concreto.
- [ ] Verificare che `drive_link`, `file_hash` e `local_path` siano aggiornati dallo stesso owner transazionale già definito.

### PR2.5 — Ridurre `application/artlist.Service`

- [ ] Rendere `Service` un orchestratore sottile o sostituirlo con use case specifici.
- [ ] Rimuovere componenti delegati che fanno solo pass-through senza policy.
- [ ] Eliminare setter usati soltanto per completare wiring tardivo.
- [ ] Conservare le strategie `verify`, `skip`, `replace` in un solo punto.
- [ ] Mantenere l'anti-zombie dei job nel job system, non dentro l'adapter scraper.
- [ ] Evitare cache duplicate tra application e scraper infrastructure.

### PR2.6 — Aggiornare provider registry

- [ ] Aggiornare l'adapter Artlist in `internal/application/assets/providers/artlist`.
- [ ] Iniettare la porta `Searcher`, non il service concreto completo.
- [ ] Verificare `CapabilitySearch` e mapping `Candidate`.
- [ ] Eliminare chiamate dirette allo scraper fuori dall'adapter infrastructure.
- [ ] Eliminare switch provider fuori dal registry.

### PR2.7 — Test unitari application

- [ ] Testare ricerca con fake `Searcher`.
- [ ] Testare limite e query forwarding.
- [ ] Testare strategia `verify` con asset già valido.
- [ ] Testare strategia `skip`.
- [ ] Testare strategia `replace`.
- [ ] Testare errore downloader, uploader e repository.
- [ ] Testare che un errore intermedio non produca stato `completed`.
- [ ] Testare deduplicazione e active key senza scraper reale.

### PR2.8 — Test infrastructure

- [ ] Testare parser risposta scraper con fixture.
- [ ] Testare timeout e risposta non valida.
- [ ] Testare costruzione comando Node/Playwright.
- [ ] Testare HLS detection e comando FFmpeg.
- [ ] Testare cleanup file temporanei.
- [ ] Testare download HTTP con server locale.
- [ ] Proteggere eventuale E2E reale con env flag esplicito.

### PR2.9 — Validazione architetturale

- [ ] Eseguire:

```bash
rg 'database/sql|os/exec|google.golang.org/api/drive|internal/infrastructure/database/sqlite' internal/application/artlist
go test ./internal/application/artlist/... -count=1
go test ./internal/infrastructure/artlist/... -count=1
go test ./internal/application/assets/providers/artlist/... -count=1
go test -race ./internal/application/artlist/... ./internal/infrastructure/artlist/...
go vet ./internal/application/artlist/... ./internal/infrastructure/artlist/...
go build ./...
go run ./scripts/archcheck
```

## Exit gate finale

PR2 è completata quando:

- `application/artlist` non gestisce SQL, processi, filesystem, HLS o SDK Drive;
- `infrastructure/artlist` possiede scraper, downloader e integrazioni tecniche;
- le strategie operative restano in un unico owner applicativo;
- provider registry e route mantengono comportamento compatibile;
- nessun percorso legacy o fallback diretto rimane attivo.
