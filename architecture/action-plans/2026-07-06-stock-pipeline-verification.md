# Stock Pipeline Verification — Action Plan (2026-07-06)

**Status**: `in_progress`  
**Wave anchor**: `architecture/current.yaml#STOCK-PIPELINE-VERIFICATION-2026-07-06`  
**Parent wave**: `STOCK-E2E-BATTERY-2026-07-05`  
**Deadline**: 2026-07-15

---

## §1 — Context

This action plan captures the live verification session of the stock pipeline
against a running PipelineGen server on port `:8000` (PID 3005686).

### Live server state

| Component | Status | Detail |
|-----------|--------|--------|
| PipelineGen server | ✅ UP | Port 8000, PID 3005686 |
| `/health` (GET) | ✅ 200 | `{"status":"ok","db":"ok","drive":"ok","qdrant":"ok"}` |
| `/ready` (GET) | ❌ 503 | `"broker heartbeat stale: last heartbeat 9223372036854775807s ago"` — **nessun heartbeat mai emesso** |
| Admin auth | ⚠️ Mixed | Config `admin_token: test-admin-token-12345` non funziona; worker token `1ea8050625...` invece passa |
| yt-dlp | ✅ `2026.06.09` | Funzionante (testato con `jNQXAC9IVRw`) |
| ffmpeg / ffprobe | ✅ | Installati e funzionanti |

---

## §2 — STK-E2E Test Results (live run, 2026-07-06)

| Probe | Status | Detail |
|-------|--------|--------|
| **STK-E2E-A** Route Aliveness | ✅ PASS | `POST {}` → HTTP 400 `"search_queries or direct_urls required"` |
| **STK-E2E-C** Direct URL (async) | ❌ TIMEOUT | Job accettato ma mai processato — broker down → `RETRY_WAIT` |
| **STK-E2E-B** Search-and-Run | ⏭️ SKIP | Non eseguito (~27 min, broker necessario) |
| **Stock sync (async=false)** YouTube | ✅ PASS | 18s wall time, 5 artifact, download/cut/compose/publish OK |
| **Stock sync (async=false)** HTTP diretto | ❌ FAIL | `all sources failed to stage` — yt-dlp non supporta URL HTTP generici |

### Pre-flight mismatch (script vs server)

Gli script STK-E2E esistenti cercano `/healthz` e `/api/healthz` come pre-flight,
ma PipelineGen espone solo `/health` (GET) e `/ready`. Gli script vanno aggiornati
(vedi §4 Azione 03).

---

## §3 — Broker Stale Heartbeat Diagnosis

**Sintomo**: `GET /ready` → HTTP 503, `runner liveness probe failed: broker heartbeat stale: last heartbeat 9223372036854775807 seconds ago`

- `9223372036854775807` = `math.MaxInt64` → **nessun heartbeat mai registrato**
- I job stock in coda restano in `RETRY_WAIT` con `retry_count=1, max_retries=1, retryable=false`
- Il job handler `media.stock` non viene mai invocato perché il broker non segnala worker disponibili

**Root cause probabile**: il server è stato avviato in modalità `--mode all` ma il
worker interno non parte correttamente (o il broker non registra il runner).

**Azioni correttive**: vedi §4 Azione 02.

---

## §4 — Action Items (8 azioni, 3 bande di priorità)

### Banda P0 — Sbloccare il broker (deadline 2026-07-08)

| # | ID | Azione | Tipo |
|---|-----|--------|------|
| 01 | `PR-STOCK-BROKER-HEARTBEAT` | Diagnosticare il broker stale: `journalctl -u pipelinegen` + verificare log `worker_registry` / `runner.Register`. Riavviare il server con `--mode worker` esplicito. | Fix |
| 02 | `PR-STOCK-ADMIN-TOKEN` | Verificare perché `test-admin-token-12345` non funziona (config.yaml vs env var `VELOX_ADMIN_TOKEN`). Allineare. | Fix |

### Banda P1 — Script e test (deadline 2026-07-12)

| # | ID | Azione | Tipo |
|---|-----|--------|------|
| 03 | `PR-STK-E2E-PREFLIGHT-FIX` | Aggiornare pre-flight degli script STK-E2E: `/healthz` → `/health`. Aggiungere `--max-time` curl robusti. | Fix |
| 04 | `PR-STK-E2E-SYNC-MODE` | Aggiungere `async=false` come default negli script STK-E2E così funzionano anche senza broker. | Enhancement |
| 05 | `PR-STK-E2E-B-REAL-RUN` | Eseguire STK-E2E-B (search-and-run 9 folder) con `async=false` + YouTube queries per validare la pipeline completa. | Test |
| 06 | `PR-STK-E2E-D-REAL-RUN` | Eseguire STK-E2E-D (media_assets DB probe) per verificare che le proiezioni SQLite siano corrette post-run. | Test |

### Banda P2 — Qualità codice (deadline 2026-07-15)

| # | ID | Azione | Tipo |
|---|-----|--------|------|
| 07 | `PR-STOCK-SYNC-RESPONSE` | Correggere il messaggio di risposta del handler: in modalità `async=false` restituire `"Stock pipeline run completed"` invece di `"Stock pipeline job enqueued"`. Il `status_url` con `job_id` vuoto (`//full`) è fuorviante — omettere il campo per sync mode. | Fix |
| 08 | `PR-STOCK-DOCS-UPDATE` | Aggiornare `docs/operations/stock-e2e-runbook.md` con i findings della sessione: broker dipendenza, yt-dlp only, sync mode workaround. | Docs |

---

## §5 — Verification Gates (per wave closure)

Prima di flippare `STOCK-PIPELINE-VERIFICATION-2026-07-06` a `status: shipped`:

1. ✅ Broker funzionante (`GET /ready` → 200, heartbeat < 60s)
2. ✅ STK-E2E-A PASS (route aliveness)
3. ✅ STK-E2E-C PASS (direct URL, YouTube)
4. ✅ STK-E2E-B PASS (search-and-run, almeno 1/9 folder)
5. ✅ STK-E2E-D PASS (media_assets projection popolata)
6. ✅ Stock sync mode (async=false) completa senza errori bloccanti
7. ✅ Admin token funzionante (`test-admin-token-12345`)
8. ✅ Pre-flight script allineati con endpoint reali (`/health`, non `/healthz`)

---

## §6 — Honest Limitations (godlike/07)

- Il broker è down — finché non viene ripristinato, qualsiasi test async fallisce.
- yt-dlp supporta solo piattaforme video (YouTube, Vimeo, etc.) — URL HTTP diretti
  richiedono un downloader alternativo (curl/wget) che il `SourceStager` attuale non supporta.
- Il worker token usato nei test (`1ea8050625...`) è un token interno; l'admin token
  dichiarato in `config.yaml` non funziona — va verificato se c'è un override via env var.
- L'enrichment error (`no such column: status`) è non bloccante ma indica una possibile
  divergenza schema SQLite nella tabella `media_assets`.

---

## §7 — Cross-References (godlike/06 SSOT lockstep)

- `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` — parent wave (status: `in_progress`)
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — 6 carry-forward items (unchanged)
- `architecture/action-plans/2026-07-05-stock-e2e-battery.md` — canonical E2E narrative
- `docs/operations/stock-e2e-runbook.md` — operator-facing runbook (Phase I)
- `AGENTS.md § Stock E2E Runbook` — agent-facing fast reference
- `CHANGELOG.md ## Unreleased` — closure meta-entry (da aggiungere)

---

## §8 — Git Workflow (per AGENTS.md Git-Lesson-2)

Ogni azione atterra **direttamente su `main`**:

```bash
git fetch origin
git rebase origin/main                    # race-protect
git push origin main                      # fast-forward only, NO --force
```

**Anti-patterns vietati**: topic branches, PRs, `--force`, `--no-ff`, `commit --amend`
su commit già pushate.

**Commit convention** (AGENTS.md Git-Lesson-3):
```
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit -m '<subject>

<body>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'
```
