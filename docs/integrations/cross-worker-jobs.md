# Cross-worker job submission — cheat sheet

How a worker process on any machine (creator sidecar, sister microservice,
CI job, dev script) submits jobs to PipelineGen and tracks them to
completion. The contract is HTTP + bearer auth + idempotency-by-header.

> **Pre-requisiti lato server (PipelineGen):**
>
> 1. `VELOX_WORKER_TOKEN=<hex>` nell'env (vedi `scripts/generate_worker_token.sh`).
> 2. Bind accettabile: per la maggior parte dei setup basta
>    `server.host: 0.0.0.0` con Caddy/Nginx davanti per TLS. Se invece
>    esponi direttamente, leggi anche il paragrafo "TLS" in fondo.
> 3. La migration `036_job_idempotency.sql` deve essere stata applicata sul
>    DB; lo è di default al boot se è presente in `migrations/sqlite/`.
>
> Le righe guida sono già implementate — non devi patchare il server.

## 1. Auth: genera e distribuisci il WorkerToken

Sul server PipelineGen (questo host, o quello che vuoi autenticare):

```bash
# Genera un token casuale 256-bit (64 hex chars).
./scripts/generate_worker_token.sh

# Output: una stringa esadecimale di 64 caratteri, es.
# a1b2c3...  (copia/incolla nel prossimo comando)

# Output come .env pronto:
./scripts/generate_worker_token.sh --env
# → VELOX_WORKER_TOKEN=a1b2c3...
```

Metti quel valore come env var sul server PipelineGen:

```bash
export VELOX_WORKER_TOKEN=$(./scripts/generate_worker_token.sh)  # one-shot
# oppure in /etc/pipelinegen.env o in un secret manager
```

Sullo stesso valore (identico byte-per-byte) sul worker remoto:

```bash
export VELOX_WORKER_TOKEN=a1b2c3...        # identico a quello del server
```

**Mai** usare `VELOX_ADMIN_TOKEN` per i worker remoti — è un secret più
potente, separato, isolato. WorkerToken esiste apposta per loro.

## 2. URL del server

Tipici pattern (scegline uno):

- **Sulla stessa macchina del server** (dev / CI):
  `http://127.0.0.1:18080`
- **Stessa LAN, niente TLS**: `http://192.168.1.42:18080`
- **Internet, dietro reverse proxy con TLS**:
  `https://pipeline.tuodominio.com`

> **Attenzione:** su questa macchina la porta standard è `:18080`; `:8080`
> può essere occupato da SearXNG o altri servizi. Per scoprire la porta
> del pipelinegen in esecuzione:
>
> ```bash
> ss -ltnp | grep pipelinegen
> ```

## 3. Submit a job — cURL (qualsiasi linguaggio)

```bash
# id_unico stabile per questo submit: riusalo se ritenti.
REQ_ID="req-$(date -u +%Y%m%dT%H%M%S)-$RANDOM"

curl -sS -X POST "$URL/api/script/generate-with-images" \
  -H "Authorization: Bearer $VELOX_WORKER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Request-ID: $REQ_ID" \
  -d '{
        "topic": "storia di Roma",
        "sentences_per_image": 6,
        "generate_voiceover": true,
        "language": "it"
      }' | jq
# → {"ok":true,"job_id":"job_...","status":"queued",...}

# Salva il job_id e ripeti la stessa identica richiesta se la rete ha
# perso la risposta: il server restituirà lo stesso job_id (idempotency
# via (type, correlation_id) UNIQUE INDEX).
```

Per il payload corretto per gli altri endpoint vedi
`docs/API_REFERENCE.md#script-generation`.

## 4. Go client: `pkg/veloxclient/`

Importabile come modulo interno del repo, oppure copiabile in altri
progetti Go. Superficie stabile, stdlib-only.

```go
import "velox/go-master/pkg/veloxclient"

cli := veloxclient.New("https://pipeline.tuodominio.com", os.Getenv("VELOX_WORKER_TOKEN"),
    veloxclient.WithTimeout(60*time.Second))

// Submit — req_id vuoto = auto-generato (32-hex). Stabile attraverso i
// retry interni, quindi anche con network failure + retry nessun duplicato.
resp, err := cli.SubmitAsync(ctx,
    "api/script/generate-with-images",
    map[string]any{
        "topic":             "storia di Roma",
        "sentences_per_image": 6,
    },
    "") // oppure una stringa deterministica per retry-resilienza
if err != nil { /* vedi Error taxonomy sotto */ }

// Poll finché terminal.
for {
    st, err := cli.GetJobStatus(ctx, resp.JobID)
    if err != nil { return err }
    fmt.Printf("%s — %d%%\n", st.Status, st.Progress)
    if veloxclient.IsTerminal(st.Status) { break }
    time.Sleep(2 * time.Second)
}
```

### Error taxonomy

| Sentinella | Quando | Azione del caller |
|---|---|---|
| `ErrUnauthorized` | 401/403 | Ruota il token; niente retry |
| `ErrBadRequest` | 4xx non-auth | Fix payload; niente retry |
| `ErrNotFound` | 404 (lookup job) | Cattivo job_id o job pruned |
| `ErrServer` | 5xx / network dopo retry esauriti | Bubbles up, riprova più tardi |

### Retry policy interna

Il client ritenta automaticamente 5xx + errori di rete con backoff
esponenziale: 200 ms → 400 ms → 800 ms (3 tentativi totali di default).
Lo stesso `X-Request-ID` viene passato ad ogni tentativo, quindi lato
server la UNIQUE INDEX garantisce che **non** venga creato un job
duplicato anche se il primo POST era arrivato ma la risposta era persa.

## 5. Python client: `scripts/velox_client.py`

Per i worker Python (google-accounting sidecar incluso). Copia il file
nel progetto o aggiungilo come submodule / symlink. Stdlib-only — niente
`requests` / `httpx` quindi niente conflitti di versione.

```python
import os, time
from velox_client import VeloxClient, is_terminal, ServerError, AuthError

cli = VeloxClient(
    "https://pipeline.tuodominio.com",
    os.environ["VELOX_WORKER_TOKEN"],
    max_attempts=3,
    retry_base_ms=200,
)

# Submit
resp = cli.submit_async(
    "api/script/generate-with-images",
    {
        "topic": "storia di Roma",
        "sentences_per_image": 6,
    },
    req_id=f"req-{int(time.time())}",
)
job_id = resp["job_id"]

# Poll
while True:
    st = cli.get_job(job_id)
    print(f"{st['status']} — {st['progress']}%")
    if is_terminal(st["status"]):
        break
    time.sleep(2)

if st["status"] != "completed":
    raise RuntimeError(st["error"])
print(st["result"])
```

## 6. Idempotency: perché è critica

Una macchina remota che ritenta automaticamente su network error senza
idempotency farebbe **due job identici** di video generazione. Costo:
ore di compute, due video su Drive (uno da scartare), doppio consumo
delle quote API di Ollama/NVIDIA/Google.

La pipeline-risposta:

```
Worker → POST /api/script/... X-Request-ID: req-001
       → (network OK ma response lost)
Worker → POST /api/script/... X-Request-ID: req-001 (retry)
Server → {"job_id": "job_abc"}                     ← stesso job_id, niente duplicato
```

La magia è nella migration `migrations/sqlite/036_job_idempotency.sql`:

```sql
CREATE UNIQUE INDEX idx_jobs_type_correlation
    ON jobs(type, correlation_id)
    WHERE correlation_id != '';
```

Più la logica in `internal/jobs/service.go::Enqueue`: prima della
INSERT, fa `SELECT` per `(type, correlation_id)`; se c'è già, ritorna
quello invece di crearne uno nuovo. Dopo la INSERT, se SQLite butta
`UNIQUE constraint failed`, fa lo stesso fallback. Reti lossy + retry
aggressivo sono ora sicuri.

### Idempotency key — cosa mettere

- **Default**: lascia vuoto, il client genera un `uuidv4-like` 32-hex a
  runtime. Sufficiente per 99% dei casi.
- **Best (retries deterministici anche dopo riavvio del worker)**:
  derivalo dall'input, es.
  - `hash(topic + filename)` per le generazioni script+image
  - `sha256(absolute_file_path + mtime)` per i bulk upload
  - `request_id` passato dal chiamante a monte
- **Anti-pattern**: nulla di veramente random per ogni retry interno
  del client — il client già passa lo stesso req_id, non cambiarlo.

## 7. Token rotation

```bash
# 1) Sul server: genera il nuovo token
NEW=$(./scripts/generate_worker_token.sh)

# 2) Aggiorna env + restart (esempio con systemd)
sudo systemctl edit pipelinegen
# → Environment="VELOX_WORKER_TOKEN=$NEW"   (EnvFile= /etc/pipelinegen.env)
sudo systemctl restart pipelinegen
curl -sS http://127.0.0.1:18080/api/health   # OK

# 3) Aggiorna env su TUTTI i worker remoti (fai in batch — ci sono
#    molti modi; uno è Ansible, uno è Vault, uno è un .env deploy script)

# 4) Distruggi il vecchio token: non riutilizzarlo.
```

Mai mantenere attivi due token "in overlap" se non durante un cutover
controllato. Default: un solo token attivo.

## 8. TLS

PipelineGen è tipicamente dietro Caddy/Nginx per terminazione TLS. Stato:

- **Default attuale (dev / LAN)**: nessun TLS. Va bene per LAN trusted.
- **Per esporre su internet**: bind su `127.0.0.1` + Caddy davanti.
  Vedi `docs/operations.md` sezione "TLS termination" (TODO: scrivere).

Il client di default **valida** i certificati:

- Go: nessuna opzione speciale richiesta.
- Python: `verify_ssl=True` (il default del client).
- Bypass solo per cluster interni: `velox.New(..., WithInsecureTLS())` /
  `VeloxClient(..., verify_ssl=False)`. Mai in produzione.

## 9. Esempi pronti per copia-incolla

Crea `submit.sh` su un worker Linux:

```bash
#!/usr/bin/env bash
set -euo pipefail
: "${VELOX_WORKER_TOKEN:?missing}"
: "${PIPELINEGEN_URL:?missing}"
TOPIC="${1:?usage: $0 <topic>}"
curl -fsS -X POST "$PIPELINEGEN_URL/api/script/generate-with-images" \
  -H "Authorization: Bearer $VELOX_WORKER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Request-ID: req-$(date -u +%Y%m%dT%H%M%S)-$$" \
  -d "{\"topic\":\"$TOPIC\",\"sentences_per_image\":6}" | jq -r .job_id
```

```bash
chmod +x submit.sh
VELOX_WORKER_TOKEN="..." PIPELINEGEN_URL="http://192.168.1.42:18080" \
  ./submit.sh "storia di Roma"
```

— end —
