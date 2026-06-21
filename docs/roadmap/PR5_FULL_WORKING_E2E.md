# PR5 — Full Working End-to-End Certification

## Obiettivo

Certificare PipelineGen come sistema realmente operativo dopo PR0–PR4. Questa fase non è un altro refactor: esegue workflow completi con database, processi esterni e servizi reali, introduce test automatici ripetibili e corregge soltanto i difetti che impediscono l'esecuzione end-to-end.

PR5 è conclusa soltanto quando un ambiente pulito può:

- avviare server e worker;
- applicare tutte le migration;
- elaborare job con lease, retry e recovery;
- completare workflow YouTube e Artlist;
- generare script e Google Doc;
- eseguire le capability immagini e voiceover configurate;
- ripartire dopo crash senza perdere o duplicare dati;
- produrre log, metriche e report sufficienti a diagnosticare gli errori;
- eseguire backup e restore verificati;
- sostenere il carico operativo CPU-first definito.

## Dipendenze

PR5 è bloccata da PR0, PR1, PR2, PR3 e PR4. Non iniziare la certificazione finale mentre package, route o composition root sono ancora in movimento.

## Principi della certificazione

1. Un test è valido solo se controlla anche gli effetti persistenti, non soltanto lo status HTTP.
2. I test reali devono usare account, cartelle e dati sandbox dedicati.
3. Nessun test deve cancellare o sovrascrivere asset di produzione.
4. Ogni workflow deve produrre un correlation ID e un report consultabile.
5. Ogni errore scoperto deve avere un test di regressione prima della correzione.
6. I test esterni devono essere separati dai test unitari e attivati esplicitamente.
7. Nessun risultato manuale viene considerato certificato senza evidenza salvata nel report.
8. Il sistema non è “full working” se funziona soltanto su un database già popolato o su un processo mai riavviato.

## Output atteso

La futura implementazione PR5 deve introdurre una struttura equivalente a:

```text
tests/e2e/
  README.md
  fixtures/
  helpers/
  local/
  external/
  recovery/
  load/

scripts/e2e/
  preflight.sh
  run-local.sh
  run-external.sh
  collect-report.sh
```

I report runtime devono essere scritti sotto `tmp/e2e/<run-id>/`, così `make clean` può rimuoverli e nessun risultato contenente dati sensibili viene committato.

---

## Checklist operativa

### PR5.0 — Definire la release candidate

- [ ] Registrare commit SHA, versione Go, sistema operativo e architettura della macchina di test.
- [ ] Registrare versioni di Python, Node, FFmpeg, `ffprobe`, `yt-dlp` e SQLite.
- [ ] Registrare modalità server usata: `--mode all` oppure server più worker separato.
- [ ] Registrare quali provider esterni sono abilitati.
- [ ] Creare un run ID univoco per tutti i log e gli asset del test.
- [ ] Salvare configurazione redatta senza token o credenziali.
- [ ] Rigenerare `docs/api/ACTIVE_API_GENERATED.md` prima di iniziare.

Comandi minimi:

```bash
git rev-parse HEAD
go version
python3 --version
node --version
ffmpeg -version | head -n 1
ffprobe -version | head -n 1
yt-dlp --version
sqlite3 --version
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
git diff --exit-code docs/api/ACTIVE_API_GENERATED.md
```

**Accettazione PR5.0**

Il report identifica esattamente codice, strumenti e configurazione usati. Una seconda macchina può riprodurre la stessa preparazione.

### PR5.1 — Preflight dell'ambiente

- [ ] Verificare presenza dei tre binari prodotti da `make build`.
- [ ] Verificare accesso in scrittura alle directory storage configurate.
- [ ] Verificare `VELOX_ADMIN_TOKEN` e `VELOX_WORKER_TOKEN` non vuoti e non placeholder.
- [ ] Verificare `VELOX_PORT` oppure documentare l'uso del default `8080`.
- [ ] Verificare credenziali Google Drive sandbox e cartella target dedicata.
- [ ] Verificare disponibilità del modello Ollama richiesto dai workflow script.
- [ ] Verificare disponibilità scraper Artlist quando i test Artlist sono abilitati.
- [ ] Verificare connettività verso YouTube, Artlist, Drive e provider AI configurati.
- [ ] Verificare spazio disco disponibile prima dei test media.
- [ ] Verificare che database e output di test non puntino ai dati di produzione.

Comandi base:

```bash
make clean
make build
make doctor
```

`make doctor` va eseguito dopo l'avvio del server.

**Accettazione PR5.1**

Il preflight fallisce con un messaggio preciso per ogni dipendenza assente. Nessun workflow parte in uno stato parzialmente configurato.

### PR5.2 — Database vuoto e catena completa delle migration

- [ ] Creare una directory temporanea di test completamente vuota.
- [ ] Avviare PipelineGen puntando a database SQLite inesistenti.
- [ ] Verificare applicazione ordinata di tutte le migration dalla prima all'ultima.
- [ ] Verificare assenza di errori `no transaction`, `duplicate column`, sintassi SQL o migration parziali.
- [ ] Verificare presenza delle tabelle core: asset, jobs, outbox, scripts e tabelle satellite.
- [ ] Verificare indici e vincoli principali.
- [ ] Riavviare immediatamente il server sul database appena migrato.
- [ ] Verificare che il secondo avvio non riapplichi migration già completate.
- [ ] Eseguire `PRAGMA integrity_check` su ogni database.
- [ ] Salvare schema e lista migration applicate nel report.

Controlli minimi:

```bash
sqlite3 <database> 'PRAGMA integrity_check;'
sqlite3 <database> '.tables'
```

**Accettazione PR5.2**

Un ambiente senza database raggiunge `/api/health/deep` dopo un solo avvio e resta avviabile dopo restart.

### PR5.3 — Boot, health, doctor e shutdown

- [ ] Avviare `./bin/pipelinegen --mode all`.
- [ ] Verificare `GET /health`.
- [ ] Verificare `GET /api/health`.
- [ ] Verificare `GET /api/health/deep`.
- [ ] Verificare `GET /api/system/doctor` tramite `make doctor`.
- [ ] Verificare `GET /metrics`.
- [ ] Controllare che health e doctor distinguano componenti healthy, degraded e unavailable.
- [ ] Inviare SIGTERM al processo.
- [ ] Verificare shutdown ordinato di HTTP server, runner, scheduler, monitor e outbox.
- [ ] Verificare che nessun database resti corrotto o locked dopo shutdown.
- [ ] Ripetere boot/shutdown almeno tre volte sullo stesso database.

**Accettazione PR5.3**

Tre cicli consecutivi di avvio e arresto terminano senza goroutine bloccate, lock SQLite persistenti o job persi.

### PR5.4 — Contratti API e autenticazione

- [ ] Confrontare tutte le route live con `docs/api/ACTIVE_API_GENERATED.md`.
- [ ] Verificare che route pubbliche, admin e worker applichino l'autenticazione corretta.
- [ ] Verificare token mancante.
- [ ] Verificare token errato.
- [ ] Verificare token corretto.
- [ ] Verificare body JSON malformato.
- [ ] Verificare body vuoto per endpoint che richiedono input.
- [ ] Verificare limiti e validazione dei parametri query/path.
- [ ] Verificare che errori interni non espongano credenziali, path sensibili o stack trace.
- [ ] Verificare `Content-Type`, status code e forma JSON degli errori.
- [ ] Salvare una matrice endpoint/status nel report.

**Accettazione PR5.4**

Nessuna route protetta è accessibile senza credenziali corrette e nessun errore espone dati sensibili.

### PR5.5 — Job system end-to-end

- [ ] Enqueue di un job reale tramite `POST /api/jobs` o endpoint capability-specifico.
- [ ] Verificare persistenza stato `PENDING`.
- [ ] Verificare claim e transizione a `LEASED`/`RUNNING`.
- [ ] Verificare progress events e revision incrementale.
- [ ] Verificare completamento `SUCCEEDED` con risultato persistito.
- [ ] Verificare `GET /api/jobs/:id`.
- [ ] Verificare `GET /api/jobs/:id/full`.
- [ ] Verificare `GET /api/jobs/:id/events`.
- [ ] Verificare cancellazione di job pending e running.
- [ ] Verificare retry di job fallito.
- [ ] Verificare `max_retries` e transizione terminale.
- [ ] Verificare active key/idempotenza contro enqueue duplicati.
- [ ] Verificare fencing: una lease vecchia non può completare il job.
- [ ] Verificare reaper di lease scadute.
- [ ] Verificare worker separato usando `VELOX_BROKER_URL` quando supportato dall'ambiente.

**Accettazione PR5.5**

Ogni transizione è coerente tra API, tabella jobs ed event log. Nessun job può essere completato due volte dalla stessa o da una vecchia lease.

### PR5.6 — Workflow YouTube completo

- [ ] Usare un video sandbox pubblico, stabile e non sensibile.
- [ ] Eseguire ricerca tramite `/api/clips/search`.
- [ ] Verificare risultati, metadata e ranking minimi.
- [ ] Ottenere metadata tramite `/api/clips/info`.
- [ ] Avviare download/process tramite `/api/clips/process` oppure il workflow canonico equivalente.
- [ ] Eseguire `POST /api/media/register-from-youtube` per un segmento breve.
- [ ] Verificare download locale.
- [ ] Verificare estrazione segmento con durata attesa entro tolleranza.
- [ ] Verificare output video con `ffprobe`.
- [ ] Verificare hash del file.
- [ ] Verificare upload nella cartella Drive sandbox.
- [ ] Verificare record asset, location, processing e version.
- [ ] Verificare eventuale indicizzazione/outbox.
- [ ] Ripetere la stessa richiesta e verificare idempotenza o comportamento duplicato documentato.
- [ ] Eliminare o archiviare l'asset sandbox tramite API canonica.

Controlli media minimi:

```bash
ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 <output>
sha256sum <output>
```

**Accettazione PR5.6**

Una richiesta YouTube produce un asset riproducibile, persistito e rintracciabile fino a Drive; il secondo invio non crea duplicati incontrollati.

### PR5.7 — Workflow Artlist completo

- [ ] Verificare `/api/artlist/diagnostics`.
- [ ] Eseguire `POST /api/artlist/search/live` con un termine sandbox.
- [ ] Eseguire `POST /api/artlist/run` oppure `make artlist TERM=<term> LIMIT=1 PRESET=youtube_1080p_7s`.
- [ ] Salvare `run_id` e monitorare `/api/artlist/runs/:run_id`.
- [ ] Verificare discovery candidato.
- [ ] Verificare download HTTP/HLS.
- [ ] Verificare processamento FFmpeg a formato previsto.
- [ ] Verificare durata, risoluzione e codec con `ffprobe`.
- [ ] Verificare hash e path locale.
- [ ] Verificare upload Drive sandbox.
- [ ] Verificare aggiornamento `drive_link`, `file_hash` e `local_path`.
- [ ] Verificare strategie `verify`, `skip` e `replace` con fixture controllata.
- [ ] Verificare restart dello scraper tra due run.
- [ ] Verificare comportamento quando scraper o downloader sono unavailable.

**Accettazione PR5.7**

Un run Artlist passa da richiesta a asset pronto e persistito; strategie e recovery producono esiti deterministici.

### PR5.8 — Script generation e Google Doc

- [ ] Preparare almeno due clip reali già indicizzate.
- [ ] Eseguire `POST /api/script/generate-from-clips` con `clip_ids`, titolo, lingua e tono.
- [ ] Monitorare il job script fino allo stato terminale.
- [ ] Verificare che il piano narrativo usi esclusivamente clip esistenti.
- [ ] Verificare regola una clip uguale una scena quando prevista dal workflow.
- [ ] Verificare contenuto script non vuoto e lingua corretta.
- [ ] Verificare `clip_scenes` con ID reali.
- [ ] Verificare persistenza script, sezioni, outline e generation logs.
- [ ] Verificare creazione Google Doc nella cartella sandbox.
- [ ] Verificare `doc_link` accessibile all'account di test.
- [ ] Verificare memory gate: seconda richiesta identica usa il comportamento cache previsto.
- [ ] Verificare `force_refresh` quando supportato.
- [ ] Eseguire anche `generate-batch` e controllare progress endpoint.
- [ ] Eseguire un caso di errore LLM e verificare stato job e messaggio diagnostico.

**Accettazione PR5.8**

Da clip reali si ottengono script, scene e Google Doc coerenti, persistiti e ripetibili.

### PR5.9 — Immagini e voiceover

Eseguire soltanto le capability abilitate nella configurazione di test, marcando esplicitamente le altre come non certificate.

- [ ] Eseguire ricerca immagini tramite `/api/images/search`.
- [ ] Eseguire generazione tramite `/api/images/generate` con provider sandbox configurato.
- [ ] Verificare file, dimensioni, metadata, repository e Drive quando previsto.
- [ ] Eseguire upload e sync immagini.
- [ ] Eseguire `/api/fullimages/video/generate` se la capability è attiva.
- [ ] Eseguire `/api/media/voiceover/generate`.
- [ ] Verificare durata audio, codec e file non vuoto.
- [ ] Verificare batch voiceover.
- [ ] Verificare voiceover sync.
- [ ] Verificare associazione tra asset, script/scena e output generato.
- [ ] Verificare retry del provider e comportamento su timeout.

**Accettazione PR5.9**

Ogni capability dichiarata abilitata completa almeno un workflow reale. Una capability non testata non può essere dichiarata production-ready.

### PR5.10 — Fault injection e recovery

Per ogni test, salvare l'istante del fault, lo stato prima/dopo e il risultato del recovery.

- [ ] Terminare il server durante un download.
- [ ] Terminare il worker durante un job running.
- [ ] Terminare lo scraper Artlist durante una ricerca.
- [ ] Rendere temporaneamente Drive non raggiungibile.
- [ ] Rendere temporaneamente Ollama non raggiungibile.
- [ ] Simulare disco pieno o directory non scrivibile in ambiente isolato.
- [ ] Simulare file temporaneo mancante prima del processamento.
- [ ] Simulare lease scaduta.
- [ ] Riavviare server e worker.
- [ ] Verificare job requeued, failed o recovered secondo policy.
- [ ] Verificare assenza di asset `ACTIVE` senza file valido.
- [ ] Verificare cleanup di file parziali.
- [ ] Verificare assenza di upload duplicati dopo retry.

**Accettazione PR5.10**

Ogni fault termina in uno stato consistente e diagnosticabile; nessun job resta zombie oltre la soglia configurata.

### PR5.11 — Integrità, idempotenza e riconciliazione

- [ ] Verificare foreign key e riferimenti tra asset, locations, processing e versions.
- [ ] Verificare che ogni Drive file attivo abbia un record locale coerente.
- [ ] Verificare che ogni record locale attivo punti a un file esistente oppure sia marcato degraded/failed.
- [ ] Verificare duplicati per source ID, URL, hash e active key.
- [ ] Eseguire endpoint reconcile disponibili.
- [ ] Eseguire sync Drive sandbox.
- [ ] Verificare soft delete, trash e hard delete secondo policy.
- [ ] Verificare che retry e restart non incrementino reuse/version in modo scorretto.
- [ ] Eseguire `PRAGMA foreign_key_check` e `PRAGMA integrity_check`.

**Accettazione PR5.11**

Tutti i controlli SQLite restituiscono esito valido e la riconciliazione non produce correzioni inattese al secondo passaggio.

### PR5.12 — Backup e restore verificato

- [ ] Arrestare in modo ordinato i writer oppure usare il meccanismo SQLite backup canonico.
- [ ] Creare backup di tutti i database richiesti.
- [ ] Salvare checksum dei backup.
- [ ] Copiare backup in una directory isolata.
- [ ] Ripristinare su un ambiente vuoto.
- [ ] Avviare server e worker sul restore.
- [ ] Verificare health, job history, asset, script e configurazioni persistite.
- [ ] Aprire almeno un asset Drive e un Google Doc referenziati dal restore.
- [ ] Eseguire integrity check dopo restore.
- [ ] Documentare RPO, RTO e procedura operativa reale.

**Accettazione PR5.12**

Il restore avvia un sistema consistente e recupera i dati verificati senza modifiche manuali al database.

### PR5.13 — Carico e stabilità CPU-first

Definire prima del test i limiti della macchina: CPU, RAM, disco e numero worker.

- [ ] Misurare baseline idle di CPU, RAM e file descriptor.
- [ ] Enqueue di job leggeri concorrenti.
- [ ] Enqueue di job FFmpeg concorrenti con limite configurato.
- [ ] Eseguire ricerche API concorrenti.
- [ ] Misurare latenza p50, p95 e p99.
- [ ] Misurare throughput job/minuto.
- [ ] Misurare picco RAM durante FFmpeg.
- [ ] Verificare assenza di `database is locked` non gestiti.
- [ ] Verificare backpressure quando i worker sono saturi.
- [ ] Verificare che il server HTTP resti responsivo durante rendering CPU-heavy.
- [ ] Eseguire soak test di durata definita, minimo 60 minuti per la certificazione iniziale.
- [ ] Verificare assenza di crescita continua di goroutine, memoria e file temporanei.

**Accettazione PR5.13**

Il sistema rispetta limiti dichiarati e degrada tramite coda/backpressure, non tramite crash o corruzione.

### PR5.14 — Osservabilità e diagnosi

- [ ] Verificare correlation ID da richiesta a job, log, outbox e output.
- [ ] Verificare log strutturati per start, progress, retry, success e failure.
- [ ] Verificare metriche HTTP, job, worker e provider.
- [ ] Verificare che `/metrics` non esponga secret o payload sensibili.
- [ ] Verificare doctor/diagnostics per Drive, Ollama, Artlist, Qdrant e database quando configurati.
- [ ] Verificare log rotation o destinazione log operativa.
- [ ] Definire alert minimi:
  - job failed;
  - job zombie;
  - queue depth elevata;
  - Drive unavailable;
  - scraper unavailable;
  - database errors;
  - spazio disco basso.
- [ ] Verificare che un operatore possa diagnosticare ogni fault PR5.10 usando solo API, log e metriche.

**Accettazione PR5.14**

Per ogni errore iniettato esiste un segnale osservabile e una procedura di diagnosi non basata sull'accesso diretto al codice.

### PR5.15 — Deployment riproducibile

- [ ] Eseguire `make rebuild` da checkout pulito.
- [ ] Avviare i tre binari canonici prodotti in `bin/`.
- [ ] Verificare server `--mode all`.
- [ ] Verificare server e worker separati.
- [ ] Verificare directory persistenti e permessi.
- [ ] Verificare configurazione tramite environment e file previsto.
- [ ] Verificare startup failure su token placeholder.
- [ ] Verificare Docker build se `Dockerfile` è presente.
- [ ] Verificare container restart con volumi persistenti.
- [ ] Verificare health check del deployment.
- [ ] Documentare comando di start, stop, upgrade e rollback.
- [ ] Eseguire rollback a una build precedente usando backup compatibile.

**Accettazione PR5.15**

Una macchina pulita può installare, configurare, avviare, aggiornare e ripristinare PipelineGen seguendo una procedura documentata.

### PR5.16 — Automazione della suite E2E

- [ ] Creare target `make e2e-local` per test senza servizi a pagamento.
- [ ] Creare target `make e2e-external` protetto da env flag per Drive, YouTube, Artlist e provider AI.
- [ ] Creare target `make e2e-recovery`.
- [ ] Creare target `make e2e-report`.
- [ ] Separare chiaramente test deterministici da test dipendenti dalla rete.
- [ ] Assegnare timeout a ogni scenario.
- [ ] Garantire cleanup con `defer`, trap shell o teardown equivalente.
- [ ] Nascondere token nei log CI.
- [ ] Salvare report JUnit/JSON e log redatti come artifact CI.
- [ ] Eseguire E2E local su ogni PR che tocca workflow critici.
- [ ] Eseguire E2E external manualmente o su ambiente protetto prima del rilascio.
- [ ] Impedire il rilascio se uno scenario obbligatorio fallisce.

### PR5.17 — Matrice finale di certificazione

Compilare la tabella con evidenza reale, non con aspettative:

| Area | Scenario obbligatorio | Esito | Evidenza |
|---|---|---|---|
| Build | checkout pulito → tre binari | ⬜ | |
| Migration | database vuoto → ultima migration | ⬜ | |
| Health | health/deep/doctor/metrics | ⬜ | |
| Security | auth e secret redaction | ⬜ | |
| Jobs | enqueue/lease/progress/retry/cancel | ⬜ | |
| YouTube | search → segment → Drive → DB | ⬜ | |
| Artlist | search → download → process → Drive → DB | ⬜ | |
| Script | clip IDs → script → scene → Google Doc | ⬜ | |
| Images | search/generate/upload/sync | ⬜ | |
| Voiceover | generate/batch/sync | ⬜ | |
| Recovery | crash/restart/fault injection | ⬜ | |
| Integrity | reconcile + SQLite checks | ⬜ | |
| Backup | backup → restore → boot | ⬜ | |
| Load | concurrency + soak CPU-first | ⬜ | |
| Observability | log/metrics/alerts/diagnosis | ⬜ | |
| Deployment | install/start/upgrade/rollback | ⬜ | |

- [ ] Ogni riga obbligatoria ha esito PASS e link/percorso all'evidenza.
- [ ] Le capability disabilitate sono marcate `NOT CERTIFIED`, non `PASS`.
- [ ] Tutti i bug bloccanti scoperti hanno test di regressione.
- [ ] Nessun test obbligatorio è saltato.
- [ ] Il commit certificato è quello candidato al rilascio.

---

## Comandi finali obbligatori

```bash
make clean
make tidy-check
make vet
make lint
make test-unit
make coverage-check
make build
go run ./scripts/archcheck
bash scripts/ci-architectural-checks.sh
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
git diff --exit-code docs/api/ACTIVE_API_GENERATED.md
```

Dopo l'implementazione della suite PR5:

```bash
make e2e-local
make e2e-external
make e2e-recovery
make e2e-report
```

## Exit gate finale — “Full Working”

PipelineGen può essere dichiarato **operativo al 100% per le capability certificate** soltanto quando:

- PR0–PR4 sono concluse;
- tutti i controlli statici, unit test, race test e build sono verdi;
- database vuoto, migration e restart sono verificati;
- job system, YouTube, Artlist e script completano workflow reali;
- immagini e voiceover sono certificate oppure dichiarate esplicitamente non abilitate;
- crash, retry e recovery non producono dati inconsistenti;
- backup e restore sono stati eseguiti realmente;
- carico e soak test rispettano i limiti dichiarati;
- deployment e rollback sono riproducibili;
- ogni riga obbligatoria della matrice PR5.17 è PASS;
- il report E2E identifica commit, ambiente, prove ed evidenze.

“Compila”, “i test unitari passano” o “la route risponde 200” non sono criteri sufficienti per chiudere PR5.
