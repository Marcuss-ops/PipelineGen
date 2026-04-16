# API e trigger su questo PC

Documentazione per l’accesso alle API dal PC dove gira il server e per l’accensione dell’API da remoto (wake server).

---

## 1. Accesso API e documentazione (docs)

- **Se il server API è spento**: nessun endpoint è raggiungibile (né script, né voiceover, né documentazione). Le chiamate verso la porta dell’API (es. 5000 o 8080) vanno in timeout o connessione rifiutata.

- **Se il server API è acceso**:
  - **GET /api/docs** è disponibile e restituisce la documentazione completa in JSON (base_url, endpoints, curl, availability, wake_from_remote, ecc.).
  - **GET /health** è accessibile per il controllo stato.
  - Tutti gli altri endpoint (script, voiceover, client-config, create-master, ecc.) sono raggiungibili sullo stesso host e porta.

In sintesi: la documentazione e le API sono disponibili **solo quando il processo del server API è in esecuzione** sulla rispettiva porta.

---

## 2. Accensione API da remoto (wake server)

È disponibile uno script **wake_api_server.py** che permette di “accendere” l’API da remoto.

### Comportamento

- Resta in ascolto su una porta dedicata (default **4999**, modificabile con **WAKE_PORT**).
- **All’avvio**: se **AUTO_START_API=1** (default), verifica se l’API risponde; se non risponde, avvia subito il server API in background e in log compare il messaggio che il server API è partito e raggiungibile (es. `Server API partito e raggiungibile su http://.../api/docs`).
- **GET /wake** (o **GET /**): verifica se l’API sulla porta configurata risponde; se **non** risponde, avvia in background il comando di avvio dell’API e risponde in JSON; in log viene scritto quando il server API è partito.
- **GET /status**: risponde con `api_running` (true/false), `docs_url`, `api_port` e **wake_port** **senza** avviare nulla.

### Log

Tutti i messaggi sono scritti su **stderr** (prefisso `[wake]`), così compaiono in console e in `nohup ... >> file 2>&1`:
- `Wake server in ascolto su http://0.0.0.0:4999`
- `Docs API: http://...:8080/api/docs`
- `Avvio server API in background: ...` quando viene lanciato il processo API
- `Server API partito e raggiungibile su http://.../api/docs` quando l’API risponde dopo l’avvio (automatico o da /wake)

### Variabili d’ambiente

| Variabile   | Significato                          | Default   |
|------------|--------------------------------------|-----------|
| **WAKE_PORT** | Porta su cui resta in ascolto il wake server | 4999      |
| **API_PORT**  | Porta su cui gira (o deve girare) l’API      | 8080 (o 5000) |
| **AUTO_START_API** | Se "1" (default), all’avvio del wake server avvia anche l’API se non risponde; "0" = avvio solo su GET /wake | 1 |
| **START_API_CMD** | Comando per avviare l’API in background (es. `python studio_app.py` o `python job_master_server.py`) | vedi script |

### Modalità solo API

Quando il wake avvia l’API, imposta **API_ONLY=1** (modalità solo API). Per far partire l’API in modalità completa: `export API_ONLY=0` prima di avviare il wake (o prima di eseguire `./run_wake_server.sh`).

### Avvio del wake server

Sul PC dove gira l’API, una volta o al boot:

```bash
cd /path/to/refactored
./run_wake_server.sh
```

Log di default in `/tmp/wake_api.log`. In alternativa:

```bash
cd /path/to/progetto
python3 wake_api_server.py
```

In background (es. dopo il boot):

```bash
nohup python3 wake_api_server.py >> /tmp/wake_api.log 2>&1 &
```

### Chiamate da remoto

Per “accendere” l’API da un altro PC:

```bash
curl -s http://<IP_QUESTO_PC>:4999/wake
```

Per verificare lo stato (senza avviare):

```bash
curl -s http://<IP_QUESTO_PC>:4999/status
```

Risposta tipica di **/status**: `{"api_running": true, "docs_url": "http://...:8080/api/docs", "api_port": 8080, "wake_port": 4999}` (porte in base a API_PORT e WAKE_PORT).

---

## Riferimenti

- **GET /api/docs**: la risposta JSON include `availability`, `wake_from_remote`, **wake_port**, **wake_url** e **wake_status_url** con gli URL pronti per wake e status.
- **FRONTEND_API_CHIAMATE.md**: sezione 0 e uso del wake da remoto.
- **REMOTE_ENDPOINTS.md**: sezione "Accensione API da remoto (wake server)" con tabella endpoint e variabili.
