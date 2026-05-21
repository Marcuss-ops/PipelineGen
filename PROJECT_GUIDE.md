# Guida di Avvio Rapido - PipelineGen

Benvenuto in **PipelineGen**, una potente piattaforma backend basata su Go per l'automazione del media processing.

## 🛠 Requisiti di Sistema

Prima di iniziare, assicurati di avere installato:
- **Go**: Versione 1.25 o superiore (consigliato 1.25.9+)
- **Python**: Versione 3.10+ (per script di embedding e indexing)
- **yt-dlp**: Installato e disponibile nel PATH di sistema
- **FFmpeg**: Per il taglio e la codifica audio/video

## 🚀 Installazione e Avvio

1. **Configura l'ambiente**:
   Crea una copia del file di configurazione e inserisci le tue chiavi e credenziali (es. Google Drive API):
   ```bash
   cp config.example.yaml config.yaml
   ```

2. **Compila il server**:
   ```bash
   go build -o pipelinegen ./cmd/server/
   ```

3. **Avvia PipelineGen**:
   Esegui il backend in modalità completa (Server HTTP + Workers asincroni):
   ```bash
   ./pipelinegen --mode all
   ```

## 📂 Struttura del Database

PipelineGen utilizza SQLite centralizzato in WAL mode con due database:
- `data/velox/velox.db.sqlite`: Database principale per Script, Job e Asset Index.
- `data/media/media.db.sqlite`: Database unificato per i file multimediali (YouTube, Artlist, Stock, Immagini, Voiceovers).
