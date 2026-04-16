# Velox Editing - Struttura Progetto

Panoramica completa della struttura del progetto organizzata in moduli.

## 📁 Struttura Principale

```
refactored/
├── README_STRUCTURE.md           # Questo file
├── requirements.txt               # Dipendenze Python
│
├── docs/                          # 📚 Documentazione
│   ├── README.md                  # Indice documentazione
│   ├── scalability/               # Doc scalabilità
│   └── architecture/              # Doc architettura
│
├── modules/                       # 📦 Moduli Organizzati
│   ├── README.md                  # Indice moduli
│   ├── scalability/               # Scalabilità 1000+ worker
│   ├── video/                     # Video processing
│   ├── audio/                     # Audio processing
│   ├── web/                       # Web automation
│   ├── management/                # Machine & worker management
│   ├── cleanup/                   # Cleanup & maintenance
│   ├── installation/              # Installation & bootstrap
│   └── utils/                     # Utilities & config
│
├── routes/                        # 🔌 API Routes
│   └── README.md                  # Documentazione routes
│
├── tests/                         # 🧪 Test Files
│   └── README.md                  # Documentazione test
│
├── config/                        # ⚙️ Configuration Files
│   └── README.md                  # Documentazione config
│
├── job_master_server.py           # 🎛️ Master Server
├── job_worker.py                  # 👷 Worker
├── standalone_multi_video.py      # 📹 Multi-video interface
└── ... (altri file principali)
```

## 📚 Documentazione

### `docs/`
Documentazione completa organizzata:
- **`scalability/`**: Sistema scalabilità 1000+ worker
- **`architecture/`**: Architettura e design

Vedi [docs/README.md](./docs/README.md) per dettagli.

### Ogni Cartella Modulo
Ogni cartella in `modules/` ha un `README.md` che documenta:
- Scopo della cartella
- File contenuti e loro funzione
- Come utilizzare i moduli
- Esempi d'uso

## 📦 Moduli Principali

### `modules/scalability/`
Sistema per 1000+ worker:
- Database schema
- Lease system
- Idempotency
- Object storage
- Throttle & jitter

Vedi [modules/scalability/README.md](./modules/scalability/README.md)

### `modules/video/`
Video processing completo:
- Core processing
- Audio/video muxing
- Effetti e transizioni
- Generazione parallelizzata

Vedi [modules/video/README.md](./modules/video/README.md)

### `modules/audio/`
Audio processing:
- Analisi audio
- Voiceover processing

Vedi [modules/audio/README.md](./modules/audio/README.md)

### `modules/management/`
Gestione macchine e worker:
- Inventory
- Monitoring
- Provisioning
- Alerting

Vedi [modules/management/README.md](./modules/management/README.md)

### `modules/cleanup/`
Pulizia e manutenzione:
- Cleanup job
- Pulizia sistema
- Rimozione file temporanei

Vedi [modules/cleanup/README.md](./modules/cleanup/README.md)

### `modules/installation/`
Installazione automatica:
- Installer worker
- Bootstrap sistema
- Setup VPS

Vedi [modules/installation/README.md](./modules/installation/README.md)

### `modules/utils/`
Utility e configurazione:
- Config management
- Cache management
- Utility varie
- CLI tools

Vedi [modules/utils/README.md](./modules/utils/README.md)

## 🔌 Routes

Tutte le API routes in `routes/`:
- Dashboard
- Worker API
- Job management
- Statistics
- YouTube
- Logging

Vedi [routes/README.md](./routes/README.md)

## 🧪 Tests

File di test in `tests/`:
- Unit test
- Integration test
- Test specifici funzionalità

Vedi [tests/README.md](./tests/README.md)

## ⚙️ Config

File di configurazione JSON in `config/`:
- Master config
- Queue & jobs
- Worker data
- Inventory

Vedi [config/README.md](./config/README.md)

## 🚀 Utilizzo

### Import Moduli

```python
# Scalabilità
from modules.scalability import DatabaseManager, JobLeaseManager

# Video
from modules.video import video_generation, video_audio

# Management
from modules.management import machine_inventory

# Utils
from modules.utils import config, cache_manager
```

### Routes

Le routes vengono aggiunte in `job_master_server.py`:
```python
from routes import dashboard_routes, worker_routes
add_dashboard_routes(app, deps)
add_worker_routes(app, deps)
```

## 📝 Convenzioni

- **Un README per cartella**: Documenta tutti i file, non un MD per file
- **Moduli organizzati**: Per funzionalità, non per tipo file
- **Documentazione concentrata**: Info correlate insieme
- **Config separata**: File JSON in `config/`

## 🔍 Navigazione

1. **Struttura generale?** → `README_STRUCTURE.md` (questo file)
2. **Documentazione?** → `docs/README.md`
3. **Moduli?** → `modules/README.md`
4. **Scalabilità?** → `docs/scalability/README.md`
5. **Un modulo specifico?** → `modules/[nome]/README.md`

