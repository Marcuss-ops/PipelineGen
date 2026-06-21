# Refactor Complete — Piano operativo in quattro PR

> Documento operativo per alleggerire PipelineGen senza rimuovere funzionalità, senza introdurre nuovi layer duplicati e senza trasformare il refactor in una modifica monolitica.
>
> Stato iniziale di riferimento: branch `main`, giugno 2026.
>
> Regola principale: ogni PR deve partire da `origin/main` aggiornato, affrontare un solo problema, aggiornare i test del blocco toccato, non introdurre nuove feature e non modificare file fuori scope.

---

## 1. Obiettivo generale

Il refactor deve ridurre quattro tipi di peso:

1. **peso delle dipendenze e delle immagini Docker**;
2. **peso del deployment monolitico**;
3. **peso dei namespace legacy e delle responsabilità collocate nel layer sbagliato**;
4. **peso del mega-package YouTube**.

Il risultato finale deve conservare:

- le route HTTP pubbliche;
- i payload esistenti;
- il job system unificato;
- il provider registry comune;
- il database SQLite canonico;
- le integrazioni Drive, Qdrant, Ollama, yt-dlp e FFmpeg;
- la possibilità di eseguire server e worker nello stesso ambiente;
- la compatibilità operativa degli script realmente usati in produzione.

Il refactor non deve:

- aggiungere nuove funzionalità;
- creare nuove interfacce senza un consumatore reale;
- creare wrapper pass-through o alias temporanei;
- duplicare logica tra application, infrastructure e API;
- spostare file senza correggere ownership e dipendenze;
- mischiare cleanup documentale, feature e refactor non collegati;
- introdurre una nuova mega-struct o un service locator mascherato;
- aggiungere nuovi registry paralleli al provider registry esistente.

---

## 2. Ordine obbligatorio delle PR

Le PR devono essere eseguite in questo ordine:

| PR | Titolo | Dipende da |
|---|---|---|
| PR1 | Alleggerimento Node scraper | nessuna |
| PR2 | Split Docker server, worker e admin | PR1 |
| PR3 | Estrazione di vectorstore e storage da `internal/media` | PR2 |
| PR4 | Split del mega-package `internal/application/youtube` | PR3 |

Motivazione dell'ordine:

- PR1 riduce il peso immediato del sidecar Artlist senza toccare il backend Go.
- PR2 separa i runtime prima di spostare altre responsabilità.
- PR3 elimina due adapter infrastrutturali dal namespace legacy.
- PR4 viene eseguita per ultima perché il package YouTube dipende da processi, media pipeline, cache, metadata e storage e deve lavorare contro confini già più puliti.

---

## 3. Workflow Git obbligatorio per ogni PR

Ogni PR deve seguire questo flusso:

```bash
git fetch origin
git checkout main
git pull --ff-only origin main
git checkout -b codex/<nome-pr>
```

Durante il lavoro:

```bash
git status -sb
git diff
```

Prima del commit:

```bash
git fetch origin
git rebase origin/main
git status -sb
```

Dopo i test:

```bash
git add <solo-file-della-pr>
git commit -m "<tipo>(<scope>): <descrizione>"
git push origin codex/<nome-pr>
git log -n 5 --oneline
```

Regole:

- non pushare direttamente su `main`;
- non usare branch vecchi o già molto indietro rispetto a `origin/main`;
- non committare `node_modules`, output, database, profili Chrome, cache o file generati;
- controllare il diff remoto prima di modificare file toccati recentemente da altre PR;
- non aggiornare `baseline.json` per nascondere nuove violazioni;
- il branch deve contenere soltanto i file dichiarati nello scope;
- se una PR richiede un file fuori scope, fermarsi e correggere il piano prima di continuare;
- dopo il merge controllare gli ultimi commit su `main`.

---

# PR1 — Alleggerimento del Node scraper

## 4. Obiettivo

Rimuovere la dipendenza duplicata `puppeteer`, mantenere `puppeteer-core` come unica libreria browser e migliorare il caching della build Docker del sidecar Artlist.

Questa PR deve essere piccola, isolata e senza modifiche al comportamento dello scraper.

## 5. Branch e commit

Branch:

```text
codex/refactor-node-scraper-weight
```

Commit suggeriti:

```text
chore(node-scraper): remove redundant puppeteer dependency
build(node-scraper): improve docker layer caching
```

È preferibile usare due commit distinti perché la rimozione della dipendenza e la modifica Docker hanno rischi differenti.

## 6. Scope consentito

File consentiti:

```text
node-scraper/package.json
node-scraper/package-lock.json
node-scraper/Dockerfile.scraper
node-scraper/.dockerignore
```

File leggibili ma da non modificare salvo errore dimostrabile:

```text
node-scraper/src/artlist/browser.js
node-scraper/artlist_server.js
node-scraper/artlist_search.js
docker-compose.yml
```

Fuori scope:

- codice Go Artlist;
- nuove route;
- modifiche al protocollo HTTP del sidecar;
- cambio porta;
- cambio del browser provider;
- introduzione di Playwright;
- refactor JavaScript generale;
- modifica della cache SQLite dello scraper.

## 7. TODO operative

### 7.1 Verificare l'import reale

- [ ] Cercare tutti gli import di `puppeteer` e `puppeteer-core`.
- [ ] Confermare che il codice runtime importa soltanto `puppeteer-core`.
- [ ] Verificare che non esistano script manuali che richiedono il package completo `puppeteer`.
- [ ] Annotare nel corpo della PR il risultato della ricerca.

Comandi:

```bash
rg "from ['\"]puppeteer['\"]|require\(['\"]puppeteer['\"]" node-scraper
rg "puppeteer-core" node-scraper
```

### 7.2 Rimuovere la dipendenza duplicata

- [ ] Rimuovere `puppeteer` da `node-scraper/package.json`.
- [ ] Conservare `puppeteer-core`.
- [ ] Rigenerare il lockfile con la stessa versione di Node prevista dal progetto.
- [ ] Non modificare versioni non collegate.
- [ ] Verificare che il lockfile non contenga più la dipendenza root `puppeteer`.

Comando:

```bash
cd node-scraper
npm install --package-lock-only
npm ci
```

Controlli:

```bash
npm ls puppeteer puppeteer-core
npm audit --omit=dev
```

Il risultato atteso è che `puppeteer-core` resti installato e `puppeteer` non sia più una dipendenza diretta.

### 7.3 Rendere la build Docker cache-friendly

Il Dockerfile non deve copiare tutto il contesto prima dell'installazione delle dipendenze.

Ordine richiesto:

```dockerfile
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY . /app/
```

TODO:

- [ ] Sostituire `npm install --omit=dev` con `npm ci --omit=dev`.
- [ ] Copiare `package.json` e `package-lock.json` prima del codice.
- [ ] Copiare i sorgenti soltanto dopo l'installazione.
- [ ] Aggiungere `ENV PUPPETEER_SKIP_DOWNLOAD=true` come protezione esplicita.
- [ ] Non installare Chromium dentro questa PR.
- [ ] Non cambiare `CMD`, porta o healthcheck.

### 7.4 Aggiungere `.dockerignore`

Creare `node-scraper/.dockerignore` con almeno:

```text
node_modules
npm-debug.log
*.log
*.sqlite
*.sqlite-*
*.db
profiles
sessions
logs
tmp
coverage
.git
```

TODO:

- [ ] Escludere dipendenze locali.
- [ ] Escludere profili browser e sessioni.
- [ ] Escludere database e cache runtime.
- [ ] Non escludere i file JavaScript usati dal container.
- [ ] Non escludere `package-lock.json`.

### 7.5 Verificare il comportamento runtime

- [ ] Avviare il server Node fuori da Docker.
- [ ] Verificare `/health`.
- [ ] Eseguire almeno una ricerca Artlist in modalità mock o ambiente autorizzato.
- [ ] Verificare il collegamento a browser remoto quando `BROWSER_WS` è valorizzato.
- [ ] Verificare il percorso locale quando `CHROME_EXECUTABLE` punta a un browser disponibile.
- [ ] Controllare la chiusura di page, context e browser.

Comandi minimi:

```bash
cd node-scraper
node --check artlist_server.js
node --check artlist_search.js
npm test --if-present
```

Build:

```bash
docker build -f node-scraper/Dockerfile.scraper node-scraper -t pipelinegen-artlist:refactor-pr1
```

Smoke test:

```bash
docker run --rm -p 9123:9123 pipelinegen-artlist:refactor-pr1
curl -fsS http://127.0.0.1:9123/health
```

## 8. Exit gate PR1

La PR è completata soltanto quando:

- [ ] `puppeteer` non è più una dipendenza diretta;
- [ ] `puppeteer-core` resta installato;
- [ ] `npm ci --omit=dev` termina con successo;
- [ ] il container viene costruito;
- [ ] `/health` risponde correttamente;
- [ ] nessun file runtime è escluso per errore dal `.dockerignore`;
- [ ] il diff contiene soltanto i quattro file ammessi;
- [ ] non cambia il contratto HTTP dello scraper.

## 9. Rollback PR1

In caso di regressione:

1. ripristinare `puppeteer` nel `package.json`;
2. rigenerare il lockfile;
3. non rimuovere la `.dockerignore` se non è la causa;
4. non compensare installando un browser aggiuntivo senza prima identificare il percorso runtime realmente usato.

---

# PR2 — Split Docker server, worker e admin

## 10. Obiettivo

Separare i runtime Docker per evitare che il server HTTP includa strumenti pesanti necessari soltanto ai worker o agli operatori.

Il risultato deve produrre tre target:

```text
pipelinegen-server
pipelinegen-worker
pipelinegen-admin
```

La PR non deve cambiare la logica Go dei tre binari.

## 11. Branch e commit

Branch:

```text
codex/refactor-docker-targets
```

Commit suggeriti:

```text
build(docker): add dedicated server worker and admin targets
build(compose): run pipelinegen with separated runtime roles
```

## 12. Scope consentito

File principali:

```text
Dockerfile
.dockerignore
docker-compose.yml
```

File aggiuntivi ammessi soltanto se necessari per documentare i comandi:

```text
README.md
ARCHITECTURE.md
docs/ops-audit.md
```

Fuori scope:

- modifiche ai binari Go;
- modifiche al job protocol;
- nuova coda;
- nuova infrastruttura cloud;
- sostituzione di SQLite;
- sostituzione di Qdrant;
- modifica delle route;
- modifica dei payload;
- refactor del composition root.

## 13. Design richiesto

### 13.1 Builder condiviso

Il builder deve continuare a compilare i tre binari con:

```text
-trimpath
-ldflags "-s -w ..."
```

Output:

```text
/out/pipelinegen
/out/pipelinegen-worker
/out/pipelinegen-admin
```

### 13.2 Runtime server

Deve contenere soltanto ciò che il server HTTP usa realmente.

Base suggerita:

```dockerfile
FROM debian:bookworm-slim AS server-runtime
```

Pacchetti iniziali ammessi:

```text
ca-certificates
curl
```

Aggiungere altri pacchetti soltanto se un test runtime dimostra che sono necessari.

Il server non deve contenere per default:

```text
ffmpeg
yt-dlp
sqlite3 CLI
jq
python3
pipelinegen-worker
pipelinegen-admin
```

Entry point:

```dockerfile
ENTRYPOINT ["/usr/local/bin/pipelinegen"]
CMD ["--mode", "http"]
```

### 13.3 Runtime worker

Deve contenere:

```text
ca-certificates
curl
ffmpeg
python3
yt-dlp
```

Aggiungere `jq` o `sqlite3` soltanto se una chiamata runtime reale li richiede.

Entry point:

```dockerfile
ENTRYPOINT ["/usr/local/bin/pipelinegen-worker"]
```

Il comando esatto deve essere verificato contro `cmd/worker` e non inventato.

### 13.4 Runtime admin

Deve contenere:

```text
ca-certificates
sqlite3
jq
```

Python o FFmpeg devono essere aggiunti soltanto se un comando admin li usa veramente.

Entry point:

```dockerfile
ENTRYPOINT ["/usr/local/bin/pipelinegen-admin"]
```

### 13.5 Compatibilità immagine `runtime`

Per evitare rotture immediate, la PR può mantenere temporaneamente un target finale compatibile chiamato `runtime`, purché sia un alias esplicito del target corretto e non duplichi pacchetti o build.

Non creare quattro immagini indipendenti con quattro copie della stessa logica.

## 14. TODO operative

### 14.1 Audit dei comandi runtime

- [ ] Cercare gli usi di `ffmpeg`, `yt-dlp`, `python3`, `jq` e `sqlite3`.
- [ ] Classificare ogni uso come server, worker, admin o script esterno.
- [ ] Inserire la tabella risultante nel corpo della PR.
- [ ] Non rimuovere un pacchetto basandosi soltanto su supposizioni.

Comandi:

```bash
rg "ffmpeg|yt-dlp|python3|sqlite3|\bjq\b" cmd internal scripts
```

### 14.2 Creare `.dockerignore` root

Contenuto minimo:

```text
.git
.github
node-scraper/node_modules
data
logs
tmp
scratch
coverage.out
coverage.html
*.db
*.sqlite
*.sqlite-*
*.log
*.bak
profiles
sessions
deliverables
```

Non escludere:

```text
go.mod
go.sum
cmd
internal
pkg
migrations
config
scripts realmente copiati nell'immagine
```

### 14.3 Creare i target Docker

- [ ] Conservare un unico builder.
- [ ] Creare target runtime separati.
- [ ] Copiare in ogni target un solo binario.
- [ ] Installare pacchetti specifici del ruolo.
- [ ] Conservare healthcheck soltanto sul server.
- [ ] Impostare un utente non root se compatibile con volume e tool esterni.
- [ ] Non copiare l'intera repo nei runtime finali.
- [ ] Copiare gli script Python nel worker soltanto se richiesti dal suo funzionamento.

### 14.4 Aggiornare Compose

Compose deve separare almeno:

```text
pipelinegen-server
pipelinegen-worker
qdrant
artlist-scraper
```

TODO:

- [ ] Server con target `server-runtime`.
- [ ] Worker con target `worker-runtime`.
- [ ] Server dipendente dai servizi strettamente necessari.
- [ ] Worker collegato agli stessi volumi e servizi necessari.
- [ ] Non esporre la porta del worker.
- [ ] Conservare `/health` sul server.
- [ ] Evitare `container_name` duplicati.
- [ ] Verificare la condivisione del database SQLite e le modalità di lock/WAL.
- [ ] Verificare che due processi separati siano già supportati dalla configurazione del job system.
- [ ] Non avviare il server in `--mode all` dopo aver aggiunto il worker separato.

### 14.5 Build dei tre target

```bash
docker build --target server-runtime -t pipelinegen-server:refactor-pr2 .
docker build --target worker-runtime -t pipelinegen-worker:refactor-pr2 .
docker build --target admin-runtime -t pipelinegen-admin:refactor-pr2 .
```

### 14.6 Smoke test

- [ ] Server avviato in modalità HTTP.
- [ ] `/health` verde.
- [ ] Worker avviato senza esporre porte.
- [ ] Admin esegue almeno `--help` o un comando read-only.
- [ ] Server non contiene `ffmpeg`, `yt-dlp` e Python, salvo necessità documentata.
- [ ] Worker trova `ffmpeg`, `yt-dlp` e Python.

Esempi:

```bash
docker run --rm pipelinegen-server:refactor-pr2 --help
docker run --rm pipelinegen-worker:refactor-pr2 --help
docker run --rm pipelinegen-admin:refactor-pr2 --help
```

### 14.7 Verifica dimensioni

Registrare nel corpo della PR:

```bash
docker image inspect pipelinegen-server:refactor-pr2 --format='{{.Size}}'
docker image inspect pipelinegen-worker:refactor-pr2 --format='{{.Size}}'
docker image inspect pipelinegen-admin:refactor-pr2 --format='{{.Size}}'
```

La PR non richiede una percentuale minima arbitraria, ma il server deve essere chiaramente più piccolo del runtime monolitico precedente.

## 15. Exit gate PR2

- [ ] Un solo builder.
- [ ] Tre target runtime.
- [ ] Un solo binario per target.
- [ ] Server senza tool media non necessari.
- [ ] Worker con tool media necessari.
- [ ] Admin indipendente.
- [ ] Compose non esegue contemporaneamente worker interno e worker separato.
- [ ] Healthcheck server verde.
- [ ] Build dei tre target verde.
- [ ] `go build ./cmd/server ./cmd/worker ./cmd/admin` verde.
- [ ] Nessuna modifica al codice applicativo.

## 16. Rollback PR2

Se il worker separato presenta problemi operativi:

1. conservare i target Docker introdotti;
2. ripristinare temporaneamente in Compose il server `--mode all`;
3. disabilitare il servizio worker separato;
4. non ricreare un'immagine monolitica duplicata;
5. correggere lifecycle, configurazione o accesso DB in una PR dedicata.

---

# PR3 — Estrazione di vectorstore e storage da `internal/media`

## 17. Obiettivo

Rimuovere dal namespace legacy `internal/media` due capability che sono adapter infrastrutturali:

```text
internal/media/vectorstore
internal/media/storage
```

Destinazioni canoniche:

```text
internal/infrastructure/qdrant
internal/infrastructure/files/storage
```

La PR deve essere una migrazione reale di ownership, non un semplice spostamento con alias di compatibilità.

## 18. Branch e commit

Branch:

```text
codex/refactor-media-infrastructure
```

Commit suggeriti:

```text
refactor(qdrant): move vector store adapters out of internal media
refactor(storage): move file storage adapters into infrastructure
chore(architecture): remove migrated media paths from ratchet
```

I due spostamenti possono essere due commit separati nella stessa PR, purché ciascun commit compili.

## 19. Scope consentito

Percorsi principali:

```text
internal/media/vectorstore/**
internal/media/storage/**
internal/infrastructure/qdrant/**
internal/infrastructure/files/storage/**
internal/app/**
internal/application/**
internal/api/**
internal/infrastructure/**
scripts/archcheck/**
architecture/migration.yaml
ARCHITECTURE.md
```

I caller possono essere modificati soltanto per:

- aggiornare import;
- aggiornare nomi di package coerenti;
- usare constructor già esistenti;
- eliminare alias e wrapper diventati inutili;
- correggere test collegati allo spostamento.

Fuori scope:

- modifica degli algoritmi di ranking;
- modifica degli schemi Qdrant;
- modifica dei nomi collection;
- modifica delle dimensioni vector;
- modifica della logica di upload Drive;
- modifica delle route;
- refactor di altri package `internal/media`;
- nuove feature di ricerca semantica;
- migrazione contemporanea di catalogsync, clipindexer o semantic.

## 20. Preparazione obbligatoria

### 20.1 Inventario dei file

- [ ] Elencare tutti i file nei due package.
- [ ] Elencare tutti gli importatori attivi.
- [ ] Identificare dipendenze inverse verso application o API.
- [ ] Identificare tipi che appartengono al domain e non all'infrastructure.
- [ ] Identificare test e fixture.

Comandi:

```bash
find internal/media/vectorstore -type f -maxdepth 3 | sort
find internal/media/storage -type f -maxdepth 3 | sort
rg "internal/media/vectorstore|internal/media/storage" --type go
```

### 20.2 Classificare i simboli

Per ogni simbolo pubblico indicare:

| Simbolo | Tipo | Destinazione |
|---|---|---|
| client HTTP Qdrant | adapter | `internal/infrastructure/qdrant` |
| configurazione Qdrant | adapter config o config mapping | infrastructure/config o qdrant |
| service di orchestrazione ricerca | use case | application, non infrastructure |
| tipi asset generici | domain | `internal/domain/asset` |
| filesystem locale | adapter | `internal/infrastructure/files/storage` |
| resolver astratto | porta vicino al consumer | application/domain |

Non spostare automaticamente tutto nello stesso package se contiene responsabilità diverse.

## 21. TODO vectorstore → qdrant

### 21.1 Creare la destinazione canonica

Struttura suggerita:

```text
internal/infrastructure/qdrant/
├── client.go
├── config.go
├── collection.go
├── parse.go
├── retry.go
├── service.go
├── adapters.go
└── *_test.go
```

La struttura esatta deve seguire le capability reali. Non creare file vuoti per raggiungere questa forma.

### 21.2 Spostare con storia Git

- [ ] Usare `git mv` quando il file mantiene la stessa responsabilità.
- [ ] Dividere il file quando contiene sia application orchestration sia adapter Qdrant.
- [ ] Aggiornare `package vectorstore` a un nome coerente, preferibilmente `qdrant`.
- [ ] Non creare `type X = old.X` per compatibilità.
- [ ] Non lasciare un vecchio package che re-esporta il nuovo.

### 21.3 Correggere i confini

Il package infrastrutturale può importare:

- domain;
- config infrastrutturale;
- utility leaf in `pkg`;
- SDK/HTTP standard library.

Non deve importare:

- `internal/api`;
- handler;
- moduli di composition;
- use case concreti non necessari;
- Gin.

### 21.4 Aggiornare i caller

- [ ] `internal/app/composition.go`.
- [ ] servizi application che ricevono il vector service.
- [ ] realtime.
- [ ] association.
- [ ] clip indexer adapter.
- [ ] autotag.
- [ ] test e mock.

Regola: aggiornare gli import direttamente alla destinazione canonica. Nessuna catena transitional.

### 21.5 Test vectorstore

```bash
go test ./internal/infrastructure/qdrant/...
go test ./internal/application/realtime/...
go test ./internal/application/association/...
go test ./internal/media/clipindexer/...
```

Se alcuni test richiedono Qdrant reale, separarli con build tag `integration` invece di aggiungere nuovi `t.Skip`.

## 22. TODO storage → files/storage

### 22.1 Separare filesystem da dominio

Il nuovo package deve contenere soltanto:

- lettura e scrittura file;
- gestione path;
- operazioni atomiche;
- move/copy/delete;
- metadata filesystem;
- eventuali adapter locali.

Non deve contenere:

- decisioni di business;
- scelta del provider;
- logica job;
- handler HTTP;
- logica Drive;
- regole di classificazione asset.

### 22.2 Struttura suggerita

```text
internal/infrastructure/files/storage/
├── store.go
├── paths.go
├── atomic.go
├── cleanup.go
└── *_test.go
```

### 22.3 Correggere i consumer

- [ ] Individuare ogni import attivo del vecchio package.
- [ ] Aggiornare i consumer al nuovo path.
- [ ] Spostare eventuali interfacce vicino al consumer.
- [ ] Evitare una grande interfaccia storage globale.
- [ ] Preferire interfacce piccole: Reader, Writer, Remover o Store solo quando realmente necessario.
- [ ] Conservare semantica e gestione errori.

### 22.4 Test filesystem

I test devono usare `t.TempDir()`.

Casi minimi:

- [ ] creazione directory;
- [ ] scrittura file;
- [ ] sovrascrittura controllata;
- [ ] rename atomico quando previsto;
- [ ] cancellazione idempotente se il contratto la prevede;
- [ ] path traversal rifiutato;
- [ ] cleanup di file temporanei;
- [ ] permessi e errori propagati.

Comandi:

```bash
go test ./internal/infrastructure/files/storage/...
```

## 23. Eliminazione dei vecchi path

Prima della cancellazione:

```bash
rg "internal/media/vectorstore|internal/media/storage" --type go
```

Risultato atteso: zero import attivi.

Poi:

- [ ] eliminare i vecchi package;
- [ ] eliminare directory vuote;
- [ ] aggiornare `architecture/migration.yaml` con conteggi reali;
- [ ] aggiornare `ARCHITECTURE.md`;
- [ ] aggiornare archcheck riducendo la baseline, non espandendola;
- [ ] non lasciare TODO “remove later” per file già migrati.

## 24. Verifica architetturale PR3

```bash
go test ./internal/infrastructure/qdrant/...
go test ./internal/infrastructure/files/storage/...
go test ./internal/app/...
go build ./internal/...
go vet ./internal/infrastructure/...
go run ./scripts/archcheck
bash scripts/ci-architectural-checks.sh
```

Controlli aggiuntivi:

```bash
rg "internal/media/(vectorstore|storage)" --type go
find internal/media -type d -empty
```

## 25. Exit gate PR3

- [ ] Vecchi import a zero.
- [ ] Vecchie directory eliminate.
- [ ] Nessun alias di compatibilità.
- [ ] Nessun wrapper pass-through.
- [ ] Nessuna modifica agli algoritmi Qdrant.
- [ ] Nessuna modifica allo schema collection.
- [ ] Test package nuovi verdi.
- [ ] Build internal verde.
- [ ] Archcheck non aumenta violazioni.
- [ ] Conteggi della migration map aggiornati al codice reale.
- [ ] La PR non tocca altri package `internal/media` salvo import necessari.

## 26. Rollback PR3

In caso di regressione:

1. revert dell'intera PR;
2. non reintrodurre alias compatibili su `main`;
3. individuare se il problema è nello spostamento o in una dipendenza circolare;
4. dividere ulteriormente application orchestration e adapter;
5. riproporre una PR più stretta.

---

# PR4 — Split del mega-package YouTube

## 27. Obiettivo

Ridurre `internal/application/youtube` da mega-package a un insieme di capability coese, mantenendo una facade minima quando necessaria.

Il refactor deve eliminare la situazione in cui ricerca, metadata, estrazione, segmentazione, cache, intelligence e job integration condividono lo stesso package e lo stesso grande costruttore.

Target concettuale:

```text
internal/application/youtube/
├── service.go
├── ports.go
├── dto/
├── search/
├── metadata/
├── extraction/
├── segments/
├── enrichment/
├── cache/
└── jobs/
```

La struttura finale può differire, ma ogni directory di capability dovrebbe restare idealmente tra 5 e 8 file production.

## 28. Branch e strategia commit

Branch:

```text
codex/refactor-youtube-capabilities
```

Commit suggeriti:

```text
refactor(youtube): extract metadata capability
refactor(youtube): extract search capability
refactor(youtube): extract segment and extraction capabilities
refactor(youtube): extract cache enrichment and job integration
refactor(youtube): reduce facade dependencies and update wiring
```

Ogni commit deve compilare. Non fare un singolo commit che sposta 43 file e corregge tutto alla fine.

## 29. Scope consentito

Percorsi principali:

```text
internal/application/youtube/**
internal/infrastructure/youtube/**
internal/app/youtube_adapters.go
internal/app/composition.go
internal/api/sources/youtube/**
internal/api/sources/module_youtubeclip.go
internal/application/assets/providers/youtube/**
internal/application/monitor/**
internal/media/stockpipeline/**
internal/media/videomuscles/**
architecture/migration.yaml
ARCHITECTURE.md
docs/migrations/internal-application-youtube.md
scripts/archcheck/**
```

Modifiche nei consumer devono essere limitate ad import, costruttori, porte e wiring.

Fuori scope:

- cambio comportamento yt-dlp;
- nuove feature YouTube;
- modifica scoring;
- modifica delle route;
- modifica payload;
- nuova cache;
- nuovo database;
- nuovo provider registry;
- refactor di Artlist;
- modifica Qdrant non richiesta;
- rimozione della facade se esistono use case multi-capability reali.

## 30. Preparazione: mappa del package

Prima di spostare file creare una tabella con:

| File | Capability | Dipendenze concrete | Destinazione |
|---|---|---|---|
| `searcher*.go` | search | runner, cache, metadata | `search/` |
| `metadata*.go` | metadata | metadata port | `metadata/` |
| `extractor*.go` | extraction | process runner, pipeline | `extraction/` |
| `segment*.go` | segments | subtitles, clip files, indexer | `segments/` |
| `intelligence*.go` | enrichment | LLM, duplicate detection | `enrichment/` |
| cache files | cache | cache store | `cache/` |
| job adapter/codec | jobs | jobs service | `jobs/` |

TODO:

- [ ] Elencare tutti i 43 file production.
- [ ] Escludere i test dal conteggio production.
- [ ] Individuare i simboli condivisi.
- [ ] Individuare cicli potenziali.
- [ ] Individuare funzioni duplicate.
- [ ] Individuare porte opzionali mai usate.
- [ ] Individuare DTO che rappresentano righe SQLite e spostarli nell'infrastructure database.

Comandi:

```bash
find internal/application/youtube -maxdepth 1 -name '*.go' | sort
rg '^type |^func ' internal/application/youtube
rg 'internal/application/youtube' --type go
```

## 31. Regole di dipendenza interne

Direzione consentita:

```text
dto/domain
    ↑
capability application
    ↑
facade YouTube
    ↑
API / provider adapter / composition
```

Le capability sorelle non devono importarsi a catena.

Quando due capability condividono un tipo:

1. se è dominio stabile, spostarlo in `internal/domain/asset` o `youtube/dto`;
2. se è una richiesta interna di un use case, tenerlo vicino al consumer;
3. se è una riga SQLite, spostarlo in infrastructure database;
4. non creare un package `common` generico come discarica.

## 32. Fase 1 — Metadata

### 32.1 Destinazione

```text
internal/application/youtube/metadata/
```

Responsabilità:

- coordinare lettura metadata;
- validare DTO application;
- non eseguire direttamente processi;
- usare `VideoMetadataFetcherPort`.

Infrastructure:

```text
internal/infrastructure/youtube/metadata.go
```

resta proprietaria di yt-dlp JSON e traduzione verso DTO canonico.

TODO:

- [ ] Spostare use case metadata.
- [ ] Conservare adapter concreto in infrastructure.
- [ ] Eliminare duplicati `VideoMetadata`, `YouTubeMetadataPort` quando la compatibilità non è più necessaria.
- [ ] Usare un solo `DownloaderMetadata` canonico.
- [ ] Verificare preservazione thumbnails.
- [ ] Aggiungere test di mapping completo.

Test:

```bash
go test ./internal/application/youtube/metadata/...
go test ./internal/infrastructure/youtube/...
```

## 33. Fase 2 — Search

### 33.1 Destinazione

```text
internal/application/youtube/search/
```

Responsabilità:

- orchestrazione ricerca;
- richiesta e risposta applicativa;
- uso del SearchRunnerPort;
- uso della cache attraverso porta;
- composizione dei metadata quando necessario.

Non deve:

- shellare direttamente yt-dlp;
- leggere SQLite direttamente;
- usare Gin;
- dipendere dal package API.

TODO:

- [ ] Spostare `searcher.go` e file correlati.
- [ ] Sostituire `searchRunnerStub` con implementazione reale o errore esplicito.
- [ ] Non restituire empty success quando la capability non è implementata.
- [ ] Propagare `ctx.Err()`.
- [ ] Separare search cache da search orchestration.
- [ ] Aggiornare provider adapter YouTube.
- [ ] Aggiornare handler senza cambiare route.

Test minimi:

- [ ] context cancellato;
- [ ] risultato vuoto reale;
- [ ] runner non configurato;
- [ ] cache hit;
- [ ] cache miss;
- [ ] metadata parsing error;
- [ ] limite risultati.

## 34. Fase 3 — Extraction

Destinazione:

```text
internal/application/youtube/extraction/
```

Responsabilità:

- orchestrare fetch/download;
- richiedere segment extraction;
- costruire risultati applicativi;
- invocare lifecycle e destination resolver attraverso porte.

Infrastructure continua a possedere:

- `os/exec`;
- yt-dlp;
- FFmpeg;
- filesystem;
- parsing VTT concreto;
- video pipeline concreta.

TODO:

- [ ] Spostare `extractor_*.go`.
- [ ] Rimuovere process execution dall'application se ancora presente.
- [ ] Usare adapter in `internal/infrastructure/youtube`.
- [ ] Verificare timeout e cancellazione.
- [ ] Verificare cleanup file temporanei.
- [ ] Verificare error wrapping.
- [ ] Non modificare formato dei file prodotti.

## 35. Fase 4 — Segments

Destinazione:

```text
internal/application/youtube/segments/
```

Responsabilità:

- scegliere finestre temporali;
- coordinare subtitles, clip files e indexer;
- validare start/end;
- restituire risultato applicativo.

TODO:

- [ ] Spostare `segment*.go`.
- [ ] Definire porte piccole vicino al consumer.
- [ ] Eliminare porte marker vuote se non danno valore reale.
- [ ] Non usare typed-nil interfaces.
- [ ] Applicare `portutil.IsNilPort` nei confini in cui una dipendenza opzionale può arrivare tipizzata nil.
- [ ] Separare segment cache dalla logica segment.

Test minimi:

- [ ] start maggiore di end;
- [ ] finestra fuori durata;
- [ ] subtitle assenti;
- [ ] file cache presente;
- [ ] file cache corrotto;
- [ ] indexer disabilitato;
- [ ] cancellazione context.

## 36. Fase 5 — Enrichment

Destinazione:

```text
internal/application/youtube/enrichment/
```

Responsabilità:

- intelligence;
- dedup semantico;
- arricchimento metadata;
- eventuale folder memory come porta.

TODO:

- [ ] Spostare `intelligence_*.go` ed `enrichment.go`.
- [ ] Riutilizzare i servizi comuni esistenti.
- [ ] Non duplicare scoring già presente in asset resolver o clip resolver.
- [ ] Non creare un nuovo LLM client.
- [ ] Usare il client Ollama già iniettato.
- [ ] Separare regole deterministiche da chiamate LLM.
- [ ] Testare fallback senza LLM.

## 37. Fase 6 — Cache

Destinazione:

```text
internal/application/youtube/cache/
```

La capability application deve definire il contratto necessario, ma la row SQLite deve vivere in:

```text
internal/infrastructure/database/sqlite/assets/
```

TODO:

- [ ] Spostare `YouTubeCacheEntry` fuori da `ports.go` se rappresenta una riga DB.
- [ ] Mantenere nella porta soltanto DTO necessari all'application.
- [ ] Eliminare conversioni duplicate.
- [ ] Testare TTL, miss, dati corrotti e cancellazione.
- [ ] Non creare un secondo database cache.

## 38. Fase 7 — Job integration

Destinazione:

```text
internal/application/youtube/jobs/
```

Responsabilità:

- codec payload;
- registrazione handler;
- traduzione job → use case;
- progress reporting.

Non deve contenere:

- SQL diretto;
- Gin;
- process execution;
- business logic duplicata dall'extraction service.

TODO:

- [ ] Spostare adapter e codec job.
- [ ] Conservare job type e payload pubblici.
- [ ] Usare il job registry comune.
- [ ] Non aggiungere un dispatcher YouTube separato.
- [ ] Testare encode/decode e handler cancellation.

## 39. Riduzione della facade principale

La facade `youtube.Service` deve diventare un coordinatore piccolo.

Target indicativo:

```go
type Service struct {
    Search     *search.Service
    Extraction *extraction.Service
    Segments   *segments.Service
    Metadata   *metadata.Service
}
```

Questo esempio non è obbligatorio. La regola obbligatoria è:

- nessun costruttore con 21 dipendenze piatte;
- nessun setter cascade;
- nessuna dipendenza opzionale non usata;
- ogni capability costruita con le sue dipendenze;
- la facade riceve capability già costruite;
- il composition root resta l'unico luogo che costruisce adapter concreti.

TODO:

- [ ] Creare piccoli `Deps` per capability.
- [ ] Ridurre `youtube.ServiceDeps`.
- [ ] Eliminare campi inutilizzati.
- [ ] Evitare che la facade esponga repository concreti.
- [ ] Aggiornare `BuildDomainBundle` o builder YouTube dedicato.
- [ ] Spostare la costruzione YouTube in `internal/app/modules/youtube.go` se riduce realmente `composition.go`.
- [ ] Non creare un nuovo mega-bundle.

## 40. Aggiornamento composition root

Il builder YouTube deve ricevere soltanto le capability richieste.

Struttura suggerita:

```text
internal/app/modules/youtube.go
```

Responsabilità:

- costruire adapter concreti;
- costruire services capability;
- costruire facade;
- restituire il risultato al DomainBundle.

Limite:

- massimo 8–10 dipendenze per funzione o bundle;
- se il limite viene superato, creare un bundle coeso esistente, non una mega-struct generica.

## 41. Aggiornamento API e provider

- [ ] Le route restano identiche.
- [ ] Gli handler chiamano use case espliciti.
- [ ] Il provider registry continua a essere il solo punto di dispatch provider.
- [ ] Nessuno switch provider viene aggiunto fuori dal registry.
- [ ] Nessun handler importa infrastructure.
- [ ] Nessun handler contiene goroutine orchestration.

## 42. Strategia test PR4

Dopo ogni fase:

```bash
go test ./internal/application/youtube/...
go test ./internal/infrastructure/youtube/...
go test ./internal/app/...
```

A fine PR:

```bash
go test ./...
go vet ./internal/application/youtube/...
go vet ./internal/infrastructure/youtube/...
go build ./cmd/server
go build ./cmd/worker
go run ./scripts/archcheck
bash scripts/ci-architectural-checks.sh
```

Controlli strutturali:

```bash
find internal/application/youtube -maxdepth 1 -name '*.go' | wc -l
find internal/application/youtube -mindepth 1 -maxdepth 1 -type d | sort
rg "os/exec|exec.Command|database/sql|gin-gonic" internal/application/youtube
rg "internal/infrastructure" internal/api/sources/youtube
```

Risultati attesi:

- package root YouTube piccolo;
- nessuna execution concreta nell'application;
- nessun SQL diretto nell'application;
- nessun import infrastructure nell'API;
- nessuna nuova eccezione archcheck.

## 43. Exit gate PR4

- [ ] Root package YouTube ridotto a facade, porte e DTO strettamente condivisi.
- [ ] Capability separate e coese.
- [ ] Nessuna capability supera senza motivazione il limite 5–8 file production.
- [ ] Costruttore principale non riceve più 21 dipendenze piatte.
- [ ] Nessun setter cascade.
- [ ] Nessun alias legacy.
- [ ] Nessun wrapper pass-through.
- [ ] Nessuna route o payload modificato.
- [ ] Provider registry invariato come punto di dispatch.
- [ ] Test YouTube verdi.
- [ ] Build server e worker verde.
- [ ] Archcheck verde.
- [ ] Documentazione aggiornata al codice reale.

## 44. Rollback PR4

Poiché la PR è strutturale, il rollback corretto è il revert completo.

Non fare rollback parziali che lasciano:

- package duplicati;
- alias vecchio → nuovo;
- metà caller sul vecchio path;
- due facade;
- due set di porte;
- due cache adapter.

Se la PR diventa troppo grande:

1. chiudere il branch senza merge;
2. ripartire da `origin/main`;
3. estrarre una sola capability per PR;
4. mantenere ogni commit compilabile;
5. non usare shim temporanei per forzare un merge incompleto.

---

# 45. Verifica finale dopo le quattro PR

## 45.1 Build e test

```bash
go test ./...
go vet ./...
go build ./...
bash scripts/ci-architectural-checks.sh
go run ./scripts/archcheck
```

## 45.2 Controlli namespace

```bash
rg "internal/media/(vectorstore|storage)" --type go
rg "puppeteer\"" node-scraper/package.json
find internal/application/youtube -maxdepth 1 -name '*.go' | sort
```

## 45.3 Controlli Docker

```bash
docker build --target server-runtime -t pipelinegen-server:refactor-final .
docker build --target worker-runtime -t pipelinegen-worker:refactor-final .
docker build --target admin-runtime -t pipelinegen-admin:refactor-final .
docker compose config
docker compose build
```

## 45.4 Controlli repository

```bash
git status -sb
git diff origin/main...HEAD
git log -n 10 --oneline
```

Verificare:

- [ ] nessun file generato;
- [ ] nessun database;
- [ ] nessun `node_modules`;
- [ ] nessun output Docker;
- [ ] nessun backup;
- [ ] nessuna modifica non collegata;
- [ ] documentazione coerente con il codice;
- [ ] migration map con conteggi aggiornati;
- [ ] baseline archcheck ridotta o invariata, mai ampliata per coprire regressioni.

---

# 46. Definition of Done complessiva

Il progetto può considerare concluso questo ciclo quando:

- [ ] il Node scraper installa soltanto `puppeteer-core`;
- [ ] la build Node usa `npm ci` e caching corretto;
- [ ] esistono runtime Docker separati per server, worker e admin;
- [ ] il server non contiene tool media non necessari;
- [ ] `internal/media/vectorstore` non esiste più;
- [ ] `internal/media/storage` non esiste più;
- [ ] i nuovi package infrastructure hanno ownership corretta;
- [ ] `internal/application/youtube` non è più un mega-package;
- [ ] la facade YouTube è piccola;
- [ ] il composition root costruisce capability, non un service locator;
- [ ] non sono stati aggiunti nuovi alias, wrapper o fallback legacy;
- [ ] tutte le route e i payload pubblici sono invariati;
- [ ] server e worker compilano;
- [ ] test, vet, build e archcheck sono verdi;
- [ ] ogni PR è stata piccola, revisionabile e mergiata separatamente.

---

# 47. Regola per il lavoro successivo

Dopo questo ciclo non aggiungere nuove feature direttamente nei vecchi namespace o nel root package YouTube.

Ogni nuova funzionalità deve:

1. entrare nel registry, resolver o sampler comune appropriato;
2. avere un solo proprietario;
3. usare una porta piccola vicino al consumer;
4. costruire adapter concreti soltanto nel composition root;
5. aggiungere test nel package della capability;
6. non ricreare dipendenze piatte o mega-package;
7. non riportare tool media nel runtime server se appartengono al worker.

Questo documento è il contratto operativo del ciclo di alleggerimento. Se il codice reale cambia durante l'esecuzione delle PR, aggiornare il documento nella stessa PR che modifica il relativo piano, senza dichiarare completato un blocco ancora presente nel repository.
