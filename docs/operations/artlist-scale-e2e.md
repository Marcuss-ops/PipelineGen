# Artlist scale E2E: 20 keywords × 10 clips

This is the quota-expensive completion, performance, indexing and replay battery
for the Artlist/VidRush path. It is deliberately excluded from `verify-live`.

## What it proves

The default run submits 20 real Artlist searches with 10 clips per keyword and
requires:

- PipelineGen readiness, scraper health and all 10 canonical Artlist diagnostic
  probes to remain healthy throughout the run;
- every job to terminate successfully and return the requested number of items;
- every unique clip to be `PUBLISHED` and `INDEXED` in SQLite with Drive ID,
  Drive link, SHA-256, source version and source/download URL;
- persisted M3U8 evidence in `source_url`, `download_link` or `metadata_json`;
- every Drive file to exist, be non-trashed and have a non-zero size;
- canonical VLM rows in `asset_visual_summaries` and matching Qdrant payloads;
- a one-clip replay canary followed by a full replay with zero new successful
  `artlist_download_audit` rows and unchanged Drive/hash identities.

Any earlier failure aborts replay work so a broken dedup path cannot consume a
second full batch of Artlist quota.

## Before running

Run on the dedicated operational host with:

- PipelineGen server, worker, scraper, Qdrant and embedding/VLM provider healthy;
- a valid Artlist browser session/cookie file;
- valid Google Drive credentials and `VELOX_DRIVE_ARTLIST_ROOT`;
- `VELOX_ADMIN_TOKEN` available through `scripts/with-velox-auth`;
- sufficient temporary disk for concurrent HLS segment spooling.

The scraper process reads `ARTLIST_HLS_SEGMENT_CONCURRENCY` at runtime. Restart
it after changing this value.

## Recommended baseline

```bash
export ARTLIST_HLS_SEGMENT_CONCURRENCY=8
export ARTLIST_SCALE_CLIP_CONCURRENCY=10
export ARTLIST_SCALE_POLL_WORKERS=20
export ARTLIST_SCALE_MIN_ASSETS_PER_MINUTE=0

make verify-artlist-scale-live
```

The HLS segment concurrency is bounded to 16. Start at 8; increase only when
network, Artlist responses, disk latency and memory remain stable. Clip
concurrency is bounded by the Artlist API contract to 10.

## Smaller qualification run

Use this before paying the full 200-clip quota:

```bash
export ARTLIST_SCALE_KEYWORDS="business team modern office,city skyline aerial drone"
export ARTLIST_SCALE_CLIPS_PER_KEYWORD=2
export ARTLIST_SCALE_RUN_VLM=1
export ARTLIST_SCALE_RUN_QDRANT_REINDEX=1
export ARTLIST_SCALE_RUN_REPLAY=1

make verify-artlist-scale-live
```

## Custom keyword file

One keyword per line; blank lines and lines beginning with `#` are ignored.

```bash
export ARTLIST_SCALE_KEYWORDS_FILE=/absolute/path/artlist-keywords.txt
make verify-artlist-scale-live
```

## Reports

The runner writes a timestamped directory under `/tmp/artlist_scale_*` unless
`ARTLIST_SCALE_REPORT_DIR` is set. The main files are:

- `summary.json`: final verdict, throughput, availability and dedup deltas;
- `availability/api_health.json`: health samples taken during the run;
- `first/`: submission, job and item evidence for the first pass;
- `sqlite/assets.json`: canonical persistence and M3U8 evidence;
- `drive/resolve.json`: Drive existence and size checks;
- `vlm/validation.json`: visual-summary coverage;
- `qdrant/validation.json`: indexed VLM payload coverage;
- `replay/validation.json`: canary/full replay download deltas and identity drift.

## Tuning sequence

1. Run the smaller qualification matrix.
2. Run 20 × 10 with HLS concurrency 5 and clip concurrency 5.
3. Raise clip concurrency to 10.
4. Raise HLS segment concurrency from 5 to 8, then 12 only if the report and
   host metrics improve.
5. Set `ARTLIST_SCALE_MIN_ASSETS_PER_MINUTE` to the accepted production floor
   once a stable baseline exists.

Do not tune only for the shortest elapsed time. A valid improvement must keep
all diagnostic samples healthy, preserve zero-redownload replay behavior and
produce complete Drive/VLM/Qdrant evidence.
