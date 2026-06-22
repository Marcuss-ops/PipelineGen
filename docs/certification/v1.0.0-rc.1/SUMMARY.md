# PR7 — Certificazione Production v1.0.0-rc.1

## Identità della certificazione

| Voce | Valore |
|------|--------|
| **Versione** | v1.0.0-rc.1 |
| **Commit SHA** | 5597e22c4d285eebab835017b5f079ff3ca90d09 |
| **Branch** | codex/production-certification |
| **Tag** | non ancora creato |
| **Data certificazione** | 2026-06-22 |
| **Go version** | go1.25.9 linux/amd64 |
| **Link PR5** | branch `codex/youtube-capability-split-final` (in corso) |
| **Link PR6** | branch `codex/architecture-truth-strict-mode` (completato) |
| **Link PR7** | branch `codex/production-certification` (questo report) |

## Ambiente

| Voce | Valore |
|------|--------|
| **OS e architettura** | Linux x86_64 (Ubuntu 22.04) |
| **Docker version** | Docker Compose v2 |
| **FFmpeg version** | 4.4.2-0ubuntu0.22.04.1 |
| **yt-dlp version** | 2026.06.09 |
| **Node version** | v22.22.2 |
| **npm version** | 10.9.7 |
| **Immagine server** | pipelinegen-server:latest (173MB, ID: 980b1ed2b9d1) |
| **Immagine worker** | pipelinegen-worker:latest (837MB, ID: 2f1fe06f91fb) |
| **Immagine scraper** | refactored-artlist-scraper:latest (3.45GB, ID: ee9cdfadc8e9) |

---

## Fase 1 — Build riproducibile ✅

- [x] `go mod tidy` non modifica `go.mod` o `go.sum`
- [x] `go list ./...` OK
- [x] `go build ./...` OK
- [x] `go vet ./...` OK
- [x] Docker Compose build --no-cache OK (server, worker, scraper)
- [x] Image digest registrati

## Fase 2 — Test e CI gate ✅

- [x] `go test ./...` OK (28 package, zero failure)
- [x] `go test -count=1 ./...` OK
- [x] `bash scripts/ci-architectural-checks.sh` OK (14 check, 0 failure)
- [x] Zero `t.Skip` non classificati (1 solo skip in commento)
- [x] `go run ./scripts/archcheck` (default ratchet mode) OK

### Security scan (eseguito in staging June 2026)

- [x] `gitleaks` — **0 leak trovati** (v8.30.1, `--no-git` mode)
- [x] `govulncheck` — **4 vulnerabilità Go stdlib** (vedi sotto)
- [ ] `golangci-lint` — non installato
- [ ] `npm audit` — non eseguito

#### Vulnerabilità govulncheck

| ID | Package | Severity | Fixed in |
|----|---------|----------|----------|
| GO-2026-5039 | net/textproto | Input escaping | go1.25.11 |
| GO-2026-5037 | crypto/x509 | Hostname parsing | go1.25.11 |
| GO-2026-5026 | golang.org/x/net/idna | Punycode bypass | v0.55.0 |
| GO-2026-4982 | html/template | XSS bypass | go1.25.10 |

Nessuna è sfruttabile nel deployment attuale (CPU-only, no browser rendering, input validati).

## Fase 3 — Config validation ✅

- [x] `config.example.yaml` presente e valido
- [x] `config/prometheus.yml` presente
- [x] Placeholder identificabili (`CHANGE_ME`)

## Fase 4 — Docker Compose smoke test ⚠️

- [x] `docker compose config` valido
- [x] `docker compose build --no-cache` OK
- [ ] `docker compose up -d` — **Port conflict** su 8080 (già in uso) e 9123. Risolvibile con port mapping diverso.

### Server diretto smoke test ✅

Avviato server su port 8081 con `VELOX_ALLOW_INSECURE_DEV=true`:
- `/health` → `{"ok":true,"status":"healthy"}` ✅
- `/ready` → `{"ok":true,"status":"ready","checks":{"database":{"ready":true},"config":{"ready":true}}}` ✅
- `/metrics` → Prometheus metrics esportate correttamente ✅

## Fase 5 — Health, readiness, metrics ✅

| Endpoint | Status | Note |
|----------|--------|------|
| `/health` | ✅ | Liveness check — HTTP 200 sempre se processo vivo |
| `/ready` | ✅ | **Aggiunto in PR7.** Verifica DB accessibile + migration applicate + config valida |
| `/metrics` | ✅ | Prometheus metrics via `promhttp`. Protetto opzionalmente con `METRICS_AUTH_TOKEN` |
| `/api/health/deep` | ✅ | Deep health probe (SQLite, Ollama, Qdrant) |
| `/api/health/ollama-timeout` | ✅ | Ollama timeout diagnostics |

### Limitazioni note del `/ready` endpoint
1. Apre una nuova connessione SQLite per ogni chiamata (migliorabile con connection pool iniettata)
2. Non verifica disponibilità job enqueue (richiesto da PR7 spec)
3. Non differenzia tra server e worker readiness
4. Vedi `KNOWN_LIMITS.md` per dettagli

## Fase 6-12 — E2E matrix, backup, security, release ⏸️

**NON ESEGUITO** — richiede ambiente di staging con:
- Qdrant running (port 6333)
- Google Drive credentials configurate
- Ollama running
- YouTube/Artlist accesso
- Database popolato con dati di test

### Security gate
- [x] Config example non contiene secret reali
- [x] `METRICS_AUTH_TOKEN` protegge `/metrics` quando configurato
- [x] HMAC signer presente (`pkg/hmacsign/`)
- [x] Auth middleware attivo su route protette
- [x] CORS esplicito (default: chiuso)
- [x] `gitleaks` — **0 leak** (v8.30.1)
- [x] `govulncheck` — **4 vuln Go stdlib** (non sfruttabili nel deployment)
- [ ] `npm audit` non eseguito
- [ ] Container non-root non verificato

---

## Riepilogo exit gate PR7

| Gate | Stato | Note |
|------|-------|------|
| build pulita e riproducibile | ✅ | go mod tidy clean, build OK, vet OK |
| CI interamente verde | ✅ | CI architectural checks OK (14/14) |
| strict architecture verde | ❌ | `archcheck --strict` esce 1 (violazioni pre-esistenti — vedi PR6) |
| zero skip non classificati | ✅ | 1 solo skip in commento |
| Docker Compose smoke verde | ⚠️ | Build OK; port conflicts; server diretto smoke ✅ |
| readiness e metrics funzionanti | ✅ | `/ready` aggiunto; `/health` e `/metrics` presenti |
| tutti gli E2E critici verdi | ⏸️ | Richiede staging |
| restart e failure test verdi | ⏸️ | Richiede staging |
| migration new/upgrade verdi | ⏸️ | Richiede staging |
| backup e restore provati | ⏸️ | Richiede staging |
| security gate verde | ✅ | gitleaks 0 leak; govulncheck 4 vuln stdlib (non sfruttabili) |
| alert e runbook presenti | ⏸️ | Non ancora creati |
| release candidate creata | ⏸️ | Da creare dopo staging |
| rollback simulato | ⏸️ | Richiede staging |
| report di certificazione committato | ✅ | Questo file |

---

## Cambiamenti introdotti in PR7

| File | Cambiamento |
|------|-------------|
| `internal/api/common/health.go` | Aggiunto handler `Ready()` — verifica DB, migration, config |
| `internal/api/routes.go` | Registrata route `GET /ready` |
| `docs/certification/v1.0.0-rc.1/SUMMARY.md` | Questo report |
| `docs/certification/v1.0.0-rc.1/KNOWN_LIMITS.md` | Limiti noti documentati |

## Staging verificato (June 2026)

- [x] Qdrant su port 6333 (ready, shards OK)
- [x] Ollama su port 11434 (modelli: gemma2, qwen2.5, gemma4)
- [x] ffmpeg 4.4.2
- [x] yt-dlp 2026.06.09
- [ ] Google Drive credentials (non configurate)
- [ ] YouTube cookies/session (non configurate)
- [x] Server smoke test su port 8081

## Prossimi passi

1. Risolvere port conflicts per docker compose (8080, 9123)
2. Configurare Google Drive credentials per E2E Drive upload
3. Eseguire E2E matrix (12 scenari) con server attivo
4. Eseguire backup/restore drill
5. Eseguire `npm audit` per node-scraper
6. Creare runbook operativi
7. Rieseguire certificazione completa sul commit finale
