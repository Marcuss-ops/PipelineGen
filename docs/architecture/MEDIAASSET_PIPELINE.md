# Media Asset Pipeline Architecture

**Status:** ACTIVE - Media pipeline documentation

## Overview

The media asset pipeline provides a common, reusable pipeline for downloading, processing, and uploading media assets across different services (Artlist, YouTube Clips, Stock, etc.).

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Service Layer                            │
│  ┌──────────────┐        ┌──────────────┐                │
│  │   Artlist    │        │ YouTube Clip │                │
│  │   Service    │        │   Service    │                │
│  └──────┬───────┘        └──────┬───────┘                │
│         │                        │                            │
│         └────────┬───────────────┘                            │
│                  │                                          │
│                  ▼                                          │
│      ┌───────────────────────────┐                          │
│      │  core/processor.Processor │                          │
│      │  Process()                │                          │
│      └──────────────┬────────────┘                          │
│                     │                                       │
│      ┌──────────────┴────────────┐                         │
│      │       Shared Components    │                         │
│      │  - downloader.YTDLP       │                         │
│      │  - ffmpeg.Processor       │                         │
│      │  - hashutil.MD5File       │                         │
│      │  - drive.Uploader         │                         │
│      └───────────────────────────┘                         │
└─────────────────────────────────────────────────────────────┘
```

## Core Packages

### `internal/media/mediaasset`

Common pipeline for processing media assets.

**Types** (`types.go`):
- `AssetInput`: Input parameters for processing
  - `ID`, `Name`, `SourceURL`, `OutputDir`, `FolderID`
- `AssetResult`: Result of processing
  - `LocalPath`, `DriveFileID`, `DriveLink`, `Size`, `Hash`, `Error`

**Adapter** (`adapter.go`):
- `ToCoreProcessor()` wraps `mediaasset.Processor` into `core/processor.Processor` interface

**Note**: Prior to unificato, la logica di processing viveva in `internal/service/mediaasset/`. Ora è in `internal/media/mediaasset/`.

## Service Integration

### Artlist Service (`internal/sources/artlist`)

The Artlist service handles:
1. Searching Artlist via node-scraper
2. Downloading media assets
3. Processing via `core/processor.Processor`
4. Uploading to Drive folders
5. Enqueuing jobs for async operations

### YouTube Clip Service (`internal/sources/youtube`)

The YouTube Clip service handles:
1. Downloading YouTube videos as clips
2. Processing via `core/processor.Processor`
3. Uploading to Drive clips folder
4. Enqueuing jobs for async operations

## Database

All media assets are stored in `data/media/media.db.sqlite` (unified database).
Job state is tracked in `data/velox/velox.db.sqlite`.
