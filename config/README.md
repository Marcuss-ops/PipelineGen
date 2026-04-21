# Config - Configuration Files

Configurazione del sistema, versioni e file di setup.

## 📁 File Contenuti

### `config.py`
Configurazione principale del sistema:
- Costanti di configurazione (FPS, dimensioni video, font, etc.)
- Path di default
- Configurazione Chrome/Profilo
- Configurazione GPU

### `config_clean.py`
Versione pulita/backup del file di configurazione:
- Backup della configurazione base
- Template di configurazione

### `version/`
File di versione:
- `VERSION.txt` - Versione corrente del sistema
- `worker_patch_version.txt` - Versione patch worker

### `backups/`
Backup automatici della queue job (vedi `backups/README.md`)

## 🔧 Utilizzo

```python
from config import config  # Import configurazione
from config.config import BASE_FPS_DEFAULT  # Import costanti
```

## 📝 Note

I file di configurazione principali sono qui per centralizzare tutte le impostazioni del sistema.
