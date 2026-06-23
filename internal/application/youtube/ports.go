// PORT-TO-PACKAGE SHIM — internal-only (Phase 1 of PR4, June 2026).
//
// This file is now ZERO-EXTERNAL-IMPORTERS. All external callers (composers,
// infra adapters, HTTP handlers, provider/stock-pipeline, channel monitor)
// have been migrated to import `youtube/ports/` directly in Commit X (PR4
// phase 1). See commits 4a5d3e6b (PR1) → 48dd0cb1 (PR3 hotfix) → <this commit>
// for the migration trail.
//
// The shim is RETAINED because ~15 INTERNAL files inside the
// `internal/application/youtube/` parent package still reference the alias
// symbols via package-scope (no explicit import needed). Sweeping them is
// PR4-B scope — a separate PR will collapse the shim once the internal
// migration completes.
//
// Per PR3 (June 2026): the canonical port interfaces and DTOs live in
// `internal/application/youtube/ports/ports.go`.
//
// Per PR4 (June 2026): External API surface is now CANONICAL.
//   - External: 0 importers
//   - Internal: ~15 files still use alias symbols (PR4-B sweep queued)
//
// Rule: no NEW type definitions or interfaces should be added here —
// they belong in youtube/ports/ports.go.
package youtube

import (
	metadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	ports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// DTOs — canonical definitions in youtube/ports/ports.go
type DownloaderMetadata = ports.DownloaderMetadata
type VideoChapter = ports.VideoChapter
type VideoThumbnail = ports.VideoThumbnail
type UploadResultDTO = ports.UploadResultDTO
type SearchLiveResult = ports.SearchLiveResult
type VideoMetadata = ports.VideoMetadata
type YouTubeMetadataPort = ports.YouTubeMetadataPort

// Structural ports — canonical definitions in youtube/ports/ports.go
type ClipStorePort = ports.ClipStorePort
type MonitorsStorePort = ports.MonitorsStorePort
type VideoMetadataFetcherPort = ports.VideoMetadataFetcherPort
type DriveFolderManagerPort = ports.DriveFolderManagerPort
type FolderMemoryPort = ports.FolderMemoryPort
type OllamaClientPort = ports.OllamaClientPort
type SearchRunnerPort = ports.SearchRunnerPort
type ClipIndexerPort = ports.ClipIndexerPort

// Empty-marker ports
type SubtitleFetcherPort = ports.SubtitleFetcherPort
type WhisperTranscriberPort = ports.WhisperTranscriberPort
type ClipFilesPort = ports.ClipFilesPort
type HashServicePort = ports.HashServicePort
type TempFileManagerPort = ports.TempFileManagerPort
type YouTubeCacheStorePort = ports.YouTubeCacheStorePort

// ClipMetadataFile is a zero-copy alias to the canonical type in youtube/metadata/.
// Extracted during PR4 Phase 1 (June 2026).
type ClipMetadataFile = metadata.ClipMetadataFile
