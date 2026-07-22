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
