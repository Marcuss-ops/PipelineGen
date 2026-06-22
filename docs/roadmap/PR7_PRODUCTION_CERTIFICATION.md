# PR7 — Certificazione production

## Obiettivo

Dimostrare che un commit preciso di PipelineGen è realmente utilizzabile in produzione, non soltanto compilabile.

Questa fase certifica:

- build riproducibile;
- CI completa e osservabile;
- avvio di server, worker, admin e scraper;
- flussi end-to-end reali;
- sicurezza minima di produzione;
- backup e restore provati;
- metriche e alert;
- restart, retry e recovery;
- release versionata e rollbackabile.

PR7 non introduce nuove feature. Qualsiasi difetto trovato deve essere corretto con una PR mirata e poi la certificazione deve essere rieseguita dall'inizio sul nuovo commit.

## Branch e artefatti

Branch preparatorio:

```text
codex/production-certification
```

Artefatti da produrre:

```text
docs/certification/<version>/SUMMARY.md
docs/certification/<version>/COMMANDS.md
docs/certification/<version>/E2E_RESULTS.md
docs/certification/<version>/BACKUP_RESTORE.md
docs/certification/<version>/SECURITY.md
docs/certification/<version>/KNOWN_LIMITS.md
```

Gli output molto grandi, log, database e media non vanno committati. Salvare soltanto sintesi, checksum, link agli artifact CI e risultati riproducibili.

## Prerequisiti

PR7 inizia soltanto quando:

- [ ] PR5 è `verified`;
- [ ] PR6 è `verified`;
- [ ] `go run ./scripts/archcheck --strict` passa su `main`;
- [ ] non esistono test saltati non classificati;
- [ ] CI è attiva e required;
- [ ] Docker server/worker/admin è già separato;
- [ ] esiste un ambiente staging isolato.

## Ambiente di staging

Staging deve essere abbastanza simile alla produzione da rendere significativi i test.

Documentare:

| Voce | Valore |
|---|---|
| commit SHA | obbligatorio |
| versione release | obbligatorio |
| OS e architettura | obbligatorio |
| CPU e RAM | obbligatorio |
| spazio disco | obbligatorio |
| Docker version | obbligatorio |
| Go version | obbligatorio |
| database path/version | obbligatorio |
| Qdrant version | obbligatorio |
| yt-dlp version | obbligatorio |
| FFmpeg version | obbligatorio |
| Node version | obbligatorio |
| browser endpoint/version | se usato |

Lo staging non deve condividere:

- database production;
- cartelle Drive production;
- secret production;
- cookie o sessioni non necessarie;
- code job production.

## Fase 1 — Clean checkout e build riproducibile

Eseguire da una macchina pulita:

```bash
git clone https://github.com/Marcuss-ops/PipelineGen.git
cd PipelineGen
git checkout <COMMIT_SHA>
git status -sb
go version
go mod download
go mod tidy
git diff --exit-code -- go.mod go.sum
go list ./...
go build ./...
go vet ./...
```

Docker:

```bash
docker build --target server-runtime -t pipelinegen-server:<VERSION> .
docker build --target worker-runtime -t pipelinegen-worker:<VERSION> .
docker build --target admin-runtime -t pipelinegen-admin:<VERSION> .
docker build -f node-scraper/Dockerfile.scraper node-scraper -t pipelinegen-artlist:<VERSION>
```

Registrare:

```bash
docker image inspect pipelinegen-server:<VERSION> --format '{{.Id}} {{.Size}}'
docker image inspect pipelinegen-worker:<VERSION> --format '{{.Id}} {{.Size}}'
docker image inspect pipelinegen-admin:<VERSION> --format '{{.Id}} {{.Size}}'
docker image inspect pipelinegen-artlist:<VERSION> --format '{{.Id}} {{.Size}}'
```

Checklist:

- [ ] checkout pulito;
- [ ] `go mod tidy` non modifica file;
- [ ] build completa;
- [ ] immagini costruite senza cache almeno una volta;
- [ ] image digest registrati;
- [ ] nessun secret incorporato nelle immagini;
- [ ] server image non contiene tool worker inutili.

## Fase 2 — Test e CI gate

Eseguire localmente:

```bash
go test ./...
go test -race ./...
go test -count=1 ./...
go run ./scripts/archcheck --strict
bash scripts/ci-architectural-checks.sh
golangci-lint run --timeout=5m
govulncheck ./...
```

Controlli test:

```bash
rg 't\.Skip\(' --type go
rg 'TODO.*test|FIXME.*test' --type go
```

Regole:

- zero skip non classificati;
- test integration dietro build tag esplicito;
- nessun test dipende dall'ordine;
- almeno un run con `-count=10` sui package concorrenti;
- race detector obbligatorio sui package job, worker, outbox e YouTube.

GitHub Actions deve mostrare verdi:

```text
Build & Test
Lint
Vulnerability Scan
Secret Scanning
Strict Architecture
Docker Build
```

Nessun risultato manuale sostituisce una CI rossa.

## Fase 3 — Config validation e fail-fast

Preparare configurazioni:

```text
config/staging.valid.yaml
config/staging.invalid.yaml
```

Non committare secret reali.

Testare:

- [ ] file mancante;
- [ ] YAML malformato;
- [ ] placeholder non sostituito;
- [ ] secret obbligatorio mancante;
- [ ] porta occupata;
- [ ] data directory non scrivibile;
- [ ] URL Qdrant invalido;
- [ ] Drive credentials invalide;
- [ ] browser endpoint invalido;
- [ ] database path invalido.

Il processo deve fallire con messaggio chiaro e exit code non-zero, senza avviare parzialmente servizi background.

## Fase 4 — Docker Compose smoke test

```bash
docker compose down -v --remove-orphans
docker compose build --no-cache
docker compose up -d
docker compose ps
docker compose logs --no-color > /tmp/pipelinegen-compose.log
```

Verifiche minime:

```bash
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:8080/ready
curl -fsS http://127.0.0.1:9123/health
```

Se `/ready` non esiste, PR7 deve introdurlo prima della certificazione. `/health` non può sostituire readiness.

Checklist:

- [ ] server healthy;
- [ ] worker avviato;
- [ ] scraper healthy;
- [ ] Qdrant disponibile;
- [ ] nessun crash loop;
- [ ] nessun warning di configurazione ignorato;
- [ ] worker non espone porte inutili;
- [ ] restart policy documentata;
- [ ] shutdown pulito.

## Fase 5 — Health, readiness e metrics

Contratti richiesti:

### `/health`

Verifica soltanto che il processo sia vivo.

### `/ready`

Verifica le dipendenze necessarie al ruolo:

Server:

- database accessibile;
- migration applicate;
- job enqueue disponibile;
- config valida.

Worker:

- database/job broker accessibile;
- FFmpeg disponibile;
- yt-dlp disponibile;
- data directory scrivibile.

### `/metrics`

Metriche minime:

```text
pipelinegen_jobs_queued
pipelinegen_jobs_running
pipelinegen_jobs_completed_total
pipelinegen_jobs_failed_total
pipelinegen_job_duration_seconds
pipelinegen_job_retries_total
pipelinegen_job_lease_lost_total
pipelinegen_outbox_backlog
pipelinegen_worker_heartbeat_age_seconds
pipelinegen_external_errors_total{provider=...}
pipelinegen_sqlite_busy_total
pipelinegen_disk_free_bytes
pipelinegen_build_info{version,commit}
```

TODO:

- [ ] metriche senza cardinalità incontrollata;
- [ ] niente URL, clip ID o job ID come label;
- [ ] readiness fallisce quando una dipendenza critica manca;
- [ ] health resta vivo durante dipendenza esterna temporaneamente down;
- [ ] metriche protette se esposte fuori rete privata.

## Fase 6 — End-to-end matrix

Ogni scenario deve essere eseguito con input noto, output atteso e cleanup.

### E2E-01 — YouTube metadata

- input: URL video di test;
- output: metadata completo;
- assert: title, duration, thumbnails, language;
- failure: URL invalido.

### E2E-02 — YouTube segment extraction

- input: video e almeno due segmenti;
- output: file locali e record canonici;
- assert: durata, hash, filename, lifecycle state;
- assert: nessun duplicato al retry.

### E2E-03 — Artlist search

- input: query controllata;
- output: candidate normalizzate;
- assert: source, URL, metadata;
- failure: scraper indisponibile.

### E2E-04 — Artlist download/process

- output: file processato;
- assert: FFmpeg, hash, local path, Drive quando abilitato;
- retry idempotente.

### E2E-05 — Transcription

- input: audio/video breve;
- output: transcript e timestamp;
- assert: file non vuoto, ordine timestamp, cancellation.

### E2E-06 — Qdrant indexing/search

- indicizzare asset;
- cercare query nota;
- assert: ID atteso tra i risultati;
- ripetere upsert senza duplicazione.

### E2E-07 — Drive upload

- usare cartella staging;
- upload file;
- assert: file ID e link;
- retry non crea duplicato quando idempotency key è uguale.

### E2E-08 — Generate from clips

- input: clip ID reali con transcript;
- eseguire PlanNarrative e WriteScript;
- assert: `clip_scenes`, `script`, `doc_link`;
- assert: una clip reale per scena;
- testare `force_refresh`.

### E2E-09 — Google Doc

- assert titolo;
- assert Scenes JSON;
- assert contenuto script;
- assert link accessibile nell'ambiente staging.

### E2E-10 — Worker restart

- avviare job lungo;
- terminare worker;
- riavviare;
- assert: job recuperato o fallito in modo esplicito;
- assert: nessun doppio completamento.

### E2E-11 — Server restart

- enqueue job;
- riavviare server;
- worker continua;
- stato consultabile dopo restart.

### E2E-12 — External dependency failure

Simulare separatamente:

- Qdrant down;
- Drive rate limit;
- scraper down;
- yt-dlp failure;
- FFmpeg failure;
- Ollama timeout.

Assert:

- timeout finito;
- retry limitato;
- errore osservabile;
- job non bloccato per sempre;
- cleanup eseguito.

## Fase 7 — Idempotenza e job lifecycle

Per ogni job type critico documentare:

| Job type | Idempotency key | Lease | Retry | Terminal states | Cleanup |
|---|---|---|---|---|---|
| YouTube extraction | obbligatoria | sì | limitato | completed/failed/cancelled | sì |
| Artlist processing | obbligatoria | sì | limitato | completed/failed/cancelled | sì |
| Drive upload | obbligatoria | sì | limitato | completed/failed | sì |
| Qdrant index | asset+version | sì | limitato | completed/failed | n/a |
| Script generation | fingerprint | sì | limitato | completed/failed | n/a |

Test:

- enqueue doppio simultaneo;
- worker doppio;
- lease scaduta;
- ack perso;
- retry dopo partial write;
- cancel durante subprocess;
- zombie detection.

## Fase 8 — Database e migration certification

### Database nuovo

```bash
rm -f /tmp/pipelinegen-new.sqlite
go run ./cmd/admin <comando-migrate> --db /tmp/pipelinegen-new.sqlite
```

### Upgrade database esistente

- copia anonimizzata di staging;
- backup prima della migration;
- applicare migration;
- verificare schema e dati;
- verificare rollback applicativo.

Controlli:

- [ ] versioni migration uniche;
- [ ] migration idempotenti dove previsto;
- [ ] nessuna colonna duplicata;
- [ ] nessun dato perso;
- [ ] WAL e busy timeout configurati;
- [ ] foreign key abilitate;
- [ ] integrity check verde;
- [ ] query critiche con indice.

Comandi:

```bash
sqlite3 <DB> 'PRAGMA integrity_check;'
sqlite3 <DB> 'PRAGMA foreign_key_check;'
sqlite3 <DB> 'PRAGMA journal_mode;'
```

## Fase 9 — Backup e restore drill

Backup richiesti:

- SQLite consistente;
- file config senza secret o con secret manager;
- metadata necessari;
- elenco Drive/Qdrant collection;
- versioni immagini Docker;
- migration version.

Procedura:

1. fermare o quiescere writer secondo strategia;
2. creare backup consistente;
3. calcolare checksum;
4. copiare in destinazione separata;
5. distruggere ambiente staging;
6. ricreare infrastruttura;
7. ripristinare database e config;
8. riavviare server e worker;
9. rieseguire E2E read e write;
10. misurare RTO e RPO.

Exit:

```text
restore completato senza modifica manuale dei dati
RTO registrato
RPO registrato
checksum verificato
```

## Fase 10 — Security gate

Checklist:

- [ ] zero secret nel repository;
- [ ] zero secret nelle immagini;
- [ ] gitleaks verde;
- [ ] govulncheck verde o eccezioni documentate;
- [ ] npm audit valutato;
- [ ] immagini scansionate;
- [ ] HMAC obbligatorio in produzione;
- [ ] token admin ruotabile;
- [ ] Drive scope minimo;
- [ ] input path validati;
- [ ] upload size limit;
- [ ] request body limit;
- [ ] rate limiting;
- [ ] timeout HTTP;
- [ ] CORS esplicito;
- [ ] metriche protette;
- [ ] log senza token, cookie o secret;
- [ ] container non-root quando possibile.

## Fase 11 — Alert e runbook

Alert minimi:

- job failure rate sopra soglia;
- backlog crescente;
- worker heartbeat assente;
- outbox backlog;
- SQLite busy;
- disco sotto soglia;
- Drive/Qdrant/scraper error rate;
- restart loop;
- readiness rossa.

Runbook minimi:

```text
RUNBOOK_DATABASE_LOCKED.md
RUNBOOK_DISK_FULL.md
RUNBOOK_WORKER_ZOMBIE.md
RUNBOOK_DRIVE_RATE_LIMIT.md
RUNBOOK_QDRANT_DOWN.md
RUNBOOK_ARTLIST_SCRAPER_DOWN.md
RUNBOOK_YOUTUBE_COOKIES_EXPIRED.md
RUNBOOK_FAILED_MIGRATION.md
RUNBOOK_ROLLBACK_RELEASE.md
```

Ogni runbook deve includere:

- sintomo;
- metriche/log da guardare;
- diagnosi;
- azioni sicure;
- azioni da non fare;
- escalation;
- verifica finale.

## Fase 12 — Release e rollback

Tag suggerito:

```text
v1.0.0-rc.1
```

Poi, dopo certificazione:

```text
v1.0.0
```

Release deve contenere:

- commit SHA;
- changelog;
- migration version;
- image digest;
- config schema version;
- known limits;
- upgrade steps;
- rollback steps.

Rollback test:

- applicazione nuova → vecchia con schema compatibile;
- database backup restore;
- immagini precedenti disponibili;
- feature flags/documentazione.

Non pubblicare `v1.0.0` se il rollback non è stato almeno simulato in staging.

## Exit gate finale

PR7 è `done` quando:

- [ ] build pulita e riproducibile;
- [ ] CI interamente verde;
- [ ] strict architecture verde;
- [ ] zero skip non classificati;
- [ ] Docker Compose smoke verde;
- [ ] readiness e metrics funzionanti;
- [ ] tutti gli E2E critici verdi;
- [ ] restart e failure test verdi;
- [ ] migration new/upgrade verdi;
- [ ] backup e restore provati;
- [ ] security gate verde;
- [ ] alert e runbook presenti;
- [ ] release candidate creata;
- [ ] rollback simulato;
- [ ] report di certificazione committato;
- [ ] verifica rieseguita sul tag.

## Criterio di fallimento

La certificazione fallisce immediatamente se:

- un test critico è saltato;
- CI non è osservabile;
- un job può completarsi due volte;
- un restart perde stato;
- restore non funziona;
- secret compare in log o image;
- migration perde dati;
- il sistema richiede una correzione manuale non documentata.

Ogni fallimento genera una PR correttiva separata. Dopo il merge, PR7 riparte dal clean checkout del nuovo commit.
