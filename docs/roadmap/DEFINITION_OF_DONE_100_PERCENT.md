# Definition of Done — PipelineGen 100% operativo e pronto a scalare

## Scopo

Questa è la checklist finale e vincolante.

PipelineGen può essere dichiarato:

```text
100% operativo
production-ready
pronto a scalare
```

soltanto quando tutte le sezioni obbligatorie sono `[x]`, le prove sono collegate e il commit certificato coincide con il tag di release.

Una compilazione verde non basta. Un test locale non basta. Un backup mai ripristinato non basta. Un carico non misurato non basta.

## Identità della certificazione

Compilare prima della firma:

```text
Versione:
Commit SHA:
Tag:
Data:
Responsabile tecnico:
Ambiente staging:
Ambiente production target:
Link PR5:
Link PR6:
Link PR7:
Link PR8:
Link CI:
Link artifact test:
```

## Gate 1 — Repository e Git

- [ ] `main` è il branch canonico.
- [ ] branch protection attiva.
- [ ] push diretto su `main` bloccato o limitato.
- [ ] review obbligatoria.
- [ ] CI obbligatoria.
- [ ] branch di lavoro aggiornati con `origin/main`.
- [ ] nessun branch critico decine di commit indietro.
- [ ] nessun file generato committato.
- [ ] nessun database committato.
- [ ] nessun secret committato.
- [ ] `git status -sb` pulito sul commit certificato.
- [ ] `git diff <TAG>^..<TAG>` revisionato.
- [ ] `git log -n 10 --oneline` verificato dopo il push finale.

## Gate 2 — Build e dipendenze

- [ ] `go mod tidy` non genera diff.
- [ ] `go list ./...` verde.
- [ ] `go build ./...` verde.
- [ ] server compila.
- [ ] worker compila.
- [ ] admin compila.
- [ ] Node scraper installa con `npm ci --omit=dev`.
- [ ] `puppeteer` non è dipendenza diretta.
- [ ] `puppeteer-core` è l'unica dipendenza browser runtime.
- [ ] FFmpeg version registrata.
- [ ] yt-dlp version registrata.
- [ ] Node version registrata.
- [ ] Go version registrata.
- [ ] immagini Docker costruite senza cache.
- [ ] image digest registrati.
- [ ] SBOM o elenco dipendenze disponibile.

Comandi:

```bash
go mod tidy
git diff --exit-code -- go.mod go.sum
go list ./...
go build ./...
docker compose build --no-cache
```

## Gate 3 — Test

- [ ] `go test ./...` verde.
- [ ] `go test -race ./...` verde.
- [ ] `go test -count=1 ./...` verde.
- [ ] package concorrenti testati con ripetizione.
- [ ] zero `t.Skip` non classificati.
- [ ] test integration separati con build tag.
- [ ] test migration database nuovo verdi.
- [ ] test upgrade database esistente verdi.
- [ ] test job lifecycle verdi.
- [ ] test idempotenza verdi.
- [ ] test cancellation verdi.
- [ ] test typed-nil verdi.
- [ ] test YouTube metadata e thumbnails verdi.
- [ ] test VTT verdi.
- [ ] test Artlist verdi.
- [ ] test Qdrant adapter verdi.
- [ ] test Drive adapter verdi.
- [ ] test provider registry verdi.
- [ ] test API contract verdi.
- [ ] coverage gate verde.

Ricerca obbligatoria:

```bash
rg 't\.Skip\(' --type go
rg 'TODO.*test|FIXME.*test' --type go
```

## Gate 4 — Architettura

- [ ] PR5 YouTube capability split `verified`.
- [ ] root YouTube massimo 5–8 file production.
- [ ] nessun `Deps` oltre 10 campi senza eccezione approvata.
- [ ] zero alias compatibility YouTube.
- [ ] zero wrapper pass-through YouTube.
- [ ] API non importa infrastructure.
- [ ] application non importa Gin.
- [ ] application non esegue SQL diretto.
- [ ] application non esegue processi concreti.
- [ ] provider dispatch soltanto nel registry comune.
- [ ] composition root unico costruttore di adapter concreti.
- [ ] `architecture/migration.yaml` descrive il repository reale.
- [ ] `architecture/ownership.yaml` descrive owner reali.
- [ ] guardrail legacy attivo.
- [ ] `archcheck --strict` implementato.
- [ ] `go run ./scripts/archcheck --strict` verde.
- [ ] zero alias proibiti.
- [ ] zero wrapper proibiti.
- [ ] zero root legacy proibiti.
- [ ] zero SQL fuori infrastructure database.
- [ ] zero Gin fuori API.
- [ ] zero `os.Getenv` fuori config/app consentito.
- [ ] zero `os/exec` fuori infrastructure process/media consentito.
- [ ] zero eccezioni architetturali non temporizzate.

## Gate 5 — Docker e runtime

- [ ] target `server-runtime` costruisce.
- [ ] target `worker-runtime` costruisce.
- [ ] target `admin-runtime` costruisce.
- [ ] scraper image costruisce.
- [ ] un solo binario per runtime.
- [ ] server non contiene FFmpeg, yt-dlp e Python senza necessità documentata.
- [ ] worker contiene i tool richiesti.
- [ ] container non-root dove possibile.
- [ ] filesystem e volume permissions verificati.
- [ ] `docker compose config` verde.
- [ ] `docker compose up -d` verde.
- [ ] nessun crash loop.
- [ ] shutdown graceful.
- [ ] restart policy documentata.
- [ ] image tag versionato.
- [ ] image digest usato nel deployment production.

## Gate 6 — Health, readiness e metrics

- [ ] `/health` disponibile.
- [ ] `/ready` disponibile.
- [ ] `/metrics` disponibile e protetto quando necessario.
- [ ] health non dipende da servizi esterni non critici.
- [ ] readiness verifica dipendenze critiche.
- [ ] build info espone versione e commit.
- [ ] metriche job disponibili.
- [ ] metriche worker disponibili.
- [ ] metriche outbox disponibili.
- [ ] metriche provider disponibili.
- [ ] metriche SQLite disponibili.
- [ ] metriche disco disponibili.
- [ ] label cardinality controllata.
- [ ] dashboard staging disponibile.
- [ ] alert testati.

## Gate 7 — End-to-end

- [ ] YouTube metadata E2E verde.
- [ ] YouTube extraction E2E verde.
- [ ] YouTube segment multipli E2E verde.
- [ ] Artlist search E2E verde.
- [ ] Artlist process E2E verde.
- [ ] transcription E2E verde.
- [ ] Qdrant index/search E2E verde.
- [ ] Drive upload E2E verde.
- [ ] generate-from-clips E2E verde.
- [ ] Google Doc E2E verde.
- [ ] restart worker E2E verde.
- [ ] restart server E2E verde.
- [ ] failure provider E2E verde.
- [ ] retry non crea duplicati.
- [ ] cancellation pulisce risorse.
- [ ] output e metadata verificati.

## Gate 8 — Job system

- [ ] idempotency key per ogni job critico.
- [ ] lease documentata.
- [ ] fencing verificato.
- [ ] retry limitato.
- [ ] backoff e jitter.
- [ ] terminal states coerenti.
- [ ] zombie detection attiva.
- [ ] worker heartbeat osservabile.
- [ ] double claim testato.
- [ ] double completion impossibile.
- [ ] partial write recovery testato.
- [ ] ack perso testato.
- [ ] cancel testato.
- [ ] backlog recovery testato.

## Gate 9 — Database

- [ ] migration version uniche.
- [ ] database nuovo creato da zero.
- [ ] database esistente aggiornato.
- [ ] `PRAGMA integrity_check` verde.
- [ ] `PRAGMA foreign_key_check` verde.
- [ ] WAL configurato o scelta documentata.
- [ ] busy timeout configurato.
- [ ] indici query critiche verificati.
- [ ] backup consistente.
- [ ] restore riuscito.
- [ ] RTO misurato.
- [ ] RPO misurato.
- [ ] crescita mensile stimata.
- [ ] soglia migrazione PostgreSQL documentata.

## Gate 10 — Sicurezza

- [ ] gitleaks verde.
- [ ] govulncheck verde o eccezioni approvate.
- [ ] npm audit valutato.
- [ ] immagini scansionate.
- [ ] zero secret nei log.
- [ ] zero secret nelle immagini.
- [ ] secret rotabili.
- [ ] HMAC obbligatorio in produzione.
- [ ] replay protection verificata.
- [ ] rate limiting attivo.
- [ ] upload/body limit attivi.
- [ ] path traversal testato.
- [ ] CORS esplicito.
- [ ] endpoint admin protetti.
- [ ] Drive scope minimo.
- [ ] metriche protette.
- [ ] audit log sufficiente.
- [ ] security runbook disponibile.

## Gate 11 — Backup, restore e disaster recovery

- [ ] backup automatico configurato.
- [ ] retention definita.
- [ ] checksum verificato.
- [ ] copia off-host.
- [ ] restore staging completato.
- [ ] database ripristinato.
- [ ] config ripristinata.
- [ ] immagini disponibili.
- [ ] E2E post-restore verde.
- [ ] runbook disaster recovery presente.
- [ ] owner del restore nominato.
- [ ] RTO/RPO rispettati.

## Gate 12 — Operazioni

- [ ] dashboard principali disponibili.
- [ ] alert principali disponibili.
- [ ] escalation definita.
- [ ] runbook database locked.
- [ ] runbook disk full.
- [ ] runbook worker zombie.
- [ ] runbook Qdrant down.
- [ ] runbook Drive rate limit.
- [ ] runbook Artlist scraper down.
- [ ] runbook YouTube cookie/session failure.
- [ ] runbook migration failure.
- [ ] runbook rollback release.
- [ ] log retention definita.
- [ ] temp file cleanup monitorato.
- [ ] on-call o responsabilità operativa definita.

## Gate 13 — Scalabilità

- [ ] workload reale definito.
- [ ] target medio definito.
- [ ] target picco definito.
- [ ] target 2× definito.
- [ ] SLO definiti.
- [ ] load generator versionato.
- [ ] baseline single worker completata.
- [ ] 2 worker completati.
- [ ] 4 worker completati.
- [ ] 8 worker testati o limite documentato.
- [ ] zero job persi.
- [ ] zero duplicate completion.
- [ ] contention SQLite misurata.
- [ ] provider saturation misurata.
- [ ] failure injection completata.
- [ ] soak 24 ore completato.
- [ ] soak 72 ore completato.
- [ ] memory leak assente.
- [ ] temp growth controllata.
- [ ] backlog recovery entro SLO.
- [ ] capacity model disponibile.
- [ ] costo 1× e 2× stimato.
- [ ] autoscaling policy documentata.
- [ ] decisione SQLite/PostgreSQL approvata.
- [ ] GO/NO-GO firmato.

## Gate 14 — Release e rollback

- [ ] release candidate taggata.
- [ ] changelog disponibile.
- [ ] image digest associati.
- [ ] migration version registrata.
- [ ] config schema version registrata.
- [ ] known limits pubblicati.
- [ ] upgrade testato.
- [ ] rollback testato.
- [ ] immagini precedenti disponibili.
- [ ] backup pre-release disponibile.
- [ ] deploy staging riuscito.
- [ ] deploy production controllato.
- [ ] post-deploy smoke verde.
- [ ] rollback window definita.

## Gate 15 — Documentazione

- [ ] roadmap README aggiornata.
- [ ] PR5 aggiornata.
- [ ] PR6 aggiornata.
- [ ] PR7 report disponibile.
- [ ] PR8 report disponibile.
- [ ] architecture tracker aggiornato.
- [ ] ownership aggiornata.
- [ ] API docs generate aggiornate.
- [ ] config example aggiornato.
- [ ] runbook aggiornati.
- [ ] documenti storici marcati come storici.
- [ ] nessuna checklist falsa o stale.

## Comando finale di certificazione

Eseguire da checkout pulito del tag candidato:

```bash
git status -sb
gofmt -w .
git diff --exit-code
go mod tidy
git diff --exit-code -- go.mod go.sum
go list ./...
go build ./...
go vet ./...
go test ./...
go test -race ./...
go run ./scripts/archcheck --strict
bash scripts/ci-architectural-checks.sh
docker compose config
docker compose build --no-cache
docker compose up -d
```

Poi eseguire:

- smoke test;
- E2E matrix;
- backup/restore check;
- load test target;
- load test 2×;
- failure injection essenziale;
- post-deploy verification.

## Firma finale

```text
[ ] APPROVATO — 100% operativo
[ ] APPROVATO — pronto a scalare al workload dichiarato
[ ] APPROVATO — release v________________

Commit certificato: ________________________________
Tag certificato: ____________________________________
Data: ______________________________________________
Responsabile tecnico: _______________________________
Risultato GO/NO-GO: _________________________________
Limiti dichiarati: __________________________________
```

## Regola assoluta

Se il codice cambia dopo la certificazione:

- build, test, CI e smoke test devono essere rieseguiti;
- se cambia job, database, provider, Docker o concurrency, PR7 e PR8 devono essere rieseguite nelle parti interessate;
- il tag precedente resta storico e non certifica il nuovo commit.

Il 100% è una proprietà di un commit e di un ambiente dichiarato, non una qualità permanente del nome del repository.
