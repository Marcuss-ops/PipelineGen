# Media Asset Pipeline Architecture

**Status:** ACTIVE - Media pipeline documentation

## Overview

The media asset pipeline provides a common, reusable pipeline for downloading, processing, and uploading media assets across different services such as Artlist, YouTube Clips, and stock providers.

## Architecture

```text
┌─────────────────────────────────────────────────────────────┐
│                    Service Layer                            │
│  ┌──────────────┐        ┌──────────────┐                  │
│  │   Artlist    │        │ YouTube Clip │                  │
│  │   Service    │        │   Service    │                  │
│  └──────┬───────┘        └──────┬───────┘                  │
│         │                        │                          │
│         └────────┬───────────────┘                          │
│                  │                                          │
│                  ▼                                          │
│      ┌───────────────────────────┐                          │
│      │  core/processor.Processor │                          │
│      │  Process()                │                          │
│      └──────────────┬────────────┘                          │
│                     │                                       │
│      ┌──────────────▼────────────┐                         │
│      │ mediaasset.Processor       │                         │
│      │ direct implementation      │                         │
│      └──────────────┬────────────┘                         │
│                     │                                       │
│      ┌──────────────▼────────────┐                         │
│      │       Shared Components    │                         │
│      │  - downloader.YTDLP       │                         │
│      │  - ffmpeg.Processor       │                         │
│      │  - hashutil.MD5File       │                         │
│      │  - drive.Uploader         │                         │
│      └───────────────────────────┘                         │
└─────────────────────────────────────────────────────────────┘
```

## Canonical contract

`internal/core/processor` owns the only processing contract and its DTOs:

- `processor.Processor`
- `processor.ProcessInput`
- `processor.ProcessResult`

`internal/media/mediaasset.Processor` implements that interface directly. Package-local mirrors such as `AssetInput` and `AssetResult`, plus conversion adapters, are forbidden because they create contract drift and silently drop fields.

## Service integration

### Artlist Service (`internal/sources/artlist`)

The Artlist service handles:

1. Searching Artlist through the Node scraper.
2. Submitting media processing through `core/processor.Processor`.
3. Persisting authoritative metadata in the canonical database.
4. Enqueuing asynchronous operations through the job system.

### YouTube Clip Service (`internal/sources/youtube`)

The YouTube Clip service handles:

1. Resolving and downloading YouTube clips.
2. Submitting processing through `core/processor.Processor`.
3. Persisting authoritative metadata in the canonical database.
4. Enqueuing asynchronous operations through the job system.

## Database

All authoritative media metadata and job state are stored in `data/media/media.db.sqlite`. Generated `metadata.json` files and external indexes are exports or projections, never sources of truth.
