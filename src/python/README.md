# src/python/ — Ollama & Text Generation

Questo modulo contiene **solo** il codice Python attivo per la generazione testi via Ollama.
Tutto il Python legacy (video, audio, youtube downloader, worker, ecc.) è stato rimosso.

## File

| File | Descrizione |
|---|---|
| `llm_client.py` | Client Ollama — chiama l'API `/api/generate` |
| `yt_dlp_utils.py` | Utility per costruire comandi yt-dlp |
| `youtube_transcript.py` | Estrae transcript VTT da video YouTube |
| `script_generation.py` | Genera script documentario usando Ollama (da testo o YouTube) |

## Dipendenze

- `requests` — per le chiamate HTTP a Ollama
- `yt-dlp` — deve essere installato sul sistema per l'estrazione transcript

## Uso

Impostare `PYTHONPATH=src` prima di importare:

```bash
export PYTHONPATH=src
python3 -c "from python.script_generation import generate_script_from_text; ..."
```

Oppure in Python:
```python
import sys; sys.path.insert(0, 'src')
from python.script_generation import generate_script_from_text, generate_script_from_youtube

# Genera script da testo
script = generate_script_from_text("Testo sorgente...", "Titolo", "en", 120)

# Genera script da video YouTube
script = generate_script_from_youtube("https://youtube.com/watch?v=...", "Titolo", "en", 120)
```

## Nota

Esiste anche `scripts/generate_script.py` che usa l'API Go (porta 8080)
invece di chiamare Ollama direttamente. I due percorsi sono indipendenti.
