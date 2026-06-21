# PR1 — YouTube infrastructure residua

## Obiettivo

Completare il cutover YouTube lasciando in `internal/application/youtube` soltanto use case, policy e interfacce consumer-side. Download, `yt-dlp`, filesystem, segmentazione, metadata tecnici, Drive e persistenza concreta devono restare dietro adapter infrastructure.

## Stato verificato

Esiste già `internal/infrastructure/youtube` con adapter `yt-dlp` e alcune interfacce. Il cutover non è completo: le porte principali sono dichiarate nel package infrastructure, diversi adapter non risultano collegati, e `application/youtube.Service` continua a conoscere SDK, repository concreti, setter e percorsi di fallback.

## Checklist residua

### PR1.0 — Portare le interfacce nel consumer

- [ ] Definire in `internal/application/youtube/ports.go` soltanto le interfacce realmente consumate.
- [ ] Separare fetch/search, segment extraction, subtitle/transcription, file staging e asset persistence quando hanno consumer differenti.
- [ ] Non esporre `exec.Cmd`, tipi FFmpeg, Google Drive SDK, repository SQLite o configurazioni infrastructure.
- [ ] Mantenere private in infrastructure le strutture raw di `yt-dlp`.
- [ ] Convertire i risultati tecnici una sola volta verso DTO applicativi o `asset.Metadata` canonico.
- [ ] Eliminare interfacce dichiarate ma senza consumer reale.

### PR1.1 — Completare gli adapter concreti

- [ ] Consolidare `YTDLPAdapter` sul process runner canonico, senza mantenere plumbing `os/exec` parallelo.
- [ ] Implementare e collegare le porte effettivamente necessarie per subtitle, Whisper fallback, file staging e segment extraction.
- [ ] Riutilizzare `internal/infrastructure/media/ffmpeg` per probe, encode e segmentazione.
- [ ] Centralizzare cookie, timeout, retry e cleanup temporanei nell'adapter proprietario.
- [ ] Testare parser e command builder con fixture locali senza invocare rete o binari reali.

### PR1.2 — Ridurre `application/youtube.Service`

- [ ] Rimuovere import diretti di Google Drive SDK.
- [ ] Rimuovere import diretti di repository SQLite e outbox SQLite.
- [ ] Rimuovere dipendenze concrete da Ollama, clip indexer, folder memory e video pipeline quando esiste una porta più piccola.
- [ ] Sostituire il costruttore esteso con gruppi coerenti o use case più piccoli, senza creare un generico mega `Dependencies`.
- [ ] Eliminare `SetAssetRepos`, `SetDispatcher`, `SetAssetRepo` e gli altri setter di wiring evitabili.
- [ ] Eliminare il percorso triplo `assetRepo → dispatcher → clipsRepo legacy`.
- [ ] Mantenere un solo writer canonico per asset e un solo percorso di indicizzazione/outbox.
- [ ] Eliminare `fallback_metadata.go` o ridurlo a una policy esplicita senza secondo owner dei metadata.

### PR1.3 — Aggiornare provider e composition root

- [ ] Iniettare gli adapter concreti esclusivamente da `internal/app`.
- [ ] Fare dipendere `internal/application/assets/providers/youtube` dalle porte applicative, non dal service concreto completo.
- [ ] Conservare `SearchProvider` e `FetchProvider` come unici punti di accesso per il registry.
- [ ] Eliminare switch e fallback YouTube fuori dal provider registry.
- [ ] Verificare che il registry venga congelato dopo il wiring completo.

### PR1.4 — Test applicativi

- [ ] Testare ricerca e fetch con fake consumer-side.
- [ ] Testare full video, segment extraction e limiti temporali.
- [ ] Testare metadata incompleti e conversione verso `asset.Metadata`.
- [ ] Testare che errori di download, segmentazione, upload o persistenza non producano asset completati.
- [ ] Testare idempotenza e assenza di doppia indicizzazione.
- [ ] Verificare che nessun unit test application richieda `yt-dlp`, FFmpeg, Drive o SQLite reali.

### PR1.5 — Validazione finale

- [ ] Eseguire:

```bash
rg 'os/exec|database/sql|google.golang.org/api/drive|internal/infrastructure/database/sqlite' internal/application/youtube
rg 'SetAssetRepos|SetDispatcher|SetAssetRepo|clipsRepo legacy' internal/application/youtube
go test ./internal/application/youtube/... -count=1
go test ./internal/infrastructure/youtube/... -count=1
go test ./internal/application/assets/providers/youtube/... -count=1
go test -race ./internal/application/youtube/... ./internal/infrastructure/youtube/...
go vet ./internal/application/youtube/... ./internal/infrastructure/youtube/...
go build ./...
go run ./scripts/archcheck
```

## Exit gate

PR1 è chiusa quando `application/youtube` non esegue processi, non manipola file, non importa SDK o repository concreti, non contiene setter di wiring e usa un solo percorso canonico per asset, metadata e indicizzazione.
