package stockpipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// StockRunMetadata is the canonical per-run metadata.json envelope.
type StockRunMetadata struct {
	JobID                          string               `json:"job_id"`
	RunFingerprint                 string               `json:"run_fingerprint"`
	WorkflowID                     string               `json:"workflow_id"`
	Subfolder                      string               `json:"subfolder,omitempty"`
	DirectURLs                     []string             `json:"direct_urls,omitempty"`
	SearchQueries                  []string             `json:"search_queries,omitempty"`
	TotalMinutes                   int                  `json:"total_minutes"`
	TargetTotalDurationSeconds     int                  `json:"target_total_duration_seconds,omitempty"`
	TargetDurationPerSourceSeconds int                  `json:"target_duration_per_source_seconds,omitempty"`
	ClipsPerSource                 int                  `json:"clips_per_source,omitempty"`
	ClipDurationSeconds            int                  `json:"clip_duration_seconds,omitempty"`
	DownloadMode                   string               `json:"download_mode,omitempty"`
	ChunkDuration                  int                  `json:"chunk_duration"`
	ClipDuration                   int                  `json:"clip_duration"`
	Chunks                         []ChunkMetadataEntry `json:"chunks"`
	CreatedAt                      time.Time            `json:"created_at"`
	PolicyVersion                  string               `json:"policy_version"`

	Category string   `json:"category,omitempty"`
	Event    string   `json:"event,omitempty"`
	Round    int      `json:"round,omitempty"`
	Scene    string   `json:"scene,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	Entities []string `json:"entities,omitempty"`

	IndexingStatus           string `json:"indexing_status"`
	TimestampDriveFolderLink string `json:"timestamp_drive_folder_link,omitempty"`
	TimestampFolderID        string `json:"timestamp_folder_id,omitempty"`
}

const IndexingStatusPending = "INDEXING_PENDING"

// ChunkMetadataEntry is the per-chunk projection embedded in metadata.json.
type ChunkMetadataEntry struct {
	Index                    int      `json:"index"`
	ArtifactID               string   `json:"artifact_id"`
	DriveFileID              string   `json:"drive_file_id"`
	DriveWebViewLink         string   `json:"drive_web_view_link,omitempty"`
	SourceURL                string   `json:"source_url,omitempty"`
	SourceProvider           string   `json:"source_provider,omitempty"`
	SourceVideoID            string   `json:"source_video_id,omitempty"`
	TotalChunks              int      `json:"total_chunks,omitempty"`
	StartSec                 float64  `json:"start_sec,omitempty"`
	EndSec                   float64  `json:"end_sec,omitempty"`
	Title                    string   `json:"title,omitempty"`
	Description              string   `json:"description,omitempty"`
	Round                    int      `json:"round,omitempty"`
	Tags                     []string `json:"tags,omitempty"`
	Category                 string   `json:"category,omitempty"`
	Slug                     string   `json:"slug,omitempty"`
	SHA256                   string   `json:"sha256"`
	SizeBytes                int64    `json:"size_bytes"`
	LocalPath                string   `json:"local_path,omitempty"`
	TimestampDriveFolderLink string   `json:"timestamp_drive_folder_link,omitempty"`
	TimestampFolderID        string   `json:"timestamp_folder_id,omitempty"`
}

func buildStockRunMetadata(in *RunInput, chunks []ChunkState, runFingerprint string) StockRunMetadata {
	if in == nil {
		return StockRunMetadata{}
	}
	entries := make([]ChunkMetadataEntry, 0, len(chunks))
	for _, chunk := range chunks {
		entries = append(entries, ChunkMetadataEntry{
			Index:                    chunk.Index,
			ArtifactID:               chunk.ArtifactID,
			DriveFileID:              chunk.RemoteFileID,
			DriveWebViewLink:         chunk.RemoteWebViewLink,
			SourceURL:                chunk.SourceURL,
			SourceProvider:           chunk.SourceProvider,
			SourceVideoID:            chunk.SourceVideoID,
			TotalChunks:              chunk.TotalChunks,
			StartSec:                 chunk.StartSec,
			EndSec:                   chunk.EndSec,
			Title:                    chunk.Title,
			Description:              chunk.Description,
			Round:                    chunk.Round,
			Tags:                     append([]string(nil), chunk.Tags...),
			Category:                 chunk.Category,
			Slug:                     chunk.Slug,
			SHA256:                   chunk.SHA256,
			SizeBytes:                chunk.SizeBytes,
			LocalPath:                chunk.LocalPath,
			TimestampDriveFolderLink: chunk.TimestampDriveFolderLink,
			TimestampFolderID:        chunk.TimestampFolderID,
		})
	}
	return StockRunMetadata{
		JobID:                          in.FolderID,
		RunFingerprint:                 runFingerprint,
		WorkflowID:                     in.FolderID,
		Subfolder:                      in.Subfolder,
		DirectURLs:                     append([]string(nil), in.DirectURLs...),
		SearchQueries:                  append([]string(nil), in.SearchQueries...),
		TotalMinutes:                   in.TotalMinutes,
		TargetTotalDurationSeconds:     in.TargetTotalDurationSeconds,
		TargetDurationPerSourceSeconds: in.TargetDurationPerSourceSeconds,
		ClipsPerSource:                 in.ClipsPerSource,
		ClipDurationSeconds:            in.ClipDurationSeconds,
		DownloadMode:                   in.DownloadMode,
		ChunkDuration:                  in.ChunkDuration,
		ClipDuration:                   in.ClipDuration,
		Chunks:                         entries,
		CreatedAt:                      time.Now().UTC(),
		PolicyVersion:                  in.PolicyVersion,
		IndexingStatus:                 IndexingStatusPending,
		TimestampDriveFolderLink:       timestampFolderLink(chunks),
		TimestampFolderID:              timestampFolderID(chunks),
	}
}

func timestampFolderLink(chunks []ChunkState) string {
	if len(chunks) == 0 {
		return ""
	}
	return chunks[0].TimestampDriveFolderLink
}

func timestampFolderID(chunks []ChunkState) string {
	if len(chunks) == 0 {
		return ""
	}
	return chunks[0].TimestampFolderID
}

func writeAndHashPerClipMetadata(in *RunInput, chunk ChunkState, runFingerprint string, fs LocalFSPort) (string, string, int64, error) {
	if in == nil {
		return "", "", 0, errors.New("writeAndHashPerClipMetadata: nil RunInput")
	}
	if chunk.ArtifactID == "" {
		return "", "", 0, errors.New("writeAndHashPerClipMetadata: empty ChunkState.ArtifactID")
	}
	return writeAndHashStockMetadata(
		buildStockRunMetadata(in, []ChunkState{chunk}, runFingerprint),
		"pipelinegen-stock-clip-metadata-*.json",
		"writeAndHashPerClipMetadata",
		fs,
	)
}

func writeAndHashMetadata(in *RunInput, chunks []ChunkState, runFingerprint string, fs LocalFSPort) (string, string, int64, error) {
	if in == nil {
		return "", "", 0, errors.New("writeAndHashMetadata: nil RunInput")
	}
	return writeAndHashStockMetadata(
		buildStockRunMetadata(in, chunks, runFingerprint),
		"pipelinegen-stock-metadata-*.json",
		"writeAndHashMetadata",
		fs,
	)
}

func writeAndHashStockMetadata(meta StockRunMetadata, pattern, operation string, fs LocalFSPort) (string, string, int64, error) {
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", "", 0, fmt.Errorf("%s: marshal: %w", operation, err)
	}
	path, wc, err := fs.CreateTemp("", pattern)
	if err != nil {
		return "", "", 0, fmt.Errorf("%s: create temp: %w", operation, err)
	}
	cleanup := func() { _ = fs.Remove(path) }
	if _, err = wc.Write(raw); err != nil {
		closeErr := wc.Close()
		cleanup()
		if closeErr != nil {
			return "", "", 0, fmt.Errorf("%s: write %s: %w (close: %v)", operation, path, err, closeErr)
		}
		return "", "", 0, fmt.Errorf("%s: write %s: %w", operation, path, err)
	}
	if err = wc.Close(); err != nil {
		cleanup()
		return "", "", 0, fmt.Errorf("%s: close %s: %w", operation, path, err)
	}
	hash, err := job.ComputeSHA256(path)
	if err != nil {
		cleanup()
		return "", "", 0, fmt.Errorf("%s: hash %s: %w", operation, path, err)
	}
	return path, hash, int64(len(raw)), nil
}

func buildChunkedStockManifest(workflowID, jobID, fingerprint string, chunks []ChunkState, metadata MetadataState) *job.ArtifactManifest {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    workflowID,
		JobID:         jobID,
		Artifacts:     make([]job.Artifact, 0, 1+len(chunks)),
	}
	if metadata.LocalPath != "" {
		manifest.Artifacts = append(manifest.Artifacts, job.Artifact{
			ID:                MetadataArtifactID(fingerprint),
			Kind:              job.ArtifactKindMetadata,
			Filename:          "metadata.json",
			MIMEType:          "application/json",
			Path:              metadata.LocalPath,
			SHA256:            metadata.SHA256,
			SizeBytes:         metadata.SizeBytes,
			Required:          true,
			RemoteFileID:      metadata.RemoteFileID,
			RemoteWebViewLink: metadata.RemoteWebViewLink,
			ArtifactMetadata:  map[string]any{"total_clips": len(chunks), "total_chunks": len(chunks)},
		})
	}
	for _, chunk := range chunks {
		manifest.Artifacts = append(manifest.Artifacts, job.Artifact{
			ID:                 chunk.ArtifactID,
			Kind:               string(finalization.KindVideo),
			Filename:           chunk.Filename,
			MIMEType:           "video/mp4",
			Path:               chunk.LocalPath,
			SHA256:             chunk.SHA256,
			SizeBytes:          chunk.SizeBytes,
			Required:           true,
			RemoteFileID:       chunk.RemoteFileID,
			RemoteWebViewLink:  chunk.RemoteWebViewLink,
			RemoteDownloadLink: chunk.RemoteDownloadLink,
			ArtifactMetadata: map[string]any{
				"chunk_index":                 chunk.Index,
				"clip_count":                  1,
				"total_chunks":                len(chunks),
				"title":                       chunk.Title,
				"description":                 chunk.Description,
				"source_url":                  chunk.SourceURL,
				"source_video_id":             chunk.SourceVideoID,
				"start_sec":                   chunk.StartSec,
				"end_sec":                     chunk.EndSec,
				"drive_path":                  chunk.DrivePath,
				"timestamp_drive_folder_link": chunk.TimestampDriveFolderLink,
				"timestamp_folder_id":         chunk.TimestampFolderID,
				"policy_version":              chunk.PolicyVersion,
			},
		})
	}
	return manifest
}
