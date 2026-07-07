// Package stockpipeline — orchestrator_metadata.go (Stock P1 split, July 2026).
//
// This file owns the per-run metadata.json types and helpers extracted
// from orchestrator_steps.go: StockRunMetadata, ChunkMetadataEntry,
// buildStockRunMetadata, writeAndHashMetadata, buildChunkedStockManifest.
//
// godlike/06 SSOT: one canonical owner for the metadata.json wire shape
// and its construction logic.
package stockpipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// StockRunMetadata is the typed wire envelope placed in the
// per-run metadata.json. Contains chunk-level entries so the
// downstream run consumer can reconstruct the chunk topology
// without re-walking the orchestrator's state.
//
// LocalPath is embedded for audit; the user-facing API response
// (HandleJob result map) does NOT carry LocalPath — see godlike/07
// no-fake-availability: ApiResponseFields{AssetID/RemoteAssetID/
// SHA256/SizeBytes/DurationMS/IndexState} only.
//
// LLM enrichment fields (Category, Event, Round, Scene, Subject,
// Entities) are added in PR-007 as plumbing-on-nil slots. The
// fields stay at zero-value until the downstream LLM enrichment
// pass lands (forward-pointer per user spec). Per godlike/07
// NO-FAKE-AVAILABILITY: NEVER emit a placeholder string like
// Category:"unknown" or Event:"n/a" — omitempty drops empty
// values from the JSON wire so consumers see the field as
// absent (not as a meaningless literal). The LLM enrichment
// pass is the sole authority on when to populate these fields.
type StockRunMetadata struct {
	JobID          string               `json:"job_id"`
	RunFingerprint string               `json:"run_fingerprint"`
	WorkflowID     string               `json:"workflow_id"`
	Subfolder      string               `json:"subfolder,omitempty"`
	DirectURLs     []string             `json:"direct_urls,omitempty"`
	SearchQueries  []string             `json:"search_queries,omitempty"`
	TotalMinutes   int                  `json:"total_minutes"`
	ChunkDuration  int                  `json:"chunk_duration"`
	ClipDuration   int                  `json:"clip_duration"`
	Chunks         []ChunkMetadataEntry `json:"chunks"`
	CreatedAt      time.Time            `json:"created_at"`
	PolicyVersion  string               `json:"policy_version"`

	// ── LLM enrichment (PR-007, plumbing-on-nil) ───────────────────
	// Forward-pointer: these fields are wired into the wire shape
	// NOW so future PRs can populate them after the LLM enrichment
	// pass lands. For now, all 6 stay at zero-value; the omitempty
	// JSON tags ensure they are dropped from the metadata.json
	// payload until the LLM pass hydrates them. godlike/07
	// NO-FAKE-AVAILABILITY: do NOT use placeholder strings here.
	Category string   `json:"category,omitempty"`
	Event    string   `json:"event,omitempty"`
	Round    int      `json:"round,omitempty"`
	Scene    string   `json:"scene,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	Entities []string `json:"entities,omitempty"`
}

// ChunkMetadataEntry is the per-chunk metadata entry embedded
// in the per-run metadata.json. LocalPath is exposed here for
// audit; the public API response strips it.
//
// PR-003 (July 2026) added 3 observability fields: TotalChunks
// (per-run count, repeated per-entry per user spec — logically a
// run-level scalar but propagates per-entry for consumer
// convenience), SourceProvider (canonical 4-bucket enum: youtube
// / pexels / pixabay / unknown), SourceVideoID (provider-native
// ID, e.g. YouTube video ID when SourceProvider == youtube).
type ChunkMetadataEntry struct {
	Index            int     `json:"index"`
	ArtifactID       string  `json:"artifact_id"`
	DriveFileID      string  `json:"drive_file_id"`
	DriveWebViewLink string  `json:"drive_web_view_link,omitempty"`
	SourceURL        string  `json:"source_url,omitempty"`
	SourceProvider   string  `json:"source_provider,omitempty"`
	SourceVideoID    string  `json:"source_video_id,omitempty"`
	TotalChunks      int     `json:"total_chunks,omitempty"`
	StartSec         float64 `json:"start_sec,omitempty"`
	EndSec           float64 `json:"end_sec,omitempty"`
	Title            string  `json:"title,omitempty"`
	SHA256           string  `json:"sha256"`
	SizeBytes        int64   `json:"size_bytes"`
	LocalPath        string  `json:"local_path,omitempty"`
}

// buildStockRunMetadata constructs the typed StockRunMetadata
// from the per-call RunInput + chunks. Pure function so TDD
// coverage can pin the entry shape (no I/O, no SHA, no
// Publisher).
func buildStockRunMetadata(in *RunInput, chunks []ChunkState, runFingerprint string) StockRunMetadata {
	if in == nil {
		return StockRunMetadata{}
	}
	entries := make([]ChunkMetadataEntry, 0, len(chunks))
	for _, c := range chunks {
		entry := ChunkMetadataEntry{
			Index:            c.Index,
			ArtifactID:       c.ArtifactID,
			DriveFileID:      c.RemoteFileID,
			DriveWebViewLink: c.RemoteWebViewLink,
			SourceURL:        c.SourceURL,
			SourceProvider:   c.SourceProvider,
			SourceVideoID:    c.SourceVideoID,
			TotalChunks:      c.TotalChunks,
			StartSec:         c.StartSec,
			EndSec:           c.EndSec,
			Title:            c.Title,
			SHA256:           c.SHA256,
			SizeBytes:        c.SizeBytes,
			LocalPath:        c.LocalPath,
		}
		entries = append(entries, entry)
	}
	return StockRunMetadata{
		JobID:          in.FolderID,
		RunFingerprint: runFingerprint,
		WorkflowID:     in.FolderID,
		Subfolder:      in.Subfolder,
		DirectURLs:     append([]string(nil), in.DirectURLs...),
		SearchQueries:  append([]string(nil), in.SearchQueries...),
		TotalMinutes:   in.TotalMinutes,
		ChunkDuration:  in.ChunkDuration,
		ClipDuration:   in.ClipDuration,
		Chunks:         entries,
		CreatedAt:      time.Now().UTC(),
		PolicyVersion:  in.PolicyVersion, // populated from RunInput; Cfg() also has it
	}
}

// writeAndHashMetadata stages the per-run metadata.json content
// to a temp file, computes its SHA256, and returns
// (LocalPath, SHA256, SizeBytes, error). Best-effort cleanup on
// failure paths so the temp dir doesn't accumulate garbage.
func writeAndHashMetadata(in *RunInput, chunks []ChunkState, runFingerprint string) (string, string, int64, error) {
	if in == nil {
		return "", "", 0, errors.New("writeAndHashMetadata: nil RunInput")
	}
	meta := buildStockRunMetadata(in, chunks, runFingerprint)
	raw, mErr := json.MarshalIndent(meta, "", "  ")
	if mErr != nil {
		return "", "", 0, fmt.Errorf("writeAndHashMetadata: marshal: %w", mErr)
	}
	sizeBytes := int64(len(raw))

	f, cErr := os.CreateTemp("", "pipelinegen-stock-metadata-*.json")
	if cErr != nil {
		return "", "", 0, fmt.Errorf("writeAndHashMetadata: create temp: %w", cErr)
	}
	cleanup := func() { _ = os.Remove(f.Name()) }

	if _, wErr := f.Write(raw); wErr != nil {
		closeErr := f.Close() // P3 fix: surface close error alongside write error (godlike/07 no silent error)
		cleanup()
		if closeErr != nil {
			return "", "", 0, fmt.Errorf("writeAndHashMetadata: write %s: %w (close: %v)", f.Name(), wErr, closeErr)
		}
		return "", "", 0, fmt.Errorf("writeAndHashMetadata: write %s: %w", f.Name(), wErr)
	}
	if cErr := f.Close(); cErr != nil {
		cleanup()
		return "", "", 0, fmt.Errorf("writeAndHashMetadata: close %s: %w", f.Name(), cErr)
	}
	h, hErr := job.ComputeSHA256(f.Name())
	if hErr != nil {
		cleanup()
		return "", "", 0, fmt.Errorf("writeAndHashMetadata: hash %s: %w", f.Name(), hErr)
	}
	return f.Name(), h, sizeBytes, nil
}

// buildChunkedStockManifest constructs the canonical wire manifest
// for the §12-7 chunked pipeline:
//
//   - 1 metadata Artifact entry (Required:true) keyed by
//     MetadataArtifactID(fp)
//   - N chunk Artifact entries (Required:true) keyed by
//     ChunkArtifactID(fp, index)
//
// Pure function so TDD coverage can pin the manifest shape
// independently of orchestrator state.
func buildChunkedStockManifest(workflowID, jobID, fp string, chunks []ChunkState, metadata MetadataState) *job.ArtifactManifest {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    workflowID,
		JobID:         jobID,
		Artifacts:     make([]job.Artifact, 0, 1+len(chunks)),
	}
	if metadata.LocalPath != "" {
		manifest.Artifacts = append(manifest.Artifacts, job.Artifact{
			ID:        MetadataArtifactID(fp),
			Kind:      job.ArtifactKindMetadata,
			Filename:  "metadata.json",
			MIMEType:  "application/json",
			Path:      metadata.LocalPath,
			SHA256:    metadata.SHA256,
			SizeBytes: metadata.SizeBytes,
			Required:  true,
		})
	}
	if len(chunks) > 0 {
		for _, c := range chunks {
			manifest.Artifacts = append(manifest.Artifacts, job.Artifact{
				ID:        c.ArtifactID,
				Kind:      string(finalization.KindVideo), // canonical video variant
				Filename:  c.Filename,
				MIMEType:  "video/mp4",
				Path:      c.LocalPath,
				SHA256:    c.SHA256,
				SizeBytes: c.SizeBytes,
				Required:  true,
			})
		}
	}
	return manifest
}
