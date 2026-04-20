# Scripts - Utility Scripts

Script shell e Python per operazioni di utilità.

## 📁 File Contenuti

### `cleanup_old_services.sh`
Script shell per pulizia servizi vecchi:
- Rimozione servizi systemd obsoleti
- Cleanup servizi inattivi

### `monitor_worker_update.sh`
Script per monitorare aggiornamenti worker:
- Monitoring update process
- Verifica stato aggiornamenti

### `test_floyd_tyson_chapters.py`
Smoke test per il mapping capitoli/timestamp:
- testo controllato Floyd -> Mike Tyson
- divide + extract entities
- timestamp map con link Drive/Artlist per capitolo

### `generate_floyd_tyson_docs.sh`
Genera il documento ScriptDocs con default Floyd -> Mike Tyson:
- chiama `POST /api/script-docs/generate`
- salva la risposta JSON in `/tmp/floyd_tyson_scriptdocs_response.json`
- stampa `doc_id` e `doc_url`

## 🔧 Utilizzo

```bash
# Cleanup servizi
./scripts/cleanup_old_services.sh

# Monitor worker update
./scripts/monitor_worker_update.sh

# Test capitoli Floyd -> Tyson
python3 scripts/test_floyd_tyson_chapters.py

# Genera documento Floyd -> Tyson
./scripts/generate_floyd_tyson_docs.sh
```

## 📝 Note

Script di utilità per operazioni manuali o automatizzate sul sistema.
