# Media Asset Service

The `mediaasset` package provides a robust pipeline for downloading, processing, and normalizing media assets from various sources. It handles the complexity of interacting with external tools like `yt-dlp` and `ffmpeg`, while providing a clean Go interface.

## Core Components

### MediaProcessor
The `MediaProcessor` interface defines the primary contract for asset processing:
```go
type MediaProcessor interface {
    DownloadProcessUpload(ctx context.Context, input AssetInput) (*AssetResult, error)
}
```

### Processor
The `Processor` struct implements the `MediaProcessor` interface and orchestrates the following steps:
1.  **Download**: Uses `yt-dlp` for general video sites, `HTTPDownloader` for direct links, or `ffmpeg` for HLS streams.
2.  **Normalize**: Processes the raw download using `ffmpeg` to match target specifications (resolution, FPS, codec). Includes **Zero-Copy Optimization** to skip processing if the source already matches the target.
3.  **Deduplicate**: Uses Perceptual Hashing (PHash) to identify visual duplicates by extracting frames and comparing them via an external embedding server.
4.  **Hash**: Calculates MD5 file hashes for integrity and database tracking.
5.  **Cleanup**: Removes temporary raw files after processing.

### Ports
The package uses interfaces (`ports.go`) to decouple the processor from specific implementations:
- `YTDLP`: Interface for `yt-dlp` wrappers.
- `HTTPDownloader`: Interface for direct HTTP downloads.
- `VideoProcessor`: Interface for `ffmpeg` operations (Normalize, Probe, ExtractFrame).

## Features

- **Zero-Copy Optimization**: Automatically detects if a downloaded video already meets the target specifications (width, height, FPS) and skips the expensive normalization step if `StreamCopy` is enabled.
- **Perceptual Deduplication**: Leverages an external Python-based embedding server to detect visually similar clips even if the binary files are different.
- **Multi-Source Support**: Handles direct downloads, HLS streams (.m3u8), and complex video platform URLs.
- **Robustness**: Includes retry logic (via callers), comprehensive logging, and temporary file management.

## Usage

```go
p := mediaasset.NewProcessor(dl, httpDL, ff, log, cfg, registry)
result, err := p.DownloadProcessUpload(ctx, input)
```
