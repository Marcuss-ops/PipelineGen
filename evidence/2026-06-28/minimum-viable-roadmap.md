# Minimum Viable Production Roadmap
## 5 Pillar Criteria → Actionable Plan
**Generated:** 2026-06-28 · **Source:** user directive · **Status:** DRAFT, awaiting execution

---

## Premessa — Disallineamento critico

I 4 target `make verify`, `make e2e-grpc`, `make e2e-workload`, `make e2e-workload-mtls` **NON esistono** nel `Makefile` corrente di `Pipelinegen/`. Per design appartengono al repo **`RemoteCodex/native/worker-agent-go`** che non è ancora clonato su `/home/pierone`. Quindi:

- **Punto 2 (CI verde)** diventa una **doppia pipeline**: (a) istanza quei target nel Makefile attuale come “demo locale” di cosa _dovrebbe_ fare worker-agent-go, o (b) clonare worker-agent-go per davvero e usare i suoi target nativi.
- **Punti 1, 3, 4, 5** sono invece **completamente Pipelinegen-centrici** (master + WorkerAuth + outbox dedup + cosign bootstrap + canary rollout) e sono eseguibili da subito.

Decisione necessaria: vado in **(b)** oppure mi accontento di **(a)**? Il punto (b) richiede un URL Git valido.

---

## Punto 1 — Pulizia critica

### Cosa serve

| Sotto-punto | Stato attuale Pipelinegen | Azione |
|---|---|---|
| Niente doppio percorso Job/Task | ✅ già consolidato (`internal/jobs/`) | Nessuna azione. Verificare con `grep -r "duplicate.*job" .` |
| Niente executor finti | ✅ `cmd/worker/doctor_main.go` esiste; `internal/application/workerdoctor/default_probes.go` ha 8 probe reali | Nessuna azione. Verificare doctor compila: `go build ./cmd/worker` |
| Niente fallback legacy nel worker | Da verificare (potrebbe esistere qualcosa in worker.go) | `grep -rn "TODO\|fallback\|legacy" internal/application/jobs/worker/` — conferma zero fallback production path. Se trovi, sostituisci con errore tipizzato. |
| Niente binari compilati dentro Git | ❌ **PROBLEMA CONCRETO**: `bin/`, `pipelinegen`, `admin`, `worker` non in `.gitignore` completo | Aggiungere pattern a `.gitignore`: `/bin/`, `/pipelinegen`, `/admin`, `/worker`. POI eseguire `git rm --cached` per rimuovere i binari committati per errore. |

### Verifica

```bash
# Sotto-punto 1 - no dual path
grep -rn "if.*legacy\|TODO.*job" internal/application/jobs/ | grep -v _test.go

# Sotto-punto 4 - no binari in git
git ls-files | grep -E '^(bin/|pipelinegen$|admin$|worker$)' || echo "PASS"
```

---

## Punto 2 — CI verde (4 target)

### Cosa serve

| Target | Dove vive | Azione |
|---|---|---|
| `make verify` | `worker-agent-go/Makefile` (se clonato) | Clonare + eseguire |
| `make e2e-grpc` | `worker-agent-go/Makefile` | Clonare + eseguire |
| `make e2e-workload` | `worker-agent-go/Makefile` (needs `E2E_EXPECTED_SHA256`) | Clonare + catturare SHA256 di un artifact reale, poi eseguire |
| `make e2e-workload-mtls` | `worker-agent-go/Makefile` | Clonare + eseguire |

### Prerequisito bloccante

Il repo `worker-agent-go` non esiste su `/home/pierone` (verificato: `ls /home/pierone/Pyt/RemoteCodex/native/worker-agent-go` → No such file or directory). Senza URL Git non posso clonare. Possibili alternative:

1. **Chiedere URL all'utente** (GitHub, GitLab, self-hosted).
2. **Creare skeleton locale** di `worker-agent-go` con i 4 stub target, per dimostrare il wiring senza un repo reale.

### Verifica

```bash
cd /home/pierone/Pyt/RemoteCodex/native/worker-agent-go
make verify
make e2e-grpc
# cattura SHA di un artifact reale
SHA=$(docker exec pipelinegen-worker sha256sum /out/job-001.mp4 | awk '{print $1}')
E2E_EXPECTED_SHA256=$SHA make e2e-workload
E2E_EXPECTED_SHA256=$SHA make e2e-workload-mtls
```

---

## Punto 3 — Un Job reale per worker remoto

### Lista di controllo (per ogni worker)

```text
worker CONNECTED          → cmd/worker heartbeat OK + register session
Task ricevuta             → internal/jobs remote broker claim OK
Task eseguita             → handler.Run(ctx, job.Task) completa senza errore
Job SUCCEEDED             → jobs.status = SUCCEEDED in SQLite
artifact READY            → artifacts.verified_at >= jobs.completed_at, sha256 notato
file video valido (ffprobe) → ffprobe -v error -i <path> -show_format → exit 0
SHA del file = database   → sha256sum file == artifacts.sha256
```

### Azioni concrete

1. `docker compose up -d pipelinegen-server pipelinegen-worker` (3+ worker via `--scale`)
2. Da CLI admin: `pipelinegen-admin submit-job --type video_render --input /tmp/test.mp4`
3. Polling `velox-client get-job-status --id <job-id>` fino a `SUCCEEDED`
4. `docker exec pipelinegen-worker find /out -name "*.mp4" -newer <start-ts>` → cattura path
5. `sha256sum <path>` → confronta con `jobs.payload.expected_sha256` (field da aggiungere se mancante)
6. `ffprobe -v error -i <path> -show_format -loglevel info` → exit code 0

### Verifica

```bash
# 6 step in uno script: scripts/operations/verify-job-receipt.sh (NUOVO)
bash scripts/operations/verify-job-receipt.sh <worker-id>
# exit 0 → tutti i 6 punti PASS
```

---

## Punto 4 — Fault injection: 1 worker spento

### Scenario

```text
1. docker compose up --scale pipelinegen-worker=2
2. Submit 1 Job reale che richiede 30+s
3. docker kill pipelinegen-worker-1  (SIGKILL, no graceful)
4. Attendi lease expiry: jobs.lease_ttl_seconds (canonical: 90s, configurabile)
5. Verifica worker-2 ha ricevuto la Task sub-claim dopo lease timeout
6. Attendi completamento
7. Verifica:
   - jobs.status = SUCCEEDED (NON FAILED)
   - artifacts = 1 (NON 2)                           ← no duplicate
   - tasks WHERE status NOT IN ('SUCCEEDED','FAILED','DEAD_LETTER') = 0  ← no stuck
```

### Prerequisiti nel codice (Pipelinegen — questi sono già in piedi, da verificare)

- ✅ `internal/application/jobs/worker/runner.go::checkRenew()` → lease recovery
- ✅ Lease reaper (`leaseReaper` PR-Reaper) → raccoglie task scadute
- ✅ Idempotency key + supersede (`internal/application/jobs/outbox/`) → no dup artifact
- ✅ WorkerAuth + master URL → altro worker può claimare

### Verifica

```bash
bash scripts/operations/verify-recovery-after-worker-crash.sh
# exit 0 → 1 worker spento, 1 task ripresa, 1 artifact finale, 0 stuck
```

---

## Punto 5 — Canary rollout

### Sequenza

```text
Fase 1: 1 worker solo
         submit 3 Job reali
         if any FAIL → STOP ROTOLLOUT, indietro
         else → procedi

Fase 2: 2-3 worker
         submit 10 Job (vari tipi: small/medium/large)
         if failure_rate > 1% OR artifacts_dup > 0 OR job_lost > 0 → STOP
         else → procedi

Fase 3: tutta la flotta
         docker compose up --scale pipelinegen-worker=10
         submit 30 Job
         monitor /metrics per 10 min
         if worker_idle_ticks anomalously low OR job_queue_depth > 50 → STOP
         else → rollout COMPLETATO
```

### Stop conditions (halt immediato)

| Condition | Threshold |
|---|---|
| failure_rate | > 1% |
| artifacts.duplicate (sha256 + job_id) | > 0 |
| job_lost (created, never SUCCEEDED/FAILED) | > 0 |
| active_tasks > task_slots | sempre |
| fallback_count | > 0 |
| python_emergency_path | > 0 |

### Esistente da riusare

- `evidence/2026-06-28/rollout-plan.json` — già generato in sessione precedente (5 fasi 0% → 100%)
- `scripts/cosign-sign.sh` — gates each worker immagine

### Verifica

```bash
bash scripts/operations/canary-rollout.sh \
    --phase-1 1-worker \
    --phase-2 3-workers \
    --phase-3 10-workers \
    --jobs-per-phase "3,10,30"
# exit 0 → rollout completato senza stop conditions
```

---

## Soglia minima di OK al “via libera”

```text
✅ codice critico pulito (no dual path, no fake executor, no legacy fallback, no bin in git)
✅ 4 test principali verdi (verify, e2e-grpc, e2e-workload, e2e-workload-mtls)
✅ ogni worker completa 1 Job end-to-end
✅ recovery dopo 1 worker spento verificato (1 artifact, 0 stuck)
✅ canary 1 → 3 → 10 worker completato (3 fasi PASS)
```

Quando **tutti e 5** sono verdi → il sistema può andare in produzione. **Niente soak 72h, niente DR completo, niente dashboard avanzate, niente ottimizzazioni PostgreSQL** sono richiesti per partire.

---

## Artefatti da creare (ordine di esecuzione)

| # | File | Status |
|---|------|-------|
| 1 | `evidence/2026-06-28/minimum-viable-roadmap.md` (questo file) | ✅ creato |
| 2 | `.gitignore` update (rimozione `/bin/`, `/pipelinegen`, `/admin`, `/worker`) | 🔴 TODO |
| 3 | `scripts/operations/verify-job-receipt.sh` (Punto 3) | 🔴 TODO |
| 4 | `scripts/operations/verify-recovery-after-worker-crash.sh` (Punto 4) | 🔴 TODO |
| 5 | `scripts/operations/canary-rollout.sh` (Punto 5) | 🔴 TODO |
| 6 | Clone `worker-agent-go` repo (Punto 2) | 🔴 BLOCKED da assenza URL |
| 7 | Update `evidence/2026-06-28/verdict.json` con 5 nuovi gate | 🔴 TODO |
