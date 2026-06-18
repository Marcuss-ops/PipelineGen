# Media Asset Service

The `mediaasset` package provides the concrete pipeline for downloading, processing, normalizing, deduplicating, hashing, and uploading media assets. It hides external tools such as `yt-dlp`, `ffmpeg`, the Artlist scraper, and Google Drive behind focused ports.

## Canonical contract

`Processor` implements `internal/core/processor.Processor` directly:

```go
type Processor interface {
    Process(ctx context.Context, input *ProcessInput) (*ProcessResult, error)
}
```

`ProcessInput` and `ProcessResult` belong only to `internal/core/processor`. The media package must not recreate local mirrors or adapters for that contract.

## Processing stages

1. **Download** — `yt-dlp`, direct HTTP, the Artlist scraper, or FFmpeg HLS remuxing.
2. **Normalize** — FFmpeg normalization with zero-copy detection when the source already matches target specs.
3. **Deduplicate** — perceptual hash lookup against the asset registry.
4. **Hash** — file hash calculation for integrity and persistence.
5. **Upload** — optional Google Drive upload.
6. **Cleanup** — removal of temporary raw files.

## Ports

The package uses interfaces in `ports.go` to isolate infrastructure:

- `YTDLP`
- `HTTPDownloader`
- `VideoProcessor`

## Usage

```go
p := mediaasset.NewProcessor(dl, httpDL, ff, log, cfg, registry, driveUploader)
result, err := p.Process(ctx, &processor.ProcessInput{
    ID:        "asset-123",
    Name:      "Example clip",
    SourceURL: sourceURL,
})
```
