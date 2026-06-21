# PR1 — Separazione YouTube infrastructure

## Obiettivo

Lasciare in `internal/application/youtube` soltanto orchestrazione, use case e porte. Spostare download, `yt-dlp`, FFmpeg, filesystem, cookie retry, metadata native e segmentazione in `internal/infrastructure/youtube`.

Questa PR non cambia le route HTTP e non aggiunge funzionalità YouTube.

## Stato iniziale verificato

`internal/application/youtube` contiene ancora dipendenze concrete verso:

- Google Drive SDK;
- repository SQLite;
- outbox SQLite;
- filesystem;
- processi esterni;
- video pipeline e FFmpeg indiretti;
- package ancora presenti sotto `internal/media`.

## Struttura target

```text
internal/application/youtube/
  service.go
  usecase_*.go
  ports.go
  requests.go
  results.go

internal/infrastructure/youtube/
  downloader.go
  metadata.go
  extractor.go
  segment.go
  cookies.go
  process.go
```

I nomi definitivi possono seguire i file esistenti, ma la responsabilità deve rispettare questa separazione.

## Checklist operativa

### PR1.0 — Inventario delle responsabilità

- [ ] Elencare tutti i file in `internal/application/youtube`.
- [ ] Per ogni file, classificare la responsabilità:
  - use case;
  - dominio applicativo;
  - porta;
  - adapter esterno;
  - processo/FFmpeg;
  - filesystem;
  - persistenza.
- [ ] Cercare tutti gli import di:

```bash
rg 'os/exec|yt-dlp|ffmpeg|database/sql|google.golang.org/api/drive|internal/infrastructure|internal/media' internal/application/youtube
```

- [ ] Identificare tutti i costruttori e setter usati dal composition root.
- [ ] Identificare i test che istanziano direttamente implementazioni concrete.

**Accettazione PR1.0**

Ogni file ha un proprietario target esplicito. Nessun file viene spostato prima di questa classificazione.

### PR1.1 — Definire porte applicative minime

- [ ] Creare in `internal/application/youtube/ports.go` solo le interfacce realmente usate.
- [ ] Definire una porta per il fetch/download video.
- [ ] Definire una porta per metadata remoti se separata dal fetch.
- [ ] Definire una porta per estrazione/taglio segmento.
- [ ] Definire una porta per storage/Drive soltanto se il use case deve coordinare il salvataggio.
- [ ] Usare DTO applicativi piccoli; non esporre tipi FFmpeg, `exec.Cmd`, SDK Drive o righe SQLite.
- [ ] Non creare una singola interfaccia “YouTubeClient” con decine di metodi.

Esempio di forma accettabile:

```go
type VideoFetcher interface {
    Fetch(context.Context, FetchRequest) (FetchedVideo, error)
}

type SegmentExtractor interface {
    Extract(context.Context, SegmentRequest) (SegmentResult, error)
}
```

**Accettazione PR1.1**

`internal/application/youtube` può essere testato con fake in memoria senza invocare rete, Drive, SQLite o processi esterni.

### PR1.2 — Estrarre processi e download

- [ ] Creare `internal/infrastructure/youtube`.
- [ ] Spostare l'esecuzione di `yt-dlp` nel package infrastructure.
- [ ] Spostare gestione cookie e retry legati a `yt-dlp` nel package infrastructure.
- [ ] Spostare creazione e pulizia file temporanei nel package infrastructure.
- [ ] Spostare parsing output di processi esterni nel package infrastructure.
- [ ] Usare il runner di processo canonico già presente nel repository; non introdurre un secondo wrapper `os/exec`.
- [ ] Restituire errori tipizzati o wrapping contestuale, senza stringhe interpretate dal use case.

### PR1.3 — Estrarre segmentazione e FFmpeg

- [ ] Spostare il taglio video e la costruzione degli argomenti FFmpeg in infrastructure.
- [ ] Riutilizzare `internal/infrastructure/media/ffmpeg` o `pkg/platform` dove già canonico.
- [ ] Non duplicare probe, encode o gestione timeout.
- [ ] Tradurre i risultati concreti in DTO applicativi prima di restituirli.
- [ ] Coprire i limiti di segmento: start negativo, end precedente allo start, durata zero, full-video sentinel.

### PR1.4 — Estrarre metadata e filesystem

- [ ] Spostare letture di metadata native e manifest concrete in infrastructure.
- [ ] Spostare accesso diretto a path, file temporanei e directory di output in infrastructure.
- [ ] Mantenere in application soltanto regole come “quale asset registrare” o “quale step eseguire dopo il fetch”.
- [ ] Non mantenere campi path-specific nei DTO se non necessari al use case.

### PR1.5 — Ridurre `application/youtube.Service`

- [ ] Sostituire dipendenze concrete con le porte definite in PR1.1.
- [ ] Rimuovere import diretti di Google Drive SDK dal service applicativo.
- [ ] Rimuovere import diretti di repository SQLite dal service applicativo quando esiste già una porta di dominio.
- [ ] Rimuovere accesso diretto a outbox SQLite; usare un contratto applicativo o di dominio esistente.
- [ ] Rimuovere setter usati soltanto per late binding se la dipendenza può essere richiesta dal costruttore.
- [ ] Conservare setter solo per lifecycle realmente ciclici e documentati.

### PR1.6 — Aggiornare provider adapter e wiring

- [ ] Aggiornare `internal/application/assets/providers/youtube` per dipendere dalle porte corrette.
- [ ] Costruire gli adapter concreti esclusivamente in `internal/app`.
- [ ] Iniettare l'implementazione `infrastructure/youtube` nel service/application adapter.
- [ ] Verificare che `SearchProvider` e `FetchProvider` mantengano capability e payload esistenti.
- [ ] Eliminare fallback verso vecchi service concreti.

### PR1.7 — Test unitari

- [ ] Testare use case YouTube con fake `VideoFetcher`.
- [ ] Testare propagazione errori del fetcher.
- [ ] Testare full video e segment extraction.
- [ ] Testare metadata nil o incompleti.
- [ ] Testare che un errore dopo il download non registri un asset completato.
- [ ] Testare che nessun unit test application richieda `yt-dlp`, FFmpeg o Drive reali.

### PR1.8 — Test infrastructure

- [ ] Testare costruzione argomenti `yt-dlp` senza eseguire il binario.
- [ ] Testare costruzione argomenti FFmpeg.
- [ ] Testare parser metadata con fixture locale.
- [ ] Testare cleanup dei file temporanei.
- [ ] Aggiungere integration test opzionale, protetto da env flag, per binari esterni.

### PR1.9 — Validazione architetturale

- [ ] Eseguire:

```bash
rg 'os/exec|database/sql|google.golang.org/api/drive|internal/infrastructure/database/sqlite' internal/application/youtube
go test ./internal/application/youtube/... -count=1
go test ./internal/infrastructure/youtube/... -count=1
go test ./internal/application/assets/providers/youtube/... -count=1
go test -race ./internal/application/youtube/... ./internal/infrastructure/youtube/...
go vet ./internal/application/youtube/... ./internal/infrastructure/youtube/...
go build ./...
go run ./scripts/archcheck
```

## Exit gate finale

PR1 è completata quando:

- `application/youtube` non esegue processi, non manipola file e non importa SDK concreti;
- `infrastructure/youtube` possiede download, metadata, segmentazione e integrazione processi;
- provider e route pubbliche mantengono comportamento compatibile;
- i test applicativi usano fake e i test infrastructure coprono adapter concreti;
- non esistono wrapper o fallback legacy introdotti per mantenere due percorsi.
