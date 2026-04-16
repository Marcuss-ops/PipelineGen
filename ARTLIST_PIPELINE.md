# Artlist Clip Download Pipeline

Automated system for downloading Artlist video clips and uploading them to Google Drive, with scheduled cron jobs to continuously fill the database.

## Overview

This pipeline:
1. **Downloads** video clips from Artlist (via m3u8 URLs in SQLite DB)
2. **Converts** them to 1920x1080 MP4 (H.264 + AAC)
3. **Uploads** to Google Drive in organized folders (`Stock/Artlist/{Term}/`)
4. **Tracks** progress in local JSON files
5. **Schedules** automatic downloads via cron jobs

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      Artlist Pipeline                           │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  SQLite DB ──→ Python Downloader ──→ Google Drive              │
│  (935 clips)    (ffmpeg + upload)     (Stock/Artlist/)         │
│       │                                               │        │
│       ↓                                               ↓        │
│  59 search terms                          Tracking JSON        │
│                                         (artlist_uploaded.json)│
│                                                                 │
│  Cron Manager ─→ Scheduled Downloads ─→ Monitor                │
│  (4x daily)                              (real-time stats)     │
└─────────────────────────────────────────────────────────────────┘
```

## Files

### Scripts

| Script | Purpose |
|--------|---------|
| `scripts/artlist_bulk_downloader.py` | Main download script - downloads clips from DB to Drive |
| `scripts/artlist_cron_manager.py` | Cron job manager - schedules automatic downloads |
| `scripts/artlist_monitor.py` | Progress monitor - shows download statistics |
| `scripts/download_artlist_to_stock.py` | Original download script (legacy) |
| `populate_artlist_terms.py` | Expands search terms in SQLite DB |

### Data Files

| File | Purpose |
|------|---------|
| `src/node-scraper/artlist_videos.db` | SQLite DB with 935 clips across 59 search terms |
| `data/artlist_uploaded.json` | Tracking file - records of uploaded clips |
| `data/artlist_stock_index.json` | Index file - all uploaded clips with Drive URLs |
| `data/artlist_cron_state.json` | Cron job state and run history |

### Current State

- **Search terms in DB**: 59
- **Total clips indexed**: ~935
- **Clips uploaded to Drive**: 50 (5 terms × 10 clips)
- **Target**: Download all 935+ clips across all 59 terms

## Usage

### 1. Download Clips Manually

Download from specific terms:
```bash
python3 scripts/artlist_bulk_downloader.py --terms city,nature,people --clips-per-term 20
```

Download from all terms:
```bash
python3 scripts/artlist_bulk_downloader.py --all --clips-per-term 15
```

Dry run (see what would be downloaded):
```bash
python3 scripts/artlist_bulk_downloader.py --dry-run
```

### 2. Set Up Cron Jobs

View current schedule:
```bash
python3 scripts/artlist_cron_manager.py status
```

Start the cron scheduler (runs in foreground):
```bash
python3 scripts/artlist_cron_manager.py start
```

Run all jobs right now:
```bash
python3 scripts/artlist_cron_manager.py run-now
```

Add a custom scheduled job:
```bash
python3 scripts/artlist_cron_manager.py add-job \
  --name "Weekend Batch" \
  --time "10:00" \
  --terms "sunset,ocean,beach,landscape" \
  --clips 25
```

Remove a job:
```bash
python3 scripts/artlist_cron_manager.py remove-job --name "Weekend Batch"
```

### 3. Monitor Progress

Show current progress:
```bash
python3 scripts/artlist_monitor.py
```

Watch mode (auto-refresh every 10s):
```bash
python3 scripts/artlist_monitor.py --watch
```

Output as JSON:
```bash
python3 scripts/artlist_monitor.py --json
```

### 4. Run as Background Service

To run the cron manager in the background:
```bash
# Using nohup
nohup python3 scripts/artlist_cron_manager.py start > /tmp/artlist_cron.log 2>&1 &

# Or using screen/tmux
screen -S artlist-cron
python3 scripts/artlist_cron_manager.py start
# Ctrl+A, D to detach
```

To run as a systemd service:
```bash
# Create service file
sudo nano /etc/systemd/system/artlist-cron.service
```

```ini
[Unit]
Description=Artlist Clip Download Cron Manager
After=network-online.target

[Service]
Type=simple
User=pierone
WorkingDirectory=/home/pierone/Pyt/VeloxEditing/refactored
ExecStart=/usr/bin/python3 scripts/artlist_cron_manager.py start
Restart=always
RestartSec=60

[Install]
WantedBy=multi-user.target
```

```bash
# Enable and start
sudo systemctl enable artlist-cron
sudo systemctl start artlist-cron
sudo systemctl status artlist-cron
```

## Google Drive Structure

```
Stock/
└── Artlist/
    ├── City/          (10 clips)
    ├── Nature/        (10 clips)
    ├── People/        (10 clips)
    ├── Spider/        (10 clips)
    ├── Technology/    (10 clips)
    ├── Sunset/        (pending)
    ├── Ocean/         (pending)
    ├── Mountain/      (pending)
    ├── Forest/        (pending)
    └── ... (54 more terms)
```

Main Artlist folder: https://drive.google.com/drive/folders/1aMjQlK9J1mEyT2TOYDNjeynO1GzZS4_S

## Current Search Terms (59 total)

**Original 5 terms** (100, 50, 50, 50, 50 clips):
- spider, technology, people, nature, city

**Expanded terms** (15 clips each):
- woman, wildlife, web spinning, waterfall, walking, urban, typing keyboard, tree, traffic, team, talking, sunset, street, spider web, software, smartphone, skyline, sky, silhouette, screen, rooftop, river, portrait, person, ocean, night city, network, nature closeup, mountain, metropolis, laptop, landscape, internet, insect macro, human, highway, hands, group, garden, forest, flower, face, downtown, digital, data, cyber, crowd, computer, coding, cloud, cityscape, business man

## Default Cron Schedule

| Job | Time | Terms | Clips/Term |
|-----|------|-------|------------|
| Morning Batch | 06:00 | city, nature, people, technology, sunset, ocean | 20 |
| Midday Batch | 12:00 | mountain, forest, urban, business man, walking, face | 20 |
| Evening Batch | 18:00 | crowd, landscape, sky, river, flower, tree | 20 |
| Night Batch | 00:00 | spider, wildlife, waterfall, traffic, night city, rooftop | 15 |

**Total daily capacity**: ~285 clips/day

## Configuration

### Dependencies

```bash
pip install google-api-python-client google-auth apscheduler
```

### Google OAuth

Credentials are stored in:
- `src/go-master/token.json` - OAuth token (auto-refreshes)
- `src/go-master/credentials.json` - OAuth client credentials

### FFmpeg

Required for video download and conversion:
```bash
sudo apt install ffmpeg
```

## Troubleshooting

### Token Expired
The script automatically refreshes the token. If it fails, re-authenticate:
```bash
# Check token expiry
cat src/go-master/token.json | grep expiry
```

### Download Failures
- Check ffmpeg is installed: `ffmpeg -version`
- Check internet connection
- Some m3u8 URLs may be expired (Artlist session tokens)

### Drive Upload Failures
- Check token is valid
- Check Drive API is enabled in Google Cloud Console
- Check storage quota

### Cron Jobs Not Running
- Check logs: `cat /tmp/artlist_cron.log`
- Check Python dependencies: `pip list | grep apscheduler`
- Test manually: `python3 scripts/artlist_cron_manager.py run-now`

## Monitoring Dashboard

Real-time stats:
```bash
# Quick stats
python3 scripts/artlist_monitor.py

# Continuous monitoring
watch -n 30 'python3 scripts/artlist_monitor.py --json | jq .uploads.total_uploaded'
```

## Next Steps

1. **Expand search terms** - Add more diverse topics (sports, business, education, etc.)
2. **Increase clips per term** - Target 50-100 clips per term instead of 15-20
3. **Quality filtering** - Only download clips with good resolution (>720p)
4. **Tag enrichment** - Add descriptive tags to clips for better search
5. **Integration with Go backend** - Use ArtlistDB and API endpoints for clip association

## License

Internal project - VeloxEditing 2026
