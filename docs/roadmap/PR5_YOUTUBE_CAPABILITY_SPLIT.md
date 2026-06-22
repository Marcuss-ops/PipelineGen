# PR5 — YouTube capability split finale

## Obiettivo

Completare il refactor di `internal/application/youtube` trasformando il package root da mega-servizio con dipendenze piatte a facade minima composta da capability autonome.

Il risultato deve eliminare:

- `ServiceDeps` con oltre 20 dipendenze;
- alias temporanei nel root package;
- ownership condivisa tra search, extraction, metadata, segments, cache, enrichment e jobs;
- use case che dipendono da repository o processi non propri;
- helper generici usati come collante tra capability.

La PR non aggiunge feature YouTube e non modifica route o payload pubblici.

## Stato iniziale verificato

Attualmente:

- `service_orchestrator.go` possiede configurazione, processor, video pipeline, lifecycle, resolver, tre repository e numerose porte;
- `ports.go` re-esporta con alias i tipi canonici di `youtube/ports` e `youtube/metadata`;
- `types.go` re-esporta `Segment` da `youtube/types`;
- esistono già `metadata/`, `segments/`, `jobs/`, `ports/`, `types/` e `tagutil/`;
- alcune directory capability sono state rimosse perché contenevano soltanto `doc.go`;
- il lavoro residuo non è creare directory vuote: è spostare use case reali e ridurre il wiring.

## Branch e commit

Branch:

```text
codex/youtube-capability-split-final
```

Sequenza suggerita:

```text
refactor(youtube): extract metadata service
refactor(youtube): extract search service
refactor(youtube): extract extraction and segment services
refactor(youtube): extract cache enrichment and jobs services
refactor(youtube): reduce root facade and composition wiring
refactor(youtube): remove root compatibility aliases
```

Ogni commit deve compilare e passare i test YouTube. Non accumulare tutti gli spostamenti in un singolo commit finale.

## Scope consentito

Percorsi principali:

```text
internal/application/youtube/**
internal/infrastructure/youtube/**
internal/app/composition.go
internal/app/youtube_adapters.go
internal/app/modules/youtube.go
internal/api/sources/youtube/**
internal/api/sources/module_youtubeclip.go
internal/application/assets/providers/youtube/**
internal/infrastructure/database/sqlite/assets/youtube_cache.go
architecture/migration.yaml
scripts/archcheck/**
docs/roadmap/PR5_YOUTUBE_CAPABILITY_SPLIT.md
```

I consumer esterni al perimetro possono essere modificati soltanto per import, costruttori e tipi canonici.

## Fuori scope

- nuove route;
- modifiche ai payload JSON;
- nuove strategie di segmentazione;
- cambio di yt-dlp o FFmpeg;
- nuovo database cache;
- nuovo provider registry;
- modifica dello scoring;
- modifica di Qdrant;
- refactor Artlist;
- migrazione generale di `internal/media`;
- sostituzione del job system.

## Regole architetturali

1. Ogni capability possiede il proprio use case.
2. Ogni capability definisce porte piccole vicino al consumer.
3. Infrastructure implementa processi, filesystem, HTTP, SDK e SQL.
4. API importa application, mai infrastructure.
5. Il composition root costruisce adapter concreti una sola volta.
6. Nessuna capability sorella importa un'altra capability per raggiungere un adapter.
7. Tipi condivisi soltanto in `youtube/types`, `youtube/ports` o domain quando realmente stabili.
8. Nessun package `common`, `shared`, `helpers` o `utils` generico creato per evitare una decisione di ownership.
9. Massimo 8–10 dipendenze per builder o `Deps`.
10. Nessun alias lasciato “temporaneamente” senza task di rimozione nella stessa PR.

## Fase 0 — Inventario e mappa dei simboli

Prima di modificare codice:

```bash
find internal/application/youtube -type f -name '*.go' | sort
rg '^type |^func ' internal/application/youtube
rg 'internal/application/youtube' --type go
rg 'type .* =' internal/application/youtube --type go
```

Creare nel corpo della PR una tabella:

| File/simbolo | Capability | Dipendenze attuali | Destinazione | Test esistente |
|---|---|---|---|---|
| metadata | metadata | MetaFetcher, cache | `youtube/metadata` | sì/no |
| search | search | SearchRunner, cache, metadata | `youtube/search` | sì/no |
| extract | extraction | VideoPipeline, lifecycle, resolver | `youtube/extraction` | sì/no |
| segment | segments | clip files, hash, lifecycle | `youtube/segments` | sì/no |
| enrichment | enrichment | Ollama, folder memory, indexer | `youtube/enrichment` | sì/no |
| cache | cache | YouTubeCacheStore | `youtube/cache` | sì/no |
| jobs | jobs | jobs service, codecs | `youtube/jobs` | sì/no |

Checklist:

- [ ] elencare tutti i file production root;
- [ ] elencare tutti i campi di `ServiceDeps`;
- [ ] associare ogni campo a una sola capability;
- [ ] identificare campi usati da più capability;
- [ ] classificare gli alias come pubblici necessari o compatibilità interna;
- [ ] trovare process execution, SQL e filesystem nell'application;
- [ ] trovare dipendenze opzionali mai valorizzate;
- [ ] trovare porte marker vuote.

## Fase 1 — Metadata service

Destinazione:

```text
internal/application/youtube/metadata/
```

Responsabilità:

- richiedere metadata attraverso `VideoMetadataFetcherPort`;
- normalizzare il DTO applicativo;
- coordinare cache metadata;
- validare campi obbligatori;
- propagare context cancellation;
- restituire errori espliciti.

Non deve:

- eseguire yt-dlp direttamente;
- leggere file VTT;
- scrivere SQL;
- conoscere Gin;
- gestire job.

Struttura indicativa:

```text
metadata/
├── service.go
├── ports.go
├── types.go
├── validate.go
├── service_test.go
└── mapping_test.go
```

TODO:

- [ ] spostare il vero use case metadata, non soltanto i tipi;
- [ ] tenere parsing yt-dlp in `internal/infrastructure/youtube`;
- [ ] usare un solo DTO canonico;
- [ ] rimuovere mapping duplicati;
- [ ] testare thumbnails, chapters, lingua e campi vuoti;
- [ ] testare `ctx.Err()` prima e durante la chiamata;
- [ ] testare errore del fetcher;
- [ ] testare cache hit e miss.

Exit locale:

```bash
go test ./internal/application/youtube/metadata/...
go test ./internal/infrastructure/youtube/...
```

## Fase 2 — Search service

Destinazione:

```text
internal/application/youtube/search/
```

Responsabilità:

- orchestrare la ricerca live;
- applicare limite, sort e validazione input;
- usare `SearchRunnerPort`;
- usare cache attraverso porta;
- richiedere metadata attraverso interfaccia stretta quando necessario.

Non deve:

- restituire successo vuoto quando il runner manca;
- eseguire processi;
- interrogare SQLite direttamente;
- importare API;
- creare provider.

Struttura indicativa:

```text
search/
├── service.go
├── ports.go
├── request.go
├── result.go
└── service_test.go
```

TODO:

- [ ] estrarre il use case da root;
- [ ] eliminare dipendenze non usate dalla ricerca;
- [ ] rendere esplicito l'errore “runner non configurato”;
- [ ] conservare timeout e cancellazione;
- [ ] aggiornare provider adapter YouTube;
- [ ] aggiornare gli handler senza cambiare route;
- [ ] testare input vuoto;
- [ ] testare limite massimo;
- [ ] testare risultato vuoto reale;
- [ ] testare cancellation;
- [ ] testare errore provider;
- [ ] testare cache.

Exit locale:

```bash
go test ./internal/application/youtube/search/...
go test ./internal/application/assets/providers/youtube/...
```

## Fase 3 — Extraction service

Destinazione:

```text
internal/application/youtube/extraction/
```

Responsabilità:

- coordinare download e cut;
- ricevere una richiesta applicativa;
- invocare `VideoPipeline`;
- delegare segment processing al servizio segments;
- coordinare lifecycle e destination resolver;
- restituire risultato applicativo.

Infrastructure possiede:

- `os/exec`;
- yt-dlp;
- FFmpeg;
- filesystem;
- parsing VTT concreto;
- gestione processi.

TODO:

- [ ] spostare `ExtractRequest` e `ExtractResponse` nel package corretto o in types canonici;
- [ ] creare `extraction.Deps` con massimo 8–10 campi;
- [ ] ricevere `segments.Service` come capability, non le sue dipendenze interne;
- [ ] eliminare accessi diretti a cache, indexer e Ollama se non appartengono all'extraction;
- [ ] propagare timeout;
- [ ] garantire cleanup file temporanei;
- [ ] conservare nomi e formato output;
- [ ] testare download fallito;
- [ ] testare cut fallito;
- [ ] testare file pre-scaricato;
- [ ] testare cancellation;
- [ ] testare risultato parziale.

## Fase 4 — Segments service

Destinazione:

```text
internal/application/youtube/segments/
```

Responsabilità:

- validare start/end;
- generare filename;
- verificare cache file;
- calcolare hash;
- costruire metadata lifecycle;
- persistere attraverso writer canonico;
- opzionalmente invocare indexer tramite porta.

TODO:

- [ ] trasformare gli helper già estratti in un servizio coeso;
- [ ] evitare che extraction acceda direttamente a hash, clip files, indexer e cache;
- [ ] definire porte piccole;
- [ ] usare `types.Segment` direttamente;
- [ ] eliminare l'alias `youtube.Segment` dopo migrazione caller;
- [ ] testare timestamp invalidi;
- [ ] testare finestra fuori durata;
- [ ] testare cache valida;
- [ ] testare cache corrotta;
- [ ] testare hash error;
- [ ] testare indexer nil e typed-nil;
- [ ] testare persistenza fallita.

## Fase 5 — Enrichment service

Destinazione:

```text
internal/application/youtube/enrichment/
```

Responsabilità:

- chiamate Ollama;
- dedup semantico;
- enrichment deterministico;
- folder memory tramite porta;
- update metadata tramite writer canonico.

TODO:

- [ ] spostare intelligence ed enrichment dal root;
- [ ] evitare duplicazione con asset resolver e clip resolver;
- [ ] separare funzioni pure da chiamate LLM;
- [ ] riutilizzare il client Ollama esistente;
- [ ] imporre semaforo e timeout nel livello corretto;
- [ ] testare fallback senza LLM;
- [ ] testare risposta malformata;
- [ ] testare duplicato;
- [ ] testare cancellation.

## Fase 6 — Cache service

Destinazione:

```text
internal/application/youtube/cache/
```

Responsabilità:

- policy TTL;
- chiavi cache;
- validazione del contenuto;
- hit/miss applicativo.

Infrastructure database mantiene la row SQLite e il repository concreto.

TODO:

- [ ] eliminare DTO database dall'application;
- [ ] definire `Reader` e `Writer` soltanto se entrambi necessari;
- [ ] evitare un'interfaccia cache globale;
- [ ] testare TTL scaduto;
- [ ] testare record corrotto;
- [ ] testare miss;
- [ ] testare errore repository.

## Fase 7 — Jobs service

Destinazione:

```text
internal/application/youtube/jobs/
```

Responsabilità:

- payload codec;
- handler job;
- traduzione payload → use case;
- progress reporting;
- idempotency key.

TODO:

- [ ] spostare job registration e codec;
- [ ] usare il job registry comune;
- [ ] non creare dispatcher YouTube;
- [ ] non duplicare extraction logic;
- [ ] testare encode/decode;
- [ ] testare payload version;
- [ ] testare retry;
- [ ] testare cancellation;
- [ ] testare job duplicato.

## Fase 8 — Facade root minima

Target indicativo:

```go
type Service struct {
    Metadata   *metadata.Service
    Search     *search.Service
    Extraction *extraction.Service
    Enrichment *enrichment.Service
}
```

La forma esatta dipende dai caller. La facade può anche essere eliminata se non esiste un use case che richiede più capability.

Regole:

- massimo 4–6 capability nella facade;
- nessun repository concreto;
- nessuna porta di basso livello;
- nessun semaforo condiviso se appartiene a una capability;
- nessun accesso diretto a cache, hash o filesystem;
- nessun metodo che inoltra soltanto una chiamata senza valore.

TODO:

- [ ] ridurre o eliminare `ServiceDeps` root;
- [ ] creare builder capability in `internal/app/modules/youtube.go`;
- [ ] assicurare massimo 8–10 dipendenze per builder;
- [ ] aggiornare `BuildDomainBundle`;
- [ ] rimuovere wiring duplicato da `youtube_adapters.go`;
- [ ] aggiornare compile-time assertions.

## Fase 9 — Rimozione alias e shim

Ricerca:

```bash
rg '^\s*type\s+\w+\s*=' internal/application/youtube --type go
```

TODO:

- [ ] migrare tutti i caller a `youtube/ports`;
- [ ] migrare tutti i caller a `youtube/types`;
- [ ] migrare tutti i caller a `youtube/metadata`;
- [ ] eliminare `internal/application/youtube/ports.go` quando vuoto;
- [ ] eliminare alias `Segment` dal root;
- [ ] eliminare alias interni a tagutil;
- [ ] non aggiornare baseline per mantenere alias.

Risultato atteso:

```text
zero alias di compatibilità nel root YouTube
```

## Test obbligatori

Durante ogni fase:

```bash
go test ./internal/application/youtube/...
go test ./internal/infrastructure/youtube/...
go test ./internal/app/...
```

Prima della PR:

```bash
gofmt -w internal/application/youtube internal/infrastructure/youtube internal/app
go test -race ./internal/application/youtube/...
go test ./internal/infrastructure/youtube/...
go vet ./internal/application/youtube/...
go vet ./internal/infrastructure/youtube/...
go build ./cmd/server
go build ./cmd/worker
go run ./scripts/archcheck
bash scripts/ci-architectural-checks.sh
```

## Controlli strutturali

```bash
find internal/application/youtube -maxdepth 1 -name '*.go' ! -name '*_test.go' | wc -l
rg 'os/exec|exec.Command|database/sql|gin-gonic' internal/application/youtube
rg '^\s*type\s+\w+\s*=' internal/application/youtube --type go
rg 'ServiceDeps' internal/application/youtube internal/app --type go
```

Target:

- root package massimo 5–8 file production;
- zero `os/exec` nell'application;
- zero SQL nell'application;
- zero Gin nell'application;
- zero alias compatibility;
- nessun `Deps` oltre 10 campi;
- ogni capability con test propri.

## Exit gate finale

PR5 è `done` quando:

- [ ] capability reali separate;
- [ ] root YouTube piccolo;
- [ ] `ServiceDeps` root eliminato o ridotto sotto 10 campi;
- [ ] alias root eliminati;
- [ ] route e payload invariati;
- [ ] test YouTube e infrastructure verdi;
- [ ] race test verde;
- [ ] server e worker compilano;
- [ ] archcheck non aumenta violazioni;
- [ ] documentazione aggiornata;
- [ ] CI verde sulla PR;
- [ ] verifica rieseguita su `main` dopo merge.

## Rollback

Il rollback corretto è il revert dell'intera PR o del singolo commit capability.

Non accettare rollback parziali che lasciano:

- due facade;
- due porte canoniche;
- alias vecchio → nuovo;
- metà caller sul root e metà sul sottopackage;
- doppia cache;
- doppia registrazione job.

Se un commit diventa troppo ampio, dividerlo per capability mantenendo ogni commit compilabile.
