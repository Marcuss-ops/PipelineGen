# PR5 — Full Working End-to-End Certification

## Obiettivo

Certificare PipelineGen come realmente operativo dopo PR0–PR4 e il gate single-source-of-truth. PR5 non introduce un nuovo refactor: esegue workflow completi, registra evidenze e corregge soltanto difetti che impediscono il funzionamento reale.

Tutte le attività sotto sono ancora aperte.

## Checklist residua

### PR5.0 — Release candidate e preflight

- [ ] Registrare commit SHA, Go, Python, Node, FFmpeg, `ffprobe`, `yt-dlp`, SQLite, OS e architettura.
- [ ] Costruire i binari canonici da checkout pulito.
- [ ] Verificare config, token non placeholder, directory scrivibili e spazio disco.
- [ ] Verificare accesso a Drive sandbox, Ollama/modello richiesto, scraper Artlist e provider abilitati.
- [ ] Usare database, cartelle Drive e credenziali separati dalla produzione.
- [ ] Creare un run ID e una directory `tmp/e2e/<run-id>/` per report e log redatti.

### PR5.1 — Database, migration, boot e shutdown

- [ ] Avviare da directory dati completamente vuota.
- [ ] Applicare tutte le migration dalla prima all'ultima senza interventi manuali.
- [ ] Verificare secondo avvio idempotente.
- [ ] Eseguire `PRAGMA integrity_check` e `PRAGMA foreign_key_check`.
- [ ] Verificare `/health`, `/api/health`, `/api/health/deep`, `/api/system/doctor` e `/metrics`.
- [ ] Eseguire almeno tre cicli boot/SIGTERM/restart senza lock SQLite, goroutine bloccate o job persi.

### PR5.2 — Contratto API e sicurezza

- [ ] Confrontare router live e `docs/api/ACTIVE_API_GENERATED.md`.
- [ ] Verificare token mancante, errato e corretto per route public/admin/worker.
- [ ] Verificare JSON malformato, body vuoto, parametri invalidi e limiti.
- [ ] Verificare status code, `Content-Type` e forma JSON degli errori.
- [ ] Verificare che errori, metriche e log non espongano secret, payload sensibili o stack trace.
- [ ] Salvare una matrice endpoint/status/auth nel report.

### PR5.3 — Job system completo

- [ ] Eseguire enqueue → pending → leased/running → succeeded.
- [ ] Verificare progress, events, revision e risultato persistito.
- [ ] Verificare cancellazione pending e running.
- [ ] Verificare retry, `max_retries` e stato terminale.
- [ ] Verificare active key e idempotenza.
- [ ] Verificare fencing: una lease vecchia non può completare il job.
- [ ] Verificare reaper e recovery di lease scadute.
- [ ] Verificare server e worker separati quando supportato.

### PR5.4 — Workflow YouTube reale

- [ ] Usare un video pubblico sandbox stabile.
- [ ] Eseguire search e video info.
- [ ] Eseguire fetch/download e segment extraction.
- [ ] Verificare durata, codec e integrità con `ffprobe`.
- [ ] Verificare transcript/subtitle o Whisper fallback.
- [ ] Verificare registrazione asset, metadata, location, processing e version.
- [ ] Verificare upload Drive sandbox e link persistito.
- [ ] Ripetere la richiesta e verificare idempotenza e assenza di doppia indicizzazione.
- [ ] Eseguire cleanup tramite API canonica.

### PR5.5 — Workflow Artlist reale

- [ ] Verificare diagnostics e scraper health.
- [ ] Eseguire ricerca live e creare un run.
- [ ] Monitorare discovery, download HTTP/HLS, FFmpeg, hash, upload e persistenza.
- [ ] Verificare durata, risoluzione e codec attesi.
- [ ] Verificare `drive_link`, `file_hash` e `local_path` dallo stesso owner canonico.
- [ ] Verificare strategie `verify`, `skip` e `replace`.
- [ ] Verificare restart scraper e comportamento unavailable/timeout.
- [ ] Ripetere il run e verificare deduplicazione.

### PR5.6 — Script e Google Doc

- [ ] Preparare almeno due clip reali indicizzate.
- [ ] Eseguire `generate-from-clips` con titolo, lingua e tono.
- [ ] Verificare piano narrativo, script, `clip_scenes` e ID reali.
- [ ] Verificare regola una clip = una scena dove prevista.
- [ ] Verificare persistenza script, sezioni, outline e generation log.
- [ ] Verificare Google Doc nella cartella sandbox e `doc_link` accessibile.
- [ ] Verificare memory gate/cache e `force_refresh`.
- [ ] Eseguire batch e verificare progress.
- [ ] Simulare errore LLM e verificare stato diagnostico coerente.

### PR5.7 — Images e voiceover

- [ ] Certificare ogni provider dichiarato abilitato; gli altri sono `NOT CERTIFIED`.
- [ ] Eseguire search/generate/upload/sync immagini.
- [ ] Verificare dimensioni, metadata, repository e Drive.
- [ ] Eseguire generate/batch/sync voiceover.
- [ ] Verificare durata, codec, file non vuoto e associazione a script/scena.
- [ ] Verificare timeout, retry e failure del provider.

### PR5.8 — Fault injection e recovery

- [ ] Terminare server durante download.
- [ ] Terminare worker durante job running.
- [ ] Terminare scraper durante ricerca.
- [ ] Rendere temporaneamente Drive, Ollama o provider non raggiungibili.
- [ ] Simulare directory non scrivibile, file temporaneo mancante e lease scaduta.
- [ ] Riavviare i componenti e verificare requeue/failure/recovery secondo policy.
- [ ] Verificare zero job zombie, asset falsamente attivi, file parziali e upload duplicati.

### PR5.9 — Integrità, riconciliazione, backup e restore

- [ ] Verificare riferimenti asset/location/processing/version e duplicati per source ID, URL, hash e active key.
- [ ] Eseguire reconcile e sync Drive due volte; il secondo passaggio non deve correggere nulla di inatteso.
- [ ] Creare backup consistente di tutti i database.
- [ ] Salvare checksum e ripristinare in un ambiente vuoto.
- [ ] Avviare server e worker sul restore.
- [ ] Verificare job history, asset, script, Drive file e Google Doc referenziati.
- [ ] Documentare RPO, RTO e procedura reale.

### PR5.10 — Carico e stabilità CPU-first

- [ ] Registrare CPU, RAM, disco e numero worker della macchina.
- [ ] Misurare baseline idle.
- [ ] Eseguire job leggeri, ricerche e FFmpeg concorrenti entro limiti configurati.
- [ ] Misurare latenza p50/p95/p99, throughput e picco RAM.
- [ ] Verificare backpressure e responsività HTTP durante carico CPU-heavy.
- [ ] Verificare zero `database is locked` non gestiti.
- [ ] Eseguire soak test minimo 60 minuti.
- [ ] Verificare assenza di crescita continua di goroutine, memoria, file descriptor e temporanei.

### PR5.11 — Osservabilità

- [ ] Propagare correlation ID da HTTP a job, log, outbox e output.
- [ ] Verificare log strutturati per start, progress, retry, success e failure.
- [ ] Verificare metriche HTTP, jobs, workers e provider.
- [ ] Definire alert per job failed/zombie, queue depth, Drive/scraper unavailable, database error e disco basso.
- [ ] Diagnosticare tutti i fault PR5.8 usando soltanto API, log e metriche.

### PR5.12 — Deployment, upgrade e rollback

- [ ] Eseguire build da checkout pulito.
- [ ] Verificare modalità all-in-one e server/worker separati.
- [ ] Verificare directory persistenti, permessi, health check e restart.
- [ ] Verificare Docker build/container se supportato ufficialmente.
- [ ] Documentare start, stop, upgrade e rollback.
- [ ] Eseguire realmente un upgrade e un rollback con backup compatibile.

### PR5.13 — Automazione E2E

- [ ] Aggiungere `make e2e-local` per scenari deterministici.
- [ ] Aggiungere `make e2e-external` protetto da env flag.
- [ ] Aggiungere `make e2e-recovery`.
- [ ] Aggiungere `make e2e-report`.
- [ ] Assegnare timeout e cleanup a ogni scenario.
- [ ] Salvare report JSON/JUnit e log redatti come artifact CI.
- [ ] Eseguire E2E local sulle PR critiche ed E2E external prima del rilascio.

### PR5.14 — Matrice finale

| Area | Esito richiesto |
|---|---|
| Build e migration pulita | PASS |
| Health, doctor e metrics | PASS |
| API/auth/security | PASS |
| Job lifecycle/recovery | PASS |
| YouTube end-to-end | PASS |
| Artlist end-to-end | PASS |
| Script e Google Doc | PASS |
| Images/voiceover abilitate | PASS oppure NOT CERTIFIED |
| Integrity e reconcile | PASS |
| Backup e restore | PASS |
| Load e soak CPU-first | PASS |
| Observability | PASS |
| Deployment e rollback | PASS |
| Single source of truth strict | PASS |

- [ ] Ogni riga ha comando, timestamp, commit e percorso dell'evidenza.
- [ ] Nessun test obbligatorio è saltato.
- [ ] Ogni bug bloccante ha un regression test.
- [ ] Il commit certificato coincide con il candidato al rilascio.

## Comandi finali

```bash
make clean
make tidy-check
make vet
make lint
make test-unit
make coverage-check
make build
go test -race ./...
go run ./scripts/archcheck -strict
bash scripts/ci-architectural-checks.sh
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
git diff --exit-code docs/api/ACTIVE_API_GENERATED.md
make e2e-local
make e2e-external
make e2e-recovery
make e2e-report
```

## Exit gate

PipelineGen può essere dichiarato operativo al 100% soltanto per le capability con riga PASS, evidenza riproducibile, recovery verificato, backup/restore reale e single-source-of-truth strict verde.
