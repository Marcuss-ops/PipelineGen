# PipelineGen - Guida di Accesso e Funzionamento

## Informazioni di Accesso

- **URL Pubblico**: http://77.93.152.122:8080
- **URL Locale**: http://127.0.0.1:8080
- **Password Admin**: (salvata in `data/password.txt`)

## Configurazione Server

### Bind Address
Il server è configurato per ascoltare su tutte le interfacce:
```bash
Environment=VELOX_HOST=0.0.0.0
```

### Porte
- **8080**: API backend principale
- **5173**: Frontend React (solo sviluppo)

## Database Collegati

### velox.db.sqlite
Contiene le tabelle principali:
- `scripts` - Script di generazione video
- `monitored_sources` - Canali YouTube monitorati
- `harvester_jobs` - Job di raccolta contenuti
- `media_items` - Elementi media elaborati
- `media_files` - File associati ai media
- `media_tags` - Tag per categorizzazione
- `video_metadata` - Metadati video
- `script_stock_matches` - Corrispondenze script/stock
- `video_stats_history` - Storico statistiche
- `artlist_runs` - Esecuzioni pipeline Artlist

### artlist.db.sqlite
Database per gli asset Artlist:
- `clips` - Clip Artlist con metadati
- `clip_folders` - Cartelle organizzative
- `clips_fts` - Full-text search (fallback LIKE)
- `schema_migrations` - Versioning schema

## API Endpoints Principali

### Artlist
- `POST /api/artlist/run` - Avvia pipeline Artlist
- `GET /api/artlist/runs/:run_id` - Stato esecuzione
- `GET /api/artlist/diagnostics` - Diagnostica sistema
- `POST /api/artlist/search/live` - Ricerca live

### YouTube Clips
- `POST /api/youtube-clips/extract` - Estrai clip YouTube

### Jobs
- `GET /api/jobs` - Lista job
- `GET /api/jobs/:id` - Dettaglio job
- `POST /api/jobs` - Crea nuovo job

## Autenticazione

Il sistema usa token di sicurezza:
- `VELOX_ADMIN_TOKEN` - Token amministratore
- `VELOX_WORKER_TOKEN` - Token per worker

Se `VELOX_ENABLE_AUTH=true`, le API richiedono autenticazione.

## Gestione Servizio

### Avvio/Stop/Restart
```bash
sudo systemctl start pipelinegen
sudo systemctl stop pipelinegen
sudo systemctl restart pipelinegen
```

### Stato servizio
```bash
systemctl status pipelinegen --no-pager -l
```

### Log in tempo reale
```bash
journalctl -u pipelinegen -f
```

### Ricarica configurazione
```bash
sudo systemctl daemon-reload
sudo systemctl restart pipelinegen
```

## Firewall

Se non riesci ad accedere dall'esterno:

### UFW (Ubuntu)
```bash
sudo ufw allow 8080/tcp
sudo ufw reload
sudo ufw status
```

### Verifica porta aperta
```bash
ss -tlnp | grep 8080
```
Dovresti vedere: `0.0.0.0:8080`

## Workflow Tipico

1. **Inserimento script** → `scripts` table
2. **Ricerca stock** → Artlist API o ricerca stock
3. **Download asset** → Salvataggio in `data/downloads`
4. **Generazione clip** → Elaborazione video
5. **Upload Drive** → Caricamento su Google Drive

## Diagnostica Rapida

```bash
# Verifica servizio attivo
systemctl status pipelinegen --no-pager

# Test connessione locale
curl -I http://localhost:8080

# Test connessione pubblica (dal VPS)
curl -I http://77.93.152.122:8080

# Verifica database
sqlite3 data/velox.db.sqlite ".tables"
sqlite3 data/artlist.db.sqlite ".tables"

# Log errori
journalctl -u pipelinegen --since "1 hour ago" | grep -i error
```

## File Importanti

- **Configurazione**: `pkg/config/types.go`, `config.yaml`
- **Service systemd**: `/etc/systemd/system/pipelinegen.service`
- **Database**: `data/*.db.sqlite`
- **Log**: `journalctl -u pipelinegen`
- **Binario**: `pipelinegen` (nella root del progetto)
