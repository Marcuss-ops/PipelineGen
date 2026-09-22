# Esposizione di rete di PipelineGen

Di default PipelineGen ascolta solo sull'interfaccia di loopback:

```env
VELOX_HOST=127.0.0.1
VELOX_PORT=8000
```

Questo rende il server raggiungibile solo dallo stesso computer. Per raggiungere il servizio da un altro computer sulla stessa rete locale, o da Internet, è necessario modificare il binding e aggiungere un reverse proxy HTTPS.

> **ATTENZIONE:** non esporre PipelineGen su Internet senza autenticazione e senza HTTPS. Il valore di default (`127.0.0.1`) è quello corretto quando si usa un reverse proxy locale come Nginx.

---

## 1. Esposizione sulla LAN

Per rendere PipelineGen raggiungibile da altri computer nella stessa rete locale, imposta il bind su tutte le interfacce.

Nel file `.env`:

```env
VELOX_HOST=0.0.0.0
VELOX_PORT=8000
```

Oppure in `config.yaml`:

```yaml
server:
  host: "0.0.0.0"
  port: 8000
```

Riavvia il servizio affinché la modifica abbia effetto:

```bash
sudo systemctl restart pipelinegen
```

Verifica che la porta sia in ascolto su tutte le interfacce:

```bash
sudo ss -lntp | grep :8000
```

L'output atteso è simile a:

```text
LISTEN 0 0 0.0.0.0:8000 ... pipelinegen
```

> Questa modalità va usata solo su una rete privata affidabile. Per esposizioni pubbliche, usare sempre il reverse proxy HTTPS descritto nella sezione 2.

### Firewall

Consenti la porta solo dalla tua rete privata. Sostituisci `192.168.1.0/24` con la rete effettiva:

```bash
sudo ufw allow from 192.168.1.0/24 to any port 8000 proto tcp
```

Verifica lo stato del firewall:

```bash
sudo ufw status
```

> **Non usare** `sudo ufw allow 8000` su una macchina raggiungibile da Internet: esporrebbe PipelineGen direttamente, senza il controllo di un reverse proxy.

---

## 2. Reverse proxy HTTPS (soluzione consigliata per esposizioni pubbliche)

Per computer esterni alla LAN, la soluzione corretta è lasciare PipelineGen in ascolto su loopback e posizionare Nginx davanti al servizio con SSL/TLS.

```text
Computer client
       HTTPS
   Nginx (443)
      ↓ HTTP locale
127.0.0.1:8000
      ↓
   PipelineGen
```

In questo caso lasci:

```env
VELOX_HOST=127.0.0.1
VELOX_PORT=8000
```

Esempio di configurazione Nginx in `/etc/nginx/sites-available/pipelinegen`:

```nginx
server {
    listen 443 ssl http2;
    server_name pipeline.example.com;

    ssl_certificate /etc/letsencrypt/live/pipeline.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/pipeline.example.com/privkey.pem;

    client_max_body_size 10m;

    location / {
        proxy_pass http://127.0.0.1:8000;

        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_set_header Authorization $http_authorization;
        proxy_set_header Idempotency-Key $http_idempotency_key;
        proxy_set_header X-Request-ID $http_x_request_id;
        proxy_set_header X-Workspace-ID $http_x_workspace_id;

        proxy_connect_timeout 10s;
        proxy_send_timeout 60s;
        proxy_read_timeout 60s;

        proxy_buffering off;
    }
}
```

Verifica e ricarica Nginx:

```bash
sudo nginx -t
sudo systemctl reload nginx
```

### Header da non rimuovere

Il proxy deve inoltrare questi header senza modificarli:

- `Authorization`
- `Idempotency-Key`
- `X-Request-ID`
- `X-Workspace-ID`
- `Content-Type`

La regola più importante è questa: **il proxy non deve trasformare il path `/api/script/generate`, non deve rimuovere `Authorization` e non deve rimuovere `Idempotency-Key`.**

---

## 3. Verifica finale

Dopo aver modificato la configurazione:

```bash
sudo systemctl restart pipelinegen
sudo ss -lntp | grep :8000
```

Da un computer client nella stessa LAN (o tramite il dominio HTTPS):

```bash
curl -i https://pipeline.example.com/health
curl -i https://pipeline.example.com/ready
curl -s -H "Authorization: Bearer ${TOKEN}" https://pipeline.example.com/api/capabilities
```

Per il flusso asincrono di generazione script:

```bash
curl -i -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "X-Request-ID: $(uuidgen)" \
  https://pipeline.example.com/api/script/generate \
  -d '{ ... }'
```

L'endpoint restituisce `202 Accepted` con un `job_id`; il client deve poi eseguire polling su `/api/jobs/${JOB_ID}/full` finché lo stato non diventa `completed`, `failed`, `cancelled` o `dead_letter`.

Lo schema del body di `POST /api/script/generate` (envelope → `items[]` → `source` / `script_params` / `output` / `docs` / `audio` / `media_plan`) e' documentato in [`script-generate-payload-schema.md`](script-generate-payload-schema.md): il body e' fail-closed (`DisallowUnknownFields`), quindi un campo fuori posto e' un `400`, non un campo ignorato.

---

## 4. `VELOX_WORKER_TOKEN`: configurazione e propagazione ai worker

### Stato della configurazione

Il `VELOX_WORKER_TOKEN` è **configurato sul server** nel file env canonico
`/etc/pipelinegen/pipelinegen.env` (mode `0640`, owner
`root:pipelinegen-agents`), insieme a `VELOX_ADMIN_TOKEN`.

```env
VELOX_ADMIN_TOKEN=<64-hex>
VELOX_WORKER_TOKEN=<64-hex-diverso>
VELOX_PORT=8000
```

### Come il server lo carica

- **systemd (canone di avvio)**: `pipelinegen.service` avvia
  `bin/pipelinegen --mode server --config config.yaml` con
  `EnvironmentFile=/etc/pipelinegen/pipelinegen.env`; ogni modifica richiede
  `sudo systemctl restart pipelinegen`. **Non** avviare il processo a mano in
  parallelo: `start_server.sh` compete con il servizio per la porta 8000.
- **`start_server.sh` (solo sviluppo/E2E)**: il launcher manuale cattura
  `VELOX_WORKER_TOKEN` dall'ambiente systemd **prima** di fare `source .env`, e
  lo **ripristina** dopo — il `.env` del repository non può sovrascrivere il
  valore canonico (e se il canonico è assente, `unset` disattiva il token
  worker: `WorkerAuth` rifiuta con 500).
- **Config layer Go**: `cfg.Security.WorkerToken` ← `VELOX_WORKER_TOKEN`;
  ogni modifica richiede `sudo systemctl restart pipelinegen` (l'EnvironmentFile
  è letto solo all'avvio).
- **Verifica**: `make preflight` valida `.env`; `make doctor` (dopo il boot) verifica i token
  (64-hex obbligatorio, pattern placeholder rifiutati).

### Come i worker lo usano

I worker (client Go `pkg/veloxclient`, operatori) si autenticano con l'header
`Authorization: Bearer <VELOX_WORKER_TOKEN>`. Il token worker è **distinto** da
quello admin e serve per le superfici worker/server-to-server. (`scripts/velox_client.py`
non è più nel repository — eliminato dal commit `7e6965aab`; il client Go
`pkg/veloxclient` è l'unico client di riferimento rimasto.)

| Superficie | Token accettato | Note |
|---|---|---|
| `/api/*` (admin, job, media) | `VELOX_ADMIN_TOKEN` | `RequireAdminToken` rifiuta il token worker |
| `/internal/v1/jobs/claim` e altri `/internal/v1/*` | **solo** `VELOX_WORKER_TOKEN` | `WorkerAuth` rifiuta il token admin (difesa in profondità) |
| `/internal/v1/media/search` | worker + workspace reale | i worker sono forzati a workspace `default` → servono principal tenant o admin (`X-Workspace-ID`) |
| `/api/script/generate`, polling job | admin **o** worker | i worker non-admin usano `VELOX_WORKER_TOKEN` |
| `/api/v1/jobs`, `/api/v1/media` (M2M) | `VELOX_M2M_SECRET` (per-client) | altro principal: submitter remoto, scope `jobs.submit` / `jobs.read` / `media.read`; richiede `VELOX_ENABLE_M2M=true` |

Il client di riferimento Go (`pkg/veloxclient`) prende il token da
`VELOX_WORKER_TOKEN` e lo invia come Bearer; usa lo **stesso** valore
configurato sul server.

### Propagazione a un worker

1. Genera/ruota il token a mano (nessuno script di rotazione è presente nel
   repository: `scripts/rotate_token.sh` e `scripts/generate_worker_token.sh`
   sono stati eliminati dal commit `7e6965aab`, e `scripts/regenerate_token.sh`
   riguarda il token OAuth di Google Drive, non le credenziali admin/worker):
   ```bash
   openssl rand -hex 32                        # nuovo VELOX_WORKER_TOKEN (64-hex)
   sudoedit /etc/pipelinegen/pipelinegen.env   # sostituisci il valore (0640 root:pipelinegen-agents)
   sudo systemctl restart pipelinegen          # l'EnvironmentFile è letto solo all'avvio
   ```
2. Distribuisci il valore al worker (env var / secret manager — mai nei log
   o nei commit).
3. Riavvia il servizio e verifica (atteso `200`; tutti i campi di
   `ClaimCommand` sono opzionali):
   ```bash
   sudo systemctl restart pipelinegen
   curl -s -o /dev/null -w '%{http_code}\n' \
     -X POST http://127.0.0.1:8000/internal/v1/jobs/claim \
     -H "Authorization: Bearer $VELOX_WORKER_TOKEN" \
     -H 'Content-Type: application/json' \
     -d '{"worker_id":"verify-probe","capabilities":["youtube_clip.extract"]}'
   ```
4. **Il vecchio worker token viene invalidato solo se rigenerato**: i worker
   devono ricevere il nuovo valore prima del restart del server.

### Sicurezza

- I token non compaiono mai nei log: `scripts/systemd/pipelinegenctl` e i
  probe redigono `VELOX_(ADMIN|WORKER)_TOKEN` come `<REDACTED>`.
- La separazione admin/worker è un vincolo di sicurezza: un token admin
  **non** autentica `/internal/v1/*`, un token worker **non** autentica gli
  endpoint admin (bloccato dai test `TestWorkerAuth_RejectsAdminToken` /
  `TestRequireAdminToken_RejectsWorkerToken`).
- Rotazione admin/worker: procedura manuale (genera 64-hex, sostituisci nel
  secret file, riavvia il servizio, verifica l'ambiente del PID riavviato e
  `make auth-check`). Non esiste uno script di rotazione nel repository.

---

## 5. `VELOX_ENABLE_M2M`: accesso remoto ai job e agli elementi del DB

Il surface macchina-macchina (`/api/v1/jobs` per inviare/pollare i job e
`/api/v1/media` per leggere gli elementi della media SSOT con la stessa key)
usa un principal distinto da admin e worker: un secret **per client**
(`VELOX_M2M_SECRET`), salvato solo come `SHA-256` in `m2m_clients.secret_hash`.

```env
VELOX_ENABLE_M2M=true
```

- **`true`**: il Bearer viene risolto a una riga `m2m_clients` e ogni route
  verifica lo scope (`jobs.submit`, `jobs.read`, `media.read`).
- **`false`** (default del codice): la guardia passa in modalità pass-through
  (contesto admin) per non rompere i fixture dev/test. Su qualunque host
  raggiungibile **deve** essere `true`, altrimenti `/api/v1/jobs` e
  `/api/v1/media` rispondono senza key. I config del repository
  (`config.yaml`, `config.example.yaml`, `config.production.example.yaml`)
  ora impostano `enable_m2m: true`.

Provisioning di una key (admin):

```bash
curl -sS -X POST https://pipeline.example.com/api/v1/admin/m2m/keys \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"client_id":"computer-editor-77-01","scopes":["jobs.submit","jobs.read","media.read"]}'
```

Lettura remota degli elementi (M2M):

```bash
curl -sS -H "Authorization: Bearer $VELOX_M2M_SECRET" \
  "https://pipeline.example.com/api/v1/media/assets?source=youtube&limit=20"
```

Il polling dello stato job resta `GET /api/v1/jobs/{id}` (superficie M2M). La stessa superficie accetta tutti i job type con un handler realmente attivo sul Master: clip, script, stock, voiceover e quelli aggiunti successivamente al runtime. Il catalogo è disponibile con `GET /api/v1/jobs/types` (scope `jobs.read`); i tipi presenti solo nella policy ma senza consumer vengono rifiutati con `422`, così un PC remoto non può lasciare job non eseguibili in coda. Il body standard è `{type, payload, idempotency_key, project}`.

Da un secondo PC, con il file credenziali M2M già distribuito fuori dal repository:

```bash
export VELOX_M2M_ENV="$HOME/computer-editor-77-01.env"
set -a; . "$VELOX_M2M_ENV"; set +a
export VELOX_M2M=true

# enqueue generico: enqueue.json contiene {"type":"...","payload":{...}}
velox submit jobs --m2m --key progetto-01 --payload enqueue.json --json
velox poll <job_id> --m2m --interval 3s --timeout 30m
```

Il comando usa `POST /api/v1/jobs` per l’invio e `GET /api/v1/jobs/{id}` per il polling, riconosce gli stati `queued/running/completed` e `SUCCEEDED/FAILED/CANCELLED`, e restituisce codice d’uscita non-zero in caso di errore o timeout. Per gli endpoint admin legacy (`/api/script/generate`, `/api/clips/process`) resta disponibile il comando senza `--m2m`, con `VELOX_ADMIN_TOKEN`.

---

## Riassunto configurazione sicura

```env
VELOX_HOST=127.0.0.1
VELOX_PORT=8000
VELOX_ENABLE_AUTH=true
VELOX_ADMIN_TOKEN=<token-casuale>
VELOX_WORKER_TOKEN=<token-casuale-differente>
VELOX_BASE_URL=https://pipeline.example.com
```

Esponi solo Nginx sulle porte `80/443`, lascia chiusa la porta `8000` verso l'esterno e usa HTTPS.
