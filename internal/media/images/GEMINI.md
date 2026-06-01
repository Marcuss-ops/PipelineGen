# Image Generation Service - GEMINI.md

## Overview
The `images` package manages the AI image generation lifecycle, featuring a tiered approach and an ingestion pipeline that handles both local storage and Google Drive uploads.

## Generation Strategy: Smart Generation
The primary entry point is `GenerateSmartImage`, which implements a fallback logic:

1.  **Google Labs Flow (Primary)**:
    - Calls the `google-accounting` Python server.
    - Supports generating 4 images in parallel (asynchronously tracked via Job ID).
    - Robust capture via the Python automation's dual Network/DOM strategy.
    - **Must-have**: Ensure the `google-accounting` service is running on the configured URL.

2.  **NVIDIA Fallback (Secondary)**:
    - If Google Flow fails or returns zero images, the system automatically falls back to NVIDIA NIMs (e.g., `flux-1-dev`).
    - Model selection can be customized via the `model` parameter.

## Ingestion Pipeline
Generated images are processed through `IngestImage`:
- **Hashing**: SHA-256 hash is computed to prevent duplicates.
- **Local Storage**: Saved in structured directories: `data/images/{style}/{subject}/`.
- **Drive Upload**: Uploaded to Google Drive if not skipped.
- **Metadata**: A unified `metadata.json` is generated for batch generations to keep Drive folders organized.
- **Single-Call Tagger Pattern**: `tagImageMetadata()` calls `metaWriter.Write()` once; result reused for Drive + DB (eliminates duplicate Python subprocess call).
- **Unified MetadataWriter**: All media types use `semantic.MetadataWriter` + `Payload` struct via `config/semantic_taxonomy.yaml`.

## Integration with Google Accounting
The Go service communicates with the Python server via:
- `POST /generate-flow-images`: Starts the job.
- `GET /status/{job_id}`: Polls for completion.
- Models are defined in `internal/pkg/googleaccounting/models.go`.

## Artlist Speed Optimization (June 2026)
Three-level optimization:
- **Level 1**: Persistent SQLite cache (`artlist_search_cache` table) — cached results in ~14ms
- **Level 2**: Parallel downloads with configurable concurrency (default 3, max 10)
- **Level 3**: Persistent Node.js scraper server (systemd `artlist-scraper`, port 9123) — Chromium always warm

## Best Practices
- **Style Registry**: Use `config/generation_styles.yaml` to define standard prompt modifiers.
- **Context Propagation**: Always pass the request `context.Context` to ensure timeouts are respected.
- **Health Checks**: The service performs a quick `/health` check before starting long-running Google jobs.
- **Single Tagger Call**: Use `tagImageMetadata()` instead of calling `metaWriter.Write()` and `semantic.Tagger()` separately.
