# Checklist di certificazione per worker remoto

Owner: ops/infra + worker maintainer.
Reviewer richiesto: almeno un reviewer di runtime e uno di security.

---

## 1. Scheda di certificazione (template)

Da compilare per ogni worker remoto al termine del runbook 04.

```text
Worker ID:
Hostname:
Classe hardware:
Versione worker:
Versione engine:
Bundle version:
Protocol version:
Image digest:
Cert fingerprint:
Cert expiry:
Doctor verdict:
Canary job ID:
Canary task ID:
Canary attempt ID:
Artifact ID:
Artifact SHA-256:
Soak start:
Soak end:
Job eseguiti:
Success rate:
Failure count:
Reconnect test:
Worker crash test:
Master restart test:
Network partition test:
Drain test:
Fallback count:
Python emergency count:
Verdetto finale: PRODUCTION_READY | NOT_READY
Approvato da:
Data approvazione:
```

---

## 2. Gate di ammissione (manuale + automatico)

Un worker entra nell'allowlist production del master **solo** quando tutti i gate sotto sono verdi.

| Gate | Sorgente runbook | Comando di verifica |
|---|---|---|
| `Worker token claim = 200` | — (gate di auth, non runbook) | `curl` su `POST /internal/v1/jobs/claim` con `Authorization: Bearer $VELOX_WORKER_TOKEN` deve restituire **HTTP 200** con lease vuoto (vedi sotto) |
| `Doctor = READY` | RW-PROD-016 | `velox-worker-agent doctor --production --json` deve terminare con exit `0` e verdict `READY`. |
| `Canary mTLS = PASS` | RW-PROD-007 | Un canary reale mTLS termina `Job=SUCCEEDED` sul worker scelto, `TaskAttempt=SUCCEEDED`, artifact `READY`. |
| `Artifact integrity = PASS` | RW-PROD-008 | SHA-256 calcolato sul worker uguale a quello verificato sul master; `jobs.completed_at >= artifacts.verified_at`. |
| `Recovery suite = PASS` | RW-PROD-009, RW-PROD-010, RW-PROD-011 | Restart master, crash worker, network partition superati senza doppio artifact o doppio SUCCEEDED. |
| `Soak test = PASS` | RW-PROD-015 | 24h soak per classe hardware, success rate ≥ 99%, zero fallback/emergency in produzione. |
| `Fallback count = 0` | RW-PROD-003, RW-PROD-013 | Nessun fallback production, nessun Python emergency path attivato. |
| `Verdetto = PRODUCTION_READY` | sezione 5 | Scheda di certificazione firmata da reviewer approvati. |

### Verifica autenticazione worker (`VELOX_WORKER_TOKEN`)

Prima di qualsiasi gate funzionale, il worker deve dimostrare di possedere il
`VELOX_WORKER_TOKEN` **corrente** configurato sul master (file
`/etc/pipelinegen/pipelinegen.env`, mode `0640`):

```bash
curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST "${VELOX_MASTER_URL:-http://127.0.0.1:8000}/internal/v1/jobs/claim" \
  -H "Authorization: Bearer $VELOX_WORKER_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"worker_id":"cert-probe","capabilities":["cert.probe.noop"]}'
# atteso: 200
```

Criteri:

- **`200`** → il token è valido e il worker può procedere.
- **`401`** → token assente/sbagliato: allineare il worker al valore corrente
  del master (rigenerazione: `sudo scripts/rotate_token.sh --also-worker`).
- **`500`** → `VELOX_WORKER_TOKEN` non configurato sul master: completare la
  configurazione prima della certificazione.

> **Importante**: usare una capability che non matcha alcun job reale
> (es. `cert.probe.noop`). `/internal/v1/jobs/claim` non è un mero probe di
> connettività: se la capability matchese un job in coda, il probe lo
> claimerebbe per il worker fittizio `cert-probe`, bloccandolo fino alla
> scadenza del lease e ritardando il lavoro reale in produzione.

Il gate è obbligatorio perché `/internal/v1/*` accetta **solo** token worker
(`WorkerAuth` rifiuta i token admin); un worker che fallisce questo check non
può claimare job in produzione. (In produzione `VELOX_ENABLE_AUTH` è sempre
attivo; con auth disabilitato in dev/staging `WorkerAuth` tratterebbe ogni
principal come admin e restituirebbe `200` comunque.)

### Gate numerici minimi (soak test)

- job persi = 0
- artifact duplicati READY = 0
- artifact corrotti = 0
- OOM = 0
- disk full inattesi = 0
- task senza stato terminale = 0
- riconnessioni manuali = 0
- fallback production = 0
- success rate canary = 100%
- success rate carico normale ≥ 99%
- `active_tasks` mai oltre `task_slots`
- nessun throttling termico persistente

---

## 3. Procedura di approvazione

1. **Setup** (RW-PROD-001 → RW-PROD-006). Operatore esegue la validazione config e la bootstrap runtime. Output: `doctor --production` con tutti i check bloccanti PASS.
2. **Smoke** (RW-PROD-007 → RW-PROD-008). Canary mTLS e artifact integrity. Output: report JSON firmato e archiviato.
3. **Recovery** (RW-PROD-009 → RW-PROD-012). Suite di fault-injection (restart master, crash worker, network partition, drain). Output: zero doppioni, zero perdita task.
4. **Telemetria** (RW-PROD-013 → RW-PROD-014). Metriche, log e monitoraggio PKI attivi. Output: dashboard popolata, alert collegati (Prometheus → oncall).
5. **Soak** (RW-PROD-015). 24h di carico rappresentativo per classe hardware. Output: report firmato.
6. **Doctor finale** (RW-PROD-016). `velox-worker-agent doctor --production --json` rieseguito dopo soak e prima della promotion.
7. **Rollout** (RW-PROD-017). Canary worker → finestra di osservazione → promotion per classe.
8. **Firma**. Scheda di certificazione firmata da reviewer e master aggiornato con il worker in allowlist production.

---

## 4. Deroghe temporanee

Ogni deroga deve avere contemporaneamente:

- **Owner**: persona o team responsabile del rientro.
- **Motivazione**: perché la deroga è necessaria e per quanto tempo.
- **Scadenza**: data massima entro cui chiudere la deroga.
- **Ticket di rientro**: RW-PROD-xxx collegato.

Nessuna deroga è ammessa su: `mTLS non validato`, `cert scaduto/invalid`, `fallback o Python emergency attivi`, `more than one artifact finale READY per lo stesso output logico`, `active_tasks > task_slots`.

---

## 5. Audit trail

Conservare per tutta la vita operativa del worker:

- report JSON del `doctor --production`;
- report JSON del canary mTLS (job_id, task_id, attempt_id, artifact_id, sha256);
- report soak (24h) con metriche p50/p95/p99 e failure reasons;
- log strutturati con `worker_id`, `job_id`, `task_id`, `attempt_id`, `session_id`;
- inventario PKI (seriale, fingerprint, issued_at, expires_at).

Per il parco, conservare l'inventory delle schede di certificazione e una matrice `hardware_class × worker_id × verdict`.
