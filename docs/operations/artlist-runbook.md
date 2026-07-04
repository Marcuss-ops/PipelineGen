# Artlist Operator Runbook (P3.3, July 2026)

> **Stato**: LIVE
> **Data snapshot**: 2026-07-04
> **Repository**: `Marcuss-ops/PipelineGen`
> **Obiettivo**: SRE runbook per la gestione operativa dell'integrazione
> Artlist (P0.1 + P0.2 + P1.1 + P1.3 + P2.1 + P2.2 + P2.3 + P3.1 + P3.2
> + P3.3 chiusi su `origin/main`). Copre:
>
> 1. Interpretazione delle metriche Prometheus (P1.1 + P1.3)
> 2. Health probe (P1.3) — 3 fallimenti consecutivi = 1 alert
> 3. Failure modes comuni e remediation
> 4. Forward-pointers (PR-ARTLIST-* in flight)
>
> **Cross-references** (3 surfaces lockstep, godlike/06 SSOT):
> `ARCHITECTURE.md` §15 (architectural surface) + `AGENTS.md` DL-006 +
> DL-007 (critical rules) + questo runbook (operator surface) +
> `architecture/current.yaml#ART-002` (wave-tracker).

## 1. Regole di esecuzione

> Le regole seguenti sono **non negoziabili** per ogni operazione di
> manutenzione, debug, o modifica della configurazione Artlist. Il
> mancato rispetto di una di queste regole può causare silent-failure
> o silent-degradation (vedi `godlike/07` no-fake-availability in
> `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md`).

1. **Non modificare la dual download surface** (`downloader.go` Go-primary
   + `processor_download.go::downloadViaScraper` Node-fallback) senza
   prima chiudere `PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY` (deadline 2026-08-15).
   Le 2 superfici sono mutuamente esclusive per invocazione (per il
   `input.LocalPath != ""` short-circuit in `mediaProcessor.Process`).
   Modifiche che rompono la divider rule causano race condition o
   double-download.

2. **Non disabilitare le fail-closed gates in `WireArtlist`**. Le 4
   gates mandatory (Publisher / Dispatcher / ClipsRepo / Jobs.Service)
   + la 1 boot-time URL gate (`validateArtlistScraperURL`) sono
   bloccate da 4 TDD test in
   `internal/app/build_bundles_artlist_test.go`. Vedi `AGENTS.md`
   DL-006 per il contract completo.

3. **Non chiamare `*gdrive.Service`, `database/sql`, o `os/exec`
   direttamente da `internal/application/assets/providers/artlist/**`**.
   Tutto l'I/O esterno DEVE passare per i 8 canonical Pattern 0 ports
   documentati in `ARCHITECTURE.md` §15.2. Vedi `AGENTS.md` DL-007 per
   il contract completo.

4. **Non mischiare refactor, nuove feature, e test E2E** in un singolo
   commit. Ogni PR-ARTLIST-* deve essere atomic e auto-sufficiente
   (per `AGENTS.md` Pattern 5 + godlike/07 minimum-blast-radius).

5. **Non modificare la firma dei 4 `Path*` const** in
   `internal/infrastructure/artlist/downloader/metrics.go`
   (`PathBrowser` / `PathYTDLP` / `PathHTTP` / `PathHLS`). Valori
   mis-spellati creano nuove Prometheus time-series silenziosamente
   (textbook Prometheus footgun).

6. **Non cambiare la frequenza del tick del health probe** (60s) o la
   soglia di 3 fallimenti consecutivi senza aggiornare anche le TDD
   tests in `internal/infrastructure/artlist/health/probe_test.go`.
   La soglia è il `DefaultFailureThreshold` const — non un magic number.

7. **Non deployare senza validare le dipendenze esterne**:
   - Node scraper su `http://artlist-scraper:9123` (o altro URL configurato)
   - Google Drive OAuth2 token valido (vedi `scripts/regenerate_token.sh`)
   - Qdrant raggiungibile (per la proiezione dei clip indicizzati)

## 2. Operatività routine

### 2.1 Health probe interpretation (P1.3)

Il health probe (in `internal/infrastructure/artlist/health/probe.go`)
esegue un HTTP GET sul Node scraper URL ogni 60 secondi. Il probe
traccia 3 metriche Prometheus (vedi `metrics_artlist.go`):

| Metrica | Significato | Quando cresce |
|---------|-------------|---------------|
| `artlist_scraper_probe_total{result="success"}` | Probe HTTP 2xx/3xx ricevuto | Ogni tick con successo |
| `artlist_scraper_probe_total{result="failure"}` | Transport error (connection refused, timeout, DNS) | Ogni tick con fallimento |
| `artlist_scraper_health_alerts_total` | 3 fallimenti CONSECUTIVI rilevati | 1 per streak di 3 fallimenti |

**Interpretazione operator-facing**:

- `rate(artlist_scraper_probe_total{result="failure"}[5m]) > 0.1`
  → Node scraper sta avendo problemi di connettività (5%+ dei probe
  falliscono in 5 minuti). Verificare:
  - `docker ps` (se Node scraper gira in container)
  - `curl http://artlist-scraper:9123/search` (test diretto)
  - Logs del Node scraper (`docker logs artlist-scraper`)

- `artlist_scraper_health_alerts_total` aumenta di 1
  → 3 fallimenti CONSECUTIVI rilevati (3+ minuti di outage).
  Verificare se il servizio è stato riavviato o se c'è un problema
  di rete persistente. Se l'alert counter sale di 2+ in 10 minuti,
  considerare l'esecuzione della remediation §3.1 (Node scraper down).

- `artlist_scraper_probe_total` fermo (non cresce da > 5 min)
  → Il probe è bloccato. Verificare `process.status` (la probe
  goroutine potrebbe essere in deadlock). Restart del server
  necessario.

### 2.2 Download path distribution (P1.1)

Il counter `artlist_download_path_total{path}` traccia la distribuzione
dei path di download Artlist. 4 label canoniche (vedi `Path*` const in
`metrics.go`):

| Label | Significato | Quando fired |
|-------|-------------|--------------|
| `path="yt-dlp"` | HLS stream scaricato via yt-dlp | URL con `.m3u8` o HLS detection |
| `path="http"` | Progressive MP4 scaricato via http.Get | URL con `.mp4` diretto |
| `path="browser"` | Download via Node Puppeteer fallback (Node-fallback surface) | Reserved per PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY |
| `path="hls"` | Direct HLS senza yt-dlp (altro fallback) | Reserved per PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY |

**Disciplina per-attempt NON per-completion** (vedi P1.1 `Help` text):
un singolo `Download` che ritenta 3 volte su un HLS CDN flaky aggiunge
3 al counter. `rate()` è il tasso di **tentativi**, NON di successi.
Per calcolare il success rate, cross-referenziare con il counter
`job.StatusSUCCEEDED` via il reconciliation log Qdrant-001.

**Interpretazione operator-facing**:

- `rate(artlist_download_path_total{path="yt-dlp"}[5m]) / rate(artlist_download_path_total[5m]) > 0.7`
  → La maggior parte dei download usa HLS. Verificare che yt-dlp sia
  aggiornato e che i cookies di autenticazione Artlist siano validi.

- `rate(artlist_download_path_total{path="http"}[5m]) > 0`
  → Download progressivi in corso. Se il rate è sostenuto (>1/sec)
  per >10 min, verificare che il pattern di download non sia anomalo
  (possibile loop di retry su URL malformato).

- `rate(artlist_download_path_total{path="browser"}[5m]) > 0` (dopo PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY)
  → Node Puppeteer fallback attivo. Verificare che Playwright sia
  installato e che il browser sia in buono stato (no memory leak).

### 2.3 Lifecycle startup verification

Al boot del server, verificare che:

1. **Gate P0.1 superata**: se `Features.ArtlistEnabled=true` ma
   `External.ArtlistScraperServerURL=""`, il log DEVE mostrare:
   ```
   artlist: WireArtlist failed: ArtlistEnabled=true but ArtlistScraperServerURL is empty (ART-002 P0.1 gate)
   ```
   e `wiring.ArtlistSvc` deve essere `nil`. Endpoint Artlist
   restituiscono 503.

2. **Health probe avviato**: il log DEVE mostrare:
   ```
   artlist-scraper-health-probe: started (serverURL=http://..., interval=60s, threshold=3)
   ```
   La prima probe DEVE avvenire IMMEDIATAMENTE al boot (non dopo 60s),
   grazie al code-reviewer P1.3 fix (immediate first tick).

3. **WireArtlist success**: il log DEVE mostrare:
   ```
   artlist: WireArtlist OK (Publisher=ok, Dispatcher=ok, ClipsRepo=ok, JobsSvc=ok)
   ```

4. **Endpoint status**:
   - `GET /api/artlist/stats` → 200 (read-only, no fail-closed dependency)
   - `GET /api/artlist/diagnostics` → 200
   - `GET /api/artlist/search/live?term=test` → 200 o 503 (dipende da
     Node scraper + gate P0.1)
   - `POST /api/artlist/run` → 503 se wiring.ArtlistSvc=nil

## 3. Failure modes + remediation

### 3.1 Node scraper down

**Sintomo**: `artlist_scraper_health_alerts_total` aumenta. `GET /api/artlist/search/live` ritorna 503 o timeout. `/run` richieste falliscono con `ErrTransportFallback` + exec fallback (che può anche fallire se `node` non è in PATH).

**Diagnostica**:

```bash
# 1. Verifica Node scraper status
docker ps | grep artlist-scraper
curl -v http://artlist-scraper:9123/search -X POST -d '{"term":"test","limit":1}' -H 'Content-Type: application/json'

# 2. Verifica logs del Node scraper
docker logs artlist-scraper --tail 100

# 3. Verifica che node sia in PATH (per exec fallback)
which node && node --version
```

**Remediation**:

1. Se Node scraper container è down: `docker start artlist-scraper` o
   `docker-compose up -d artlist-scraper` (vedi `docker-compose.yml`).
2. Se Node scraper è in crash loop: `docker logs artlist-scraper` per
   diagnosticare (probabile: Playwright non installato, dipendenze
   mancanti, cookies Artlist scaduti).
3. Se exec fallback ritorna `ErrUnavailable` (node non in PATH):
   installare Node 20+ e Playwright (`npm install -g playwright &&
   npx playwright install chromium`).
4. Se il problema persiste, disabilitare temporaneamente Artlist:
   `VELOX_FEATURE_ARTLIST_ENABLED=false` (richiede restart del server).

### 3.2 Drive upload failure (5xx, auth, quota)

**Sintomo**: `/run` richieste falliscono con errori Drive (HTTP 5xx,
`ErrUnauthorized`, `ErrQuotaExceeded`). `delivery.Publisher.Publish` ritorna
errore typed.

**Diagnostica**:

```bash
# 1. Verifica token Drive
bash scripts/verify-canonical-config.sh
python3 scripts/generate_drive_token.py --check

# 2. Verifica quota Drive residua
curl -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
  http://localhost:8080/api/artlist/diagnostics | jq '.drive_quota'

# 3. Verifica error log recenti
grep -E 'drive.*(5[0-9]{2}|unauthorized|quota)' logs/*.log | tail -20
```

**Remediation**:

1. **HTTP 401 / 403** — Token Drive scaduto. Rigenerare:
   `python3 scripts/generate_drive_token.py` (vedi `AGENTS.md` §
   "Drive Token Regeneration").
2. **HTTP 429 / quota** — Quota Drive esaurita. Pulire la cartella
   `VELOX_DRIVE_FOLDER` o richiedere aumento quota.
3. **HTTP 5xx persistente** — Possibile outage Google. Verificare
   [Google Workspace Status Dashboard](https://www.google.com/appsstatus)
   e mettere in pausa le `/run` finché il servizio non torna.

### 3.3 Scraper / download rate limit (Artlist 429)

**Sintomo**: `artlist_download_path_total{path="yt-dlp"}` smette di
crescere, ma `/run` continua a essere chiamato. Log mostrano
`ErrThrottled` o HTTP 429 dal Node scraper.

**Diagnostica**:

```bash
# Verifica throttling nel log
grep -i 'throttl\|429\|rate.limit' logs/*.log | tail -20
```

**Remediation**:

1. Verificare che `pkg/retry` (`RetryAfterError` interface) stia
   onorando il `Retry-After` header (vedi P1.5 closure).
2. Se il rate limit persiste > 5 min, considerare il bump del
   `MaxConcurrentVideos` in `cfg.Monitor.RuntimePolicy` (default: 1).
3. Artlist può richiedere autenticazione attiva per rate limit
   elevati — verificare cookies Artlist in `cfg.ArtlistCookiesFile`.

### 3.4 E2E test failure in CI

**Sintomo**: `go test -short -count=1 -v ./tests/e2e/` fallisce su
uno dei 3 scenari (P2.1 live search / P2.2 full run / P2.3 fallback).

**Diagnostica**:

```bash
# Esegui un singolo test per isolare il fallimento
go test -short -count=1 -v -run TestE2E_Artlist_LiveSearch_NodeOn ./tests/e2e/
go test -short -count=1 -v -run TestE2E_Artlist_FullRun_WithDriveUpload ./tests/e2e/
go test -short -count=1 -v -run TestE2E_Artlist_Fallback_NodeDown ./tests/e2e/
```

**Remediation** (per scenario):

- **P2.1 fallisce** — Il contratto `scraper.Provider` response è
  cambiato. Aggiornare `tests/e2e/artlist_live_search_test.go` con il
  nuovo shape (4 subtests: happy + 4xx + ok=false + empty).
- **P2.2 fallisce** — Il mock Drive publisher signature è cambiato.
  Verificare `delivery.Publisher.PublishRequest` shape e aggiornare il
  mock in `tests/e2e/artlist_full_run_test.go::mockPublisher`.
- **P2.3 fallisce** — Il fallback path exec è cambiato. Verificare
  `scraper.go:108-117` exec branch e aggiornare le assertions
  `require.NotErrorIs(err, artapp.ErrTransportFallback)`.

### 3.5 Pre-existing build issues

Alcuni fallimenti `go build ./...` sono **pre-esistenti** e NON
regression dei PR-ARTLIST-*. Vedi
`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` per
la lista completa (5-7 items, tutti con deadline e owner).

**Workaround**: per validare SOLO le modifiche Artlist:

```bash
# Valida il subtree Artlist
go build ./internal/application/assets/providers/artlist/... \
        ./internal/infrastructure/artlist/... \
        ./internal/app/... \
        ./tests/e2e/...

# Valida i test Artlist
go test -short -count=1 -v \
  ./internal/application/assets/providers/artlist/... \
  ./internal/infrastructure/artlist/... \
  ./internal/infrastructure/observability/... \
  ./tests/e2e/
```

## 4. Forward-pointers (PR-ARTLIST-* in flight)

I seguenti ticket PR-ARTLIST-* sono in flight o pianificati, e
chiudono il debito residuo dell'integrazione Artlist. Per stato
aggiornato, vedi `architecture/current.yaml#ART-002.linked_issues`.

| Ticket | Deadline | Stato | Descrizione |
|--------|----------|-------|-------------|
| `PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY` | 2026-08-15 | pending | Collapse dual HLS detection + dual auth (Go-primary vs Node-fallback) in 1 canonical surface |
| `PR-ARTLIST-FAIL-CLOSED-SCRAPER-URL` | 2026-07-25 | pending (closure meta) | P0.1 boot-time gate (already shipped, wave-tracker audit-pin pending) |
| `PR-ARTLIST-DOWNLOAD-PATH-METRIC` | 2026-07-25 | pending (closure meta) | P1.1 Prometheus counter (already shipped, wave-tracker audit-pin pending) |
| `PR-ARTLIST-SCRAPER-HEALTH-PROBE` | 2026-07-25 | pending (closure meta) | P1.3 health probe (already shipped, wave-tracker audit-pin pending) |
| `PR-ARTLIST-E2E-P2-{1,2,3}` | 2026-07-25 | pending (closure meta) | P2.1 + P2.2 + P2.3 E2E tests (SHAs 6082f445 + a116881e + 7c3bd085 landed) |
| `PR-ARTLIST-STAGER` | 2026-08-15 | pending | `Stager` field wiring closure in WireArtlist |
| `PR-ARTLIST-LIFECYCLE` | 2026-08-15 | pending | `LifecycleService` field wiring closure |
| `PR-ARTLIST-REPOS` | 2026-08-15 | pending | `AssetProcRepo` + `AssetVerRepo` + `AssetLocRepo` field wiring closure |
| `PR-ARTLIST-SYNCSERVICE` | 2026-08-15 | pending | `ClipResolver` field wiring closure in `Build(Dependencies)` |
| `PR-ARTLIST-SEARCHERS` | 2026-07-25 | pending | `PixabaySearcher` + `PexelsSearcher` field wiring closure |
| `PR-ARTLIST-CHIP2` | 2026-08-15 | pending | chip-2 forward-pointer (residual outbox/typed-port hardening) |
| `PR-ARTLIST-STATS-MINIMAL-RESTORE` | 2026-07-25 | pending | minimal `GET /stats` route restoration post-FASE-6 |

## 5. Riferimenti canonici

- **`ARCHITECTURE.md` §15** — canonical architectural surface
- **`AGENTS.md` DL-006 + DL-007** — critical rules (composition-root + Pattern 0 routing)
- **`architecture/current.yaml#ART-002`** — wave-tracker entry
- **`internal/infrastructure/artlist/health/probe.go`** — health probe implementation
- **`internal/infrastructure/artlist/downloader/metrics.go`** — download path metric
- **`internal/infrastructure/observability/metrics_artlist.go`** — Prometheus metric definitions
- **`internal/app/build_bundles_artlist.go`** — composition-root wiring + gates
- **`internal/app/lifecycle_scheduler.go::buildSchedulerSteps`** — health probe lifecycle step
- **`tests/e2e/artlist_{live_search,full_run,fallback}_test.go`** — 3 E2E test scenarios

---

*Questo runbook è parte della chiusura wave ART-002 (P3.3, July 2026).
Per feedback o aggiornamenti, aprire un PR verso `origin main` con
tag `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` per
l'audit-trail dell'agente (per `AGENTS.md` Git-Lesson-3).*
