# 🚀 Artlist Pipeline - Quick Start Guide

## Current Status

✅ **System is operational**
- Cron manager running (4 scheduled jobs daily)
- 54 clips uploaded to Google Drive
- 1,200 clips available in database
- Next automatic run: Today at 18:00

## Quick Commands

### 1. Check Progress
```bash
python3 scripts/artlist_monitor.py
```

### 2. Download Clips NOW (Manual)

**Option A: Quick test (2 terms, 2 clips each)**
```bash
python3 scripts/artlist_bulk_downloader.py --terms sunset,ocean --clips-per-term 2
```

**Option B: Bulk download (20 terms, 20 clips each)**
```bash
python3 scripts/artlist_bulk_downloader.py \
  --terms "spider,technology,people,nature,city,sunset,ocean,mountain,forest,urban,business man,walking,face,crowd,landscape,sky,river,flower,tree,wildlife" \
  --clips-per-term 20
```

**Option C: Download ALL terms (65 terms, 15 clips each)**
```bash
python3 scripts/artlist_bulk_downloader.py --all --clips-per-term 15
```

### 3. Run Scheduled Jobs NOW
```bash
python3 scripts/artlist_cron_manager.py run-now
```

This will run all 4 batches (Morning, Midday, Evening, Night) immediately.

### 4. Manage Cron Service
```bash
./artlist_cron_service.sh status    # Check if running
./artlist_cron_service.sh logs      # View logs
./artlist_cron_service.sh restart   # Restart if needed
./artlist_cron_service.sh stop      # Stop automatic downloads
```

## Google Drive

**Artlist folder:** https://drive.google.com/drive/folders/1aMjQlK9J1mEyT2TOYDNjeynO1GzZS4_S

Structure:
```
Stock/Artlist/
├── City/          (10 clips)
├── Nature/        (10 clips)
├── People/        (10 clips)
├── Spider/        (10 clips)
├── Technology/    (10 clips)
├── Sunset/        (2 clips)
├── Ocean/         (2 clips)
└── ... (58 more terms to be filled)
```

## What Happens Automatically

The cron manager will run 4 times daily:

| Time | Job | Terms | Clips |
|------|-----|-------|-------|
| 06:00 | Morning | city, nature, people, technology, sunset, ocean | 120 |
| 12:00 | Midday | mountain, forest, urban, business man, walking, face | 120 |
| 18:00 | Evening | crowd, landscape, sky, river, flower, tree | 120 |
| 00:00 | Night | spider, wildlife, waterfall, traffic, night city, rooftop | 90 |

**Total daily capacity: 450 clips**

## Estimated Time to Complete

- **Automatic (cron):** ~3 days to download all 1,200 clips
- **Manual bulk run:** 4-6 hours (one-time command)

## Need More Clips?

The database currently has 65 search terms with 1,200 clips total.

To add more search terms:
```bash
# Edit the populate script
nano populate_artlist_terms.py

# Add new terms to TERM_EXPANSIONS
# Then run:
python3 populate_artlist_terms.py
```

## Troubleshooting

### Cron not running
```bash
./artlist_cron_service.sh start
```

### Check logs
```bash
tail -f /tmp/artlist_cron.log
```

### Google token expired
The script auto-refreshes. If issues appear:
```bash
cat src/go-master/token.json | grep expiry
```

### Download failing
```bash
# Test with one clip
python3 scripts/artlist_bulk_downloader.py --terms sunset --clips-per-term 1
```

## Monitor in Real-Time

```bash
# Watch progress (auto-refreshes every 10s)
python3 scripts/artlist_monitor.py --watch
```

---

**That's it!** The system will automatically download clips to Google Drive 4 times per day.

For full documentation, see: `ARTLIST_PIPELINE.md`
