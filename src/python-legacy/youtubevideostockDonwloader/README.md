# YouTube Video Stock Downloader + Processor

Scarica video da YouTube, crea clip, applica effetti e carica su Google Drive.

## Struttura

```
youtubevideostockDonwloader/
├── src/
│   ├── config/          # Configurazione
│   ├── downloader/     # Download YouTube
│   ├── clipper/        # Divisione e concatenazione video
│   ├── effects/        # Transizioni e effetti
│   └── uploader/       # Upload Google Drive
├── main.py             # Entry point
├── input_config.json   # Configurazione input
└── requirements.txt   # Dipendenze
```

## Installazione

```bash
pip install -r requirements.txt
```

## Utilizzo

### 1. Crea il file di configurazione

```json
{
    "urls": [
        "https://www.youtube.com/watch?v=VIDEO_ID",
        "https://www.youtube.com/playlist?list=PLAYLIST_ID"
    ],
    "final_duration": 1200,
    "clip_duration": 5,
    "segment_duration": 25
}
```

- `final_duration`: Durata totale desiderata in secondi (20 min = 1200s)
- `clip_duration`: Durata di ogni clip in secondi
- `segment_duration`: Durata di ogni segmento finale

### 2. Esegui il programma

```bash
# Dry run (vedi cosa verrebbe fatto)
python main.py --config input_config.json --dry-run

# Esecuzione completa
python main.py --config input_config.json --drive-folder FOLDER_ID
```

## Flusso di lavoro

1. **Download**: Scarica video da YouTube
2. **Clip**: Dividi in clip da X secondi
3. **Effetti**: Applica transizioni (ogni 4 clip) ed effetti (ogni 5 clip)
4. **Segmenti**: Concatena clip in segmenti da Y secondi
5. **Upload**: Carica segmenti su Google Drive

## Google Drive Setup

1. Vai su Google Cloud Console
2. Crea un progetto
3. Abilita Google Drive API
4. Crea credenziali OAuth 2.0
5. Scarica `credentials.json` nella cartella del progetto
