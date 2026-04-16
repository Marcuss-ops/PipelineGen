# Modules - Moduli del Sistema

Moduli organizzati per funzionalità.

## 📁 Struttura

```
modules/
├── README.md                  # Questo file
├── scalability/               # Scalabilità 1000+ worker
├── video/                     # Video processing
├── audio/                     # Audio processing
├── web/                       # Web automation
├── management/                # Machine & worker management
├── cleanup/                   # Cleanup & maintenance
├── installation/              # Installation & bootstrap
├── utils/                     # Utilities & config
└── storage/                   # Storage & persistence (se esiste)
```

## 📚 Moduli

### `scalability/`
Sistema per 1000+ worker simultanei:
- Database schema
- Lease system
- Idempotency
- Object storage
- Throttle & jitter

Vedi [scalability/README.md](./scalability/README.md)

### `video/`
Video processing completo:
- Core processing
- Audio/video muxing
- Effetti e transizioni
- Generazione parallelizzata
- FFmpeg utilities

Vedi [video/README.md](./video/README.md)

### `audio/`
Audio processing:
- Analisi audio
- Voiceover processing

Vedi [audio/README.md](./audio/README.md)

### `web/`
Web automation:
- Selenium automation
- Browser automation
- Web scraping

Vedi [web/README.md](./web/README.md)

### `management/`
Gestione macchine e worker:
- Inventory
- Monitoring
- Provisioning
- Alerting
- Health checks

Vedi [management/README.md](./management/README.md)

### `cleanup/`
Pulizia e manutenzione:
- Cleanup job
- Pulizia sistema
- Rimozione file temporanei

Vedi [cleanup/README.md](./cleanup/README.md)

### `installation/`
Installazione automatica:
- Installer worker
- Bootstrap sistema
- Setup VPS

Vedi [installation/README.md](./installation/README.md)

### `utils/`
Utility e configurazione:
- Config management
- Cache management
- Utility varie
- CLI tools
- Logging

Vedi [utils/README.md](./utils/README.md)

## 🔧 Utilizzo

```python
# Import da moduli
from modules.scalability import DatabaseManager
from modules.video import video_generation
from modules.management import machine_inventory
```

## 📝 Convenzioni

- Ogni cartella ha un `README.md` che documenta tutti i file
- Moduli organizzati per funzionalità, non per tipo
- Export pubblici in `__init__.py`
