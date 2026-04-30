# Architettura Database - VeloxEditing

## Struttura Ufficiale Database

Tutti i database sono ora concentrati in un'unica posizione:

```
/home/pierone/Pyt/VeloxEditing/refactored/data/
├── velox.db.sqlite          # Clips / Google Drive (431 records)
├── stock.db.sqlite          # Stock footage (74 folders, 1969 clips)
├── artlist.db.sqlite        # Artlist videos (1681 video_links, 151 search_terms)
└── backups/                 # Backup automatici
```

## Database Eliminati

I seguenti database duplicati sono stati rimossi:
- `src/go-master/data/*.sqlite` (tutti)
- `src/go-master/data/*.shm` (WAL files)
- `src/go-master/data/*.wal` (WAL files)

## Configurazione Ambiente

Per far funzionare il server:

```bash
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master

VELOX_DATA_DIR=/home/pierone/Pyt/VeloxEditing/refactored/data \
go run ./cmd/server
```

## API Catalog

### Search Folders
```
GET /api/catalog/folders?q=<query>
```

Esempi:
```bash
# Cerca "50 Cent"
curl "http://localhost:8080/api/catalog/folders?q=50%20cent"

# Cerca "abstract"
curl "http://localhost:8080/api/catalog/folders?q=abstract"
```

## Sync Database

```bash
# Sync clips
VELOX_DATA_DIR=/home/pierone/Pyt/VeloxEditing/refactored/data \
go run ./cmd/sync_drive_content -type=clips <FOLDER_ID>

# Sync stock
go run ./cmd/sync_drive_content -type=stock <FOLDER_ID>

# Sync folders
go run ./cmd/sync_drive_content -type=folders <FOLDER_ID>
```

## Log Server

```bash
tail -f /tmp/velox-server.log
```

## Verifica Database

```bash
# Clips
sqlite3 data/velox.db.sqlite "SELECT COUNT(*) FROM clips;"

# Stock
sqlite3 data/stock.db.sqlite "SELECT COUNT(*) FROM stock_folders; SELECT COUNT(*) FROM stock_clips;"

# Artlist
sqlite3 data/artlist.db.sqlite "SELECT COUNT(*) FROM video_links; SELECT COUNT(*) FROM search_terms;"
```

## Risultati Attesi

```
431          # clips
74           # stock folders
1969         # stock clips
1681         # artlist video_links
151          # artlist search_terms
```