# Google Vids Headless Downloader — Design Spec
Date: 2026-04-27

## Overview
FastAPI server che automatizza export e download di video e immagini da Google Vids Pro, con endpoint on-demand e scheduler automatico.

## Stack
- Python + FastAPI
- Playwright (headless) — trigger export su Google Vids
- Google Drive API + OAuth2 — lista e download file
- APScheduler — sync automatico schedulato
- Storage locale `/downloads/videos/` e `/downloads/images/`

## Autenticazione
- `login.py`: script interattivo one-time, salva sessione in `session.json`
- Playwright carica `session.json` per ogni run headless
- Drive API usa `credentials.json` + `token.json` via OAuth2 flow

## Componenti
- `login.py` — login interattivo, salva session.json
- `playwright_client.py` — trigger export progetto Google Vids
- `drive_client.py` — lista progetti, scarica video/immagini via Drive API
- `scheduler.py` — APScheduler, sync configurabile (default: ogni notte alle 2:00)
- `storage.py` — salva in locale, hook opzionale per push cloud
- `main.py` — FastAPI app con tutti gli endpoint

## Endpoints
- `GET  /list` — lista progetti Google Vids disponibili
- `POST /export` — trigger export su un progetto specifico (`video_id`)
- `POST /download` — scarica video/immagini di un progetto (`video_id`, `type: video|image|all`)
- `POST /sync` — export + download in un unico step
- `GET  /status/{job_id}` — stato di un job asincrono

## Data Flow
1. Client chiama `POST /sync?video_id=xxx`
2. Playwright apre Google Vids headless, carica session.json, trigera export
3. Poll finché export completato (Drive API controlla disponibilità file)
4. Drive API scarica video MP4 + immagini in `/downloads/`
5. Response con path locali dei file scaricati

## Scheduler
- Default: ogni notte alle 02:00, scarica tutti i nuovi export
- Configurabile via `SCHEDULE_CRON` env var
- Log risultati in `logs/sync.log`

## Storage locale
```
downloads/
  videos/  → *.mp4
  images/  → *.png, *.jpg
logs/
  sync.log
```

## Config
`.env` file:
- `GOOGLE_CREDENTIALS_PATH` — path a credentials.json
- `SESSION_PATH` — path a session.json (default: session.json)
- `DOWNLOAD_DIR` — cartella download (default: ./downloads)
- `SCHEDULE_CRON` — cron expression scheduler (default: 0 2 * * *)
