# Media Asset Pipeline Architecture

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
│      │  mediaasset.Processor     │                          │
│      │  DownloadProcessUpload()  │                          │
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

### `internal/service/mediaasset`

Common pipeline for processing media assets.

**Types** (`types.go`):
- `AssetInput`: Input parameters for processing
  - `ID`, `Name`, `SourceURL`, `OutputDir`, `FolderID`
  - `DownloadSections`: For YouTube clip sections
  - `ForceKeyframes`, `Normalize`, `KeepAudio`, `DisableDuration`
  - `Metadata`: Additional metadata map

- `AssetResult`: Processing result
  - `LocalPath`, `FileHash`, `DriveLink`, `DownloadLink`
  - `Status`, `Error`

**Processor** (`processor.go`):
- `DownloadProcessUpload(ctx, input) -> (result, error)`
  - Downloads from source URL
  - Handles YouTube sections via `DownloadSections`
  - Normalizes video with FFmpeg (configurable)
  - Calculates MD5 hash
  - Uploads to Google Drive
  - Returns result with paths and links

### `internal/service/assetstore`

Deduplication logic for existing assets.

**Types** (`types.go`):
- `ExistencePolicy`: Strategy for handling existing assets
  - `replace`: Always reprocess
  - `skip`: Skip if exists
  - `verify`: Verify integrity before skipping

- `ExistingAsset`: Asset to check
- `ChecksumChecker`: Interface for remote checksum verification

**ShouldSkipExisting** (`shouldskip.go`):
- `ShouldSkipExisting(ctx, asset, policy, checker) -> (skip, reason, error)`
  - Implements skip/replace/verify strategies
  - Checks Drive links, local files, file hashes
  - Verifies remote checksums via `ChecksumChecker`

## Service Integration

### Artlist Service (`internal/service/artlist`)

**Pipeline** (`pipeline.go`):
1. Search clips in DB (or live search if empty)
2. For each clip:
   - Check if should skip using `assetstore.ShouldSkipExisting`
   - Call `mediaasset.ProcessAsset` for download/process/upload
   - Update clip in DB with results
3. Return response with processed/skipped/failed counts

**Checksum Checker** (`pipeline.go`):
- `artlistChecksumChecker`: Implements `assetstore.ChecksumChecker`
- Gets MD5 checksum from Google Drive API

### YouTube Clip Service (`internal/service/youtubeclip`)

**Extract** (`service.go`):
1. Extract video ID from URL
2. Resolve Drive destination folder
3. For each segment:
   - Build section string (`*start-end`)
   - Call `mediaasset.ProcessAsset` with `DownloadSections`
   - Set `DisableDuration = true` (don't truncate)
   - Save clip to DB with metadata
4. Update manifest (TXT + JSON)

## Data Flow

### Artlist Flow
```
RunTag(term, limit)
  └─> SearchClips(term)
  └─> for each clip:
      └─> assetstore.ShouldSkipExisting()
      └─> mediaasset.ProcessAsset()
          ├─> downloader.Download()
          ├─> ffmpeg.Normalize()
          ├─> hashutil.MD5File()
          └─> drive.Uploader.UploadFile()
      └─> clipsRepo.UpsertClip()
```

### YouTube Clip Flow
```
Extract(url, segments[])
  └─> extractVideoID(url)
  └─> drivedestination.Resolve()
  └─> for each segment:
      └─> mediaasset.ProcessAsset()
          ├─> downloader.Download(section=*start-end)
          ├─> ffmpeg.Normalize(disableDuration=true)
          ├─> hashutil.MD5File()
          └─> drive.Uploader.UploadFile()
      └─> clipsRepo.UpsertClip()
  └─> folderMemory.SaveManifest()
```

## Configuration

### mediaasset.ProcessorConfig
- `DataDir`: Base data directory
- `TempDir`: Temporary files directory
- `VideoCfg`: FFmpeg normalization options

### AssetInput Options
- `Normalize`: Enable/disable video normalization
- `KeepAudio`: Preserve audio track
- `DisableDuration`: Skip duration truncation
- `DownloadSections`: YouTube download sections

## Extending

To add a new service (e.g., Stock):

1. Create service package in `internal/service/<name>/`
2. Initialize `mediaasset.Processor` in `bootstrap/wire.go`
3. Implement "source discovery" logic (search/fetch clips)
4. For each clip, call `mediaasset.ProcessAsset`
5. Use `assetstore.ShouldSkipExisting` for deduplication
6. Save results to DB

## Benefits

- **Code Reuse**: Common pipeline shared across services
- **Consistency**: Same processing logic for all media types
- **Maintainability**: Fixes/improvements apply everywhere
- **Testability**: Pipeline can be tested independently
- **Flexibility**: Each service customizes via AssetInput options
