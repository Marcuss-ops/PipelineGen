# Runbook Automazione Clip

Guida pratica per eseguire il flusso operativo attuale:
`keyword -> download/process -> Drive -> DB sync`.

## Prerequisiti

- `yt-dlp` installato e raggiungibile.
- Credenziali Google Drive valide (`credentials.json`, `token.json`).
- Server avviabile su porta `8080`.

## 1) Avvio Server

```bash
cd /home/pierone/Pyt/VeloxEditing/refactored
go build -o ./bin/server ./src/go-master/cmd/server
./bin/server
```

Health check:

```bash
curl -sS http://127.0.0.1:8080/health
```

## 2) Test rapido pipeline dinamica keyword

Script smoke gia presente:

```bash
/home/pierone/Pyt/VeloxEditing/refactored/scripts/test_clipsearch_smoke.sh
```

Suite rapida end-to-end:

```bash
/home/pierone/Pyt/VeloxEditing/refactored/scripts/test_clipsearch_e2e_fast.sh
```

## 3) Generazione script con clip dinamiche + sync DB

```bash
curl -X POST http://127.0.0.1:8080/api/script-docs/generate \
  -H "Content-Type: application/json" \
  -d '{
    "topic":"Floyd Mayweather Highlights",
    "duration":60,
    "languages":["en"],
    "template":"documentary"
  }'
```

Quando la pipeline carica nuove clip, nei log devi vedere:

- `Dynamic clip uploaded in keyword folder`
- `Post-cycle DriveSync completed`
- `Post-cycle ArtlistSync completed`
- `Post-cycle CatalogSync completed`
- `Post-cycle DB sync completed`

## 4) Ricerca/Download YouTube v2 (base)

Ricerca:

```bash
curl -G "http://127.0.0.1:8080/api/youtube/v2/search" \
  --data-urlencode "query=Floyd Mayweather"
```

Ricerca top views ultima settimana:

```bash
curl -G "http://127.0.0.1:8080/api/youtube/v2/search" \
  --data-urlencode "query=Floyd Mayweather" \
  --data-urlencode "max_results=25" \
  --data-urlencode "sort_by=views" \
  --data-urlencode "upload_date=week"
```

Download:

```bash
curl -X POST "http://127.0.0.1:8080/api/youtube/v2/download" \
  -H "Content-Type: application/json" \
  -d '{
    "url":"https://www.youtube.com/watch?v=VIDEO_ID",
    "output_dir":"/tmp/velox/downloads"
  }'
```

Sottotitoli:

```bash
curl -G "http://127.0.0.1:8080/api/youtube/v2/subtitles" \
  --data-urlencode "video_id=VIDEO_ID" \
  --data-urlencode "language=en"
```

## 5) Abilitare servizi automatici background

Per abilitare monitor canali e scheduler stock:

```bash
export VELOX_ENABLE_CHANNEL_MONITOR=true
export VELOX_ENABLE_STOCK_SCHEDULER=true
./bin/server
```

Nota: senza queste env, monitor e stock scheduler non partono.

## 6) Config monitor canali

File atteso:

`/home/pierone/Pyt/VeloxEditing/refactored/data/channel_monitor_config.json`

Esempio minimo:

```json
{
  "check_interval": 86400000000000,
  "video_timeframe": "week",
  "stock_root_id": "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh",
  "ytdlp_path": "/home/pierone/venv/bin/yt-dlp",
  "cookies_path": "",
  "max_clip_duration": 60,
  "ollama_url": "http://localhost:11434",
  "channels": [
    {
      "url": "https://www.youtube.com/@VladimirTsvetov",
      "category": "Boxe",
      "keywords": ["floyd", "boxing", "interview"],
      "min_views": 10000,
      "max_clip_duration": 60
    }
  ]
}
```

`check_interval` e' in nanosecondi (`24h = 86400000000000`).

### API monitor canali (aggiunta/rimozione canali oltre keyword)

Route protette:

- `GET /api/monitor/channels`
- `POST /api/monitor/channels`
- `DELETE /api/monitor/channels`
- `POST /api/monitor/run` con `channel_url` per eseguire un solo canale

Esempio aggiunta canale:

```bash
curl -X POST http://127.0.0.1:8080/api/monitor/channels \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://www.youtube.com/@ALLTHESMOKEProductions",
    "category": "Boxe",
    "keywords": ["boxing", "interview", "fight"],
    "min_views": 20000,
    "max_clip_duration": 70
  }'
```

Esempio rimozione canale:

```bash
curl -X DELETE http://127.0.0.1:8080/api/monitor/channels \
  -H "Content-Type: application/json" \
  -d '{"url":"https://www.youtube.com/@ALLTHESMOKEProductions"}'
```

Esempio run manuale su un solo canale:

```bash
curl -X POST http://127.0.0.1:8080/api/monitor/run \
  -H "Content-Type: application/json" \
  -d '{"channel_url":"https://www.youtube.com/@VladimirTsvetov"}'
```

## 7) Test cron Artlist

```bash
/home/pierone/Pyt/VeloxEditing/refactored/scripts/test_cron_artlist_populate.sh
```

## 8) Gestione cron harvester via API

Nuove route disponibili (protette):

- `GET /api/harvester/cron/jobs`
- `POST /api/harvester/cron/jobs`
- `DELETE /api/harvester/cron/jobs/:id`
- `PUT /api/harvester/cron/jobs/:id/toggle`

Esempio creazione job settimanale:

```bash
curl -X POST http://127.0.0.1:8080/api/harvester/cron/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "mayweather-weekly",
    "query": "Floyd Mayweather interview",
    "channel": "",
    "interval": "weekly"
  }'
```

## 9) Limiti Operativi Attuali (importante)

- Il cron manager e' ora esposto via API, ma manca persistenza automatica dei job su disco al riavvio.

Per dettagli completi: `docs/STATO_FUNZIONALITA_AUTOMAZIONE.md`.
