// Package youtube holds the application-layer orchestrator for the YouTube
// clip-extraction pipeline.
//
// Per PR3 (June 2026): the canonical port interfaces and DTOs have been
// extracted to `internal/application/youtube/ports/` (dedicated ports package).
// This file now contains thin type aliases that re-export the ports package
// definitions so existing callers compile without rename churn. Future PRs
// will migrate importers directly to youtube/ports and then delete this shim.
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
