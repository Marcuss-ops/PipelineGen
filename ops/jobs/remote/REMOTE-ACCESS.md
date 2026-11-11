# Accesso remoto al catalogo media PipelineGen (database + file)

Questa guida collega i tuoi altri computer al **master**, il computer che
possiede davvero il database e i file. I computer remoti non copiano il
database: leggono e scaricano tutto dal master tramite l'API M2M.

Perché non copiare `media.db.sqlite`: SQLite su una cartella condivisa (NFS,
SMB, Drive) si corrompe con scritture concorrenti. La regola del repository è
una sola: **il master è l'unica autorità**, gli altri computer sono client.

```
PC remoto  ──HTTPS + chiave M2M──▶  Master (Nginx 443 → PipelineGen 127.0.0.1:8000)
                                          ├─ PostgreSQL media SSOT
                                          ├─ SQLite operativo (job, cache)
                                          └─ file locali / Google Drive
```

## Cosa vede un PC remoto

Con una chiave M2M e lo scope `media.read`:

| Cosa | Endpoint |
|---|---|
| Cercare / filtrare gli asset | `GET /api/v1/media/assets` |
| Dettaglio asset: metadati, posizioni, **Drive link**, **trascrizioni**, **info vettoriali** (famiglia, modello, dimensione) | `GET /api/v1/media/assets/{id}` |
| Conteggi per sorgente / tipo / stato | `GET /api/v1/media/facets` |
| Scaricare il file (locale o proxy da Drive) | `POST /api/v1/media/clips/{source}/clips/{id}/download` |
| Inviare un job di render / script / stock | `POST /api/v1/jobs` |
| Pollare lo stato del job | `GET /api/v1/jobs/{id}` |

Le trascrizioni sono nel campo `text_tracks` del dettaglio asset (lingua,
tipo, stato, testo completo). Le info vettoriali sono nel campo `vectors`
(famiglia, modello, dimensione, data) e `has_embedding` / `embedding_version`
sono già nell'elenco: si vede subito quali asset sono indicizzati e con quale
modello. Il download M2M richiede lo stesso scope `media.read`; non serve la
token di amministrazione.

## 1. Preparazione del master (una volta sola)

Il master deve ascoltare sulla rete e avere l'M2M attivo. Controlla
`refactored/config.yaml` o il file systemd:

```yaml
server:
  host: "127.0.0.1"   # solo Nginx locale; la 8000 non è pubblica
  port: 8000
security:
  enable_m2m: true
```

Verifica che la porta sia in ascolto:

```bash
sudo ss -lntp | grep :8000
```

Se hai appena aggiornato il codice, ricostruisci e riavvia il master per
attivare le nuove rotte (trascrizioni + download M2M):

```bash
cd refactored
# `make build-server` può fermarsi su rust-fetch per un mismatch ICU della
# macchina (non riguarda il server Go). Il server si compila comunque:
go build -trimpath -o bin/pipelinegen ./cmd/server
sudo systemctl restart pipelinegen
```

## 2. Creare una chiave per ogni computer

Esegui questo comando **sul master**. Una chiave per PC: se un PC viene perso,
revochi solo quella.

```bash
cd refactored

./ops/jobs/remote/provision-key.sh \
  --client-id pc-casa-01 \
  --description "PC casa - lettura catalogo e download" \
  --master-url https://77.93.152.122 \
  --env-file "$HOME/pc-casa-01.env"
```

L'URL pubblico attuale del master è `https://77.93.152.122`. Lo script:

- crea la riga in `m2m_clients` (solo `SHA-256` del segreto, mai il segreto);
- usa di default gli scope `jobs.submit,jobs.read,media.read`;
- scrive il file env con permessi `0600`, fuori dal repository.

Copialo sul PC remoto:

```bash
scp "$HOME/pc-casa-01.env" utente@pc-remoto:~/pc-casa-01.env
```

Sul PC remoto deve restare `0600`. Non metterlo mai in git.

## 3. Rete: accesso pubblico TLS

Il master è pubblicato su Internet attraverso Nginx con il certificato
Let's Encrypt per l'IP:

```text
https://77.93.152.122
```

Non serve Tailscale né una VPN. Nginx ascolta sulla 443 e inoltra a
PipelineGen su `127.0.0.1:8000`; la porta 8000 non è più esposta. Le API M2M
richiedono una chiave: senza `Authorization: Bearer ...` rispondono `401`.
Anche `/objects/` passa da Nginx verso l'object store locale, con URL firmati.

Il certificato IP è attendibile (niente `-k`) e viene rinnovato da Certbot in
modalità standalone sulla porta 80; il deploy hook
`/etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh` ricarica Nginx dopo il
rinnovo. La configurazione host-specific è
`scripts/deploy/nginx-pipelinegen-ip-objectstore.conf`; il profilo con dominio
è `scripts/deploy/nginx-pipelinegen-m2m.conf`.

Verifica dal master (o da un client):

```bash
curl -sS https://77.93.152.122/health
curl -sS -o /dev/null -w '%{http_code}\n' https://77.93.152.122/api/v1/media/facets  # 401 senza chiave
sudo ss -lntp | grep :8000   # solo 127.0.0.1
```

## 4. Installare il client

Sul PC remoto serve solo Python 3.8+ (nessuna libreria aggiuntiva). Copia
`chronon.py` dal repository:

```bash
scp refactored/ops/jobs/remote/chronon.py utente@pc-remoto:~/
```

Verifica la connessione:

```bash
python3 chronon.py facets --env ~/pc-casa-01.env
```

Se risponde con i conteggi, il PC sta leggendo il database del master.

### Verifica completa in un comando

`verify` controlla in sequenza, senza creare alcun job:

- `/health` e `/ready` (readiness reale, non solo liveness);
- chiave M2M e scope `media.read` (facets + elenco asset);
- dettaglio di un asset, con almeno una traccia `READY` e almeno un vettore;
- download reale in una directory temporanea, con verifica **SHA-256** contro
  `content_hash`, poi cancellato;
- scope `jobs.submit` (un POST volutamente invalido, senza job creato).

```bash
python3 chronon.py verify --env ~/pc-casa-01.env

# Con un asset preciso (utile per verificare trascrizioni e vettori noti)
python3 chronon.py verify \
  --asset-id yt_xboo56WYGh8_106_166_v1 \
  --download-id 1lEqAxjNWFXe3UpKNOpJrA2EU9izLPML2 \
  --env ~/pc-casa-01.env
```

Se la chiave è di sola lettura, aggiungi `--skip-jobs`. Se il PC non deve
scaricare file, aggiungi `--skip-download`. Se stai verificando un catalogo
ancora parziale e l'asset scelto non ha trascrizioni o vettori, usa
`--allow-empty-text` / `--allow-empty-vectors`: senza quei flag il controllo
fallisce, che è il comportamento voluto. Il comando esce `0` solo se tutti
i controlli passano; `--json` produce un esito machine-readable. Senza
`--asset-id`, cerca tra i primi 50 asset uno che abbia sia trascrizioni sia
vettori; senza `--download-id`, sceglie un asset **audio/video** con file
locale/Drive (la rotta M2M è la rotta clip; le immagini restano visibili nel
catalogo ma non sono scaricabili da questo comando) e scarica al massimo 50 MB
(`--max-download-bytes`). Se il primo candidato non è servito dal download,
prova i successivi cinque prima di fallire.

`verify-remote.sh` resta disponibile per provare anche l'invio e il polling di
un job reale (`--yes`), ma richiede `jq`; `chronon.py verify` non richiede
dipendenze ed è il controllo consigliato su un PC nuovo.

## 5. Comandi quotidiani

Tutti i flag comuni (`--env`, `--url`, `--json`, `--timeout`) vanno **dopo** il
sottocomando. `--key` è specifico di `submit`.

```bash
# Panoramica del catalogo
python3 chronon.py facets --env ~/pc-casa-01.env

# Elenco asset (con Drive presente o no)
python3 chronon.py assets --env ~/pc-casa-01.env --limit 30
python3 chronon.py assets --env ~/pc-casa-01.env --source youtube --search "dolly parton"

# Dettaglio: posizioni, Drive link, trascrizioni
python3 chronon.py asset yt_xxxxx --env ~/pc-casa-01.env

# Solo la trascrizione italiana (o tutte le lingue)
python3 chronon.py transcript yt_xxxxx --lang it --env ~/pc-casa-01.env
python3 chronon.py transcript yt_xxxxx --env ~/pc-casa-01.env

# Ricerca e download
python3 chronon.py search "tesla factory" --env ~/pc-casa-01.env
python3 chronon.py download yt_xxxxx -o clip.mp4 --env ~/pc-casa-01.env

# Ripresa dopo una connessione interrotta
python3 chronon.py download yt_xxxxx -o clip.mp4 --resume --env ~/pc-casa-01.env
```

Il download scrive prima un `.part` e rinomina solo a fine trasferimento: un
file interrotto non sembra mai completo. Calcola lo **SHA-256** e lo confronta
con `content_hash` del catalogo: se non coincide, elimina il file e fallisce.
Con `--resume` riparte dalla dimensione del `.part` usando `Range`; il master
supporta la ripresa sia per i file locali sia per il proxy Drive. Se l'asset è
ancora in render, il master risponde `409` con lo stato del job; riprova più
tardi.

## 6. Creare video dal PC remoto

Il render avviene sul master (o sui worker che lui governa): il PC remoto
invia il job e scarica il risultato.

```bash
# Cosa può eseguire questo master
python3 chronon.py submit --type ... --payload job.json --key progetto-01 --env ~/pc-casa-01.env
```

Per un payload già nel formato `{type, payload}`:

```bash
python3 chronon.py submit --payload job.json --key progetto-01 --env ~/pc-casa-01.env
python3 chronon.py job job_abcd1234 --env ~/pc-casa-01.env
```

Il client aggiunge `project` e `idempotency_key` se mancano. Il master accetta
solo i tipi marcati come esterni (external-safe): un tipo sconosciuto risponde
`422` e non crea alcun job. Per i flussi PREPARE/FINALIZE già presenti nel
repository puoi usare anche `ops/jobs/remote/verify-remote.sh` e
`ops/jobs/remote/run-flow.sh` con lo stesso file env.

## 7. Sicurezza e revoca

- Una chiave per PC, mai condivisa.
- Il file env è `0600` e fuori dal repository.
- Per revocare un PC: `./ops/jobs/remote/provision-key.sh --client-id pc-casa-01 --disable`.
- Per ruotare il segreto: `--rotate` (riscrive il file env; usa `--force` se
  esiste già).
- La chiave non dà accesso admin: non può cancellare asset, cambiare
  configurazione o leggere job di altri client.
- La chiave ha un rate limit per client (`rate_limit_rps`, default 2 RPS,
  burst 10) e al massimo 2 download concorrenti. Un `429` indica di attendere
  il `Retry-After`, non di cambiare chiave.
- La porta 8000 ascolta solo su `127.0.0.1`; l'unica porta pubblica è la 443
  di Nginx. La chiave M2M resta l'unica barriera applicativa: non condividerla
  e revocala subito se un PC viene perso.
- Il traffico è HTTPS con certificato IP attendibile; la chiave non viaggia in
  chiaro. Non disattivare TLS e non esporre la 8000.
- `/metrics` richiede un token (401 senza), mentre `/assets` e
  `/media/google-accounting` non sono serviti (404).

## 8. Se qualcosa non funziona

| Sintomo | Causa | Rimedio |
|---|---|---|
| `401` | chiave assente/errata | ricontrolla `VELOX_M2M_SECRET` nel file env |
| `403` | manca `media.read` | ricrea la chiave con gli scope completi |
| `404` su `/api/v1/media/...` | master vecchio | ricostruisci e riavvia il master |
| `404` su `/api/v1/media/clips/.../download` | build senza la rotta download | aggiorna il master e riavvia |
| `verify` → FAIL su `jobs_submit` | chiave senza `jobs.submit` | ricrea la chiave con gli scope completi, oppure usa `--skip-jobs` se è di sola lettura |
| `verify` → FAIL su `download` | asset senza file locale/Drive o file oltre il limite | prova `--download-id` con un asset che ha `has_local_file`/`has_drive_file`, o alza `--max-download-bytes` |
| `verify` → FAIL su `readiness` | il master risponde a `/health` ma non a `/ready` | controlla `/ready` sul master e i check segnalati (DB, Drive, worker) |
| `429` | rate limit per chiave o troppi download concorrenti | attendi `Retry-After`; non rigenerare la chiave |
| `hash non valido` | trasferimento corrotto o file Drive cambiato | riprova con `--resume`; se persiste, verifica `content_hash` e il file Drive |
| Connessione rifiutata | host/porta/firewall | `ss -lntp` sul master, IP corretto, regola UFW |
| Nessuna trascrizione | asset senza tracce READY | controlla `text_tracks` nel dettaglio o il job di materializzazione |

Per una diagnostica completa dal PC remoto:

```bash
cd refactored
ops/jobs/remote/verify-remote.sh --env ~/pc-casa-01.env
```

Lo script controlla liveness, autenticazione, lettura del catalogo e comandi,
senza creare job.
