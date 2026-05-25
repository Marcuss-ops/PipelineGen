# PipelineGen - Access Guide

## Access Info

- **Public URL**: http://77.93.152.122:8080
- **Local URL**: http://127.0.0.1:8080
- **Admin Password**: (stored in `data/password.txt`)

## Server Configuration

### Bind Address
The server listens on all interfaces:
```bash
Environment=VELOX_HOST=0.0.0.0
```

### Ports
- **8080**: Main API backend
- **5173**: React frontend (dev only)

## Databases

### velox.db.sqlite (`data/velox/velox.db.sqlite`)
Main database:
- `scripts` - Video generation scripts
- `monitored_sources` - Monitored YouTube channels
- `harvester_jobs` - Content harvesting jobs
- `media_items` - Processed media items
- `media_files` - Files associated with media
- `media_tags` - Categorization tags
- `video_metadata` - Video metadata
- `script_stock_matches` - Script-to-stock matches
- `video_stats_history` - Statistics history
- `artlist_runs` - Artlist pipeline runs

### media.db.sqlite (`data/media/media.db.sqlite`)
Unified media database:
- `media_assets` - All assets (YouTube, Artlist, Stock, Voiceovers)

## Key API Endpoints

### Artlist
- `POST /api/artlist/run` - Start Artlist pipeline
- `GET /api/artlist/runs/:run_id` - Run status
- `GET /api/artlist/diagnostics` - System diagnostics
- `POST /api/artlist/search/live` - Live search

### YouTube Clips
- `POST /api/clips/process` - Download and process YouTube clips

### Jobs
- `GET /api/jobs` - List jobs
- `GET /api/jobs/:id` - Job details
- `POST /api/jobs` - Create new job

## Authentication

The system uses security tokens:
- `VELOX_ADMIN_TOKEN` - Admin token
- `VELOX_WORKER_TOKEN` - Worker token

If `VELOX_ENABLE_AUTH=true`, all APIs require authentication.

## Service Management

### Start/Stop/Restart
```bash
sudo systemctl start pipelinegen
sudo systemctl stop pipelinegen
sudo systemctl restart pipelinegen
```

### Service Status
```bash
systemctl status pipelinegen --no-pager -l
```

### Live Logs
```bash
journalctl -u pipelinegen -f
```

### Reload Configuration
```bash
sudo systemctl daemon-reload
sudo systemctl restart pipelinegen
```

## Firewall

If you can't access from outside:

### UFW (Ubuntu)
```bash
sudo ufw allow 8080/tcp
sudo ufw reload
sudo ufw status
```

### Verify open port
```bash
ss -tlnp | grep 8080
```
Expected: `0.0.0.0:8080`

## Typical Workflow

1. **Script input** → `scripts` table
2. **Stock search** → Artlist API or stock search
3. **Asset download** → Save to `data/downloads`
4. **Clip generation** → Video processing
5. **Drive upload** → Upload to Google Drive

## Quick Diagnostics

```bash
# Check service status
systemctl status pipelinegen --no-pager

# Test local connection
curl -I http://localhost:8080

# Test public connection (from VPS)
curl -I http://77.93.152.122:8080

# Check databases
sqlite3 data/velox/velox.db.sqlite ".tables"
sqlite3 data/media/media.db.sqlite ".tables"

# Error logs
journalctl -u pipelinegen --since "1 hour ago" | grep -i error
```

## Important Files

- **Config**: `internal/config/types.go`, `config.yaml`
- **systemd service**: `/etc/systemd/system/pipelinegen.service`
- **Databases**: `data/velox/velox.db.sqlite`, `data/media/media.db.sqlite`
- **Logs**: `journalctl -u pipelinegen`
- **Binary**: `pipelinegen` (project root)
