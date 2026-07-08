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

	// ── IndexingStatus (PR-008) ────────────────────────────────────
	// Projection-time literal: stock.finalize emits "INDEXING_PENDING"
	// without doing a DB SELECT on media_assets.index_state (which
	// would block throughput on every chunk finalization). The
	// IndexingHandler downstream (outbox asset.index.requested
	// consumer) will overwrite this to "INDEXED" in the Qdrant
	// payload after a successful upsert, but media_assets.index_state
	// in the SQLite DB is the canonical SSOT for the lifecycle
	// state. This field is the wire-snapshot hint for offline
	// consumers; it is NOT authoritative for retry/decision logic.
	//
	// No `omitempty` per godlike/07 NO-FAKE-AVAILABILITY: the
	// literal value is ALWAYS present at projection time so
	// consumers see a stable, parseable value. Setting this to
	// "" would silently drop the hint.
	IndexingStatus string `json:"indexing_status"`

	// ── Timestamp folder metadata (PR-TIMESTAMP-FOLDER-LINK) ──────
	// Parent timestamp Drive folder metadata. Captured from the
	// metadataPublished.Location after Phase 2 (metadata.json upload).
	// For legacy runs: points to the timestamp parent folder.
	// For explicit-clips runs: points to the metadata/ subfolder
	// (operators can click the Drive breadcrumb to go up one level).
	// Propagated to Qdrant payload for "open folder in Drive"
	// navigation from search results.
	TimestampDriveFolderLink string `json:"timestamp_drive_folder_link,omitempty"`
	TimestampFolderID        string `json:"timestamp_folder_id,omitempty"`
}

// IndexingStatusPending is the canonical projection-time literal
// for the IndexingStatus field on StockRunMetadata. Per PR-008 the
// stock.finalize path emits this value at the metadata.json build
// site (no DB SELECT). The IndexingHandler downstream overwrites
// to "INDEXED" in the Qdrant payload after a successful upsert.
const IndexingStatusPending = "INDEXING_PENDING"

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
	Description      string  `json:"description,omitempty"`
	// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): the 4 typed
	// content fields that travel ClipSpec → ClipPlan → ChunkState →
	// ChunkMetadataEntry. omitempty on all so deterministic-planner
	// runs (where the 4 stay at zero/nil) produce the same wire shape
	// as the pre-PR baseline.
	Round     int      `json:"round,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Category  string   `json:"category,omitempty"`
	Slug      string   `json:"slug,omitempty"`
	SHA256    string   `json:"sha256"`
	SizeBytes int64    `json:"size_bytes"`
	LocalPath string   `json:"local_path,omitempty"`
	// PR-TIMESTAMP-FOLDER-LINK (July 2026): parent timestamp Drive
	// folder metadata. Per-run scalar (all chunks in the same
	// timestamp block share the same parent folder).
	TimestampDriveFolderLink string `json:"timestamp_drive_folder_link,omitempty"`
	TimestampFolderID        string `json:"timestamp_folder_id,omitempty"`
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
			Description:      c.Description,
			// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): the 4
			// content fields propagate verbatim (godlike/06 SSOT
			// one-owner-per-fact: ChunkState is the canonical owner
			// after step_publish copies them in from ClipPlan).
			Round:     c.Round,
			Tags:      append([]string(nil), c.Tags...), // defensive copy so the entry doesn't share ChunkState's slice
			Category:  c.Category,
			Slug:      c.Slug,
			SHA256:    c.SHA256,
			SizeBytes: c.SizeBytes,
			LocalPath: c.LocalPath,
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
		// PR-008 (July 2026): hardcode the projection-time literal
		// to avoid a media_assets.index_state DB SELECT in the
		// hot path. The IndexingHandler (outbox consumer) flips
		// to "INDEXED" in the Qdrant payload after a successful
		// upsert; media_assets.index_state in the DB is the
		// canonical SSOT for retry/decision logic.
		IndexingStatus: IndexingStatusPending,
		// PR-TIMESTAMP-FOLDER-LINK (July 2026): run-level parent
		// timestamp folder metadata. Captured from the first chunk's
		// ChunkState after Phase 2 backfill (step_publish.go). All
		// chunks share the same parent folder per godlike/06 SSOT.
		TimestampDriveFolderLink: timestampFolderLink(chunks),
		TimestampFolderID:        timestampFolderID(chunks),
	}
}

// timestampFolderLink returns the TimestampDriveFolderLink from the
// first chunk (run-level scalar: all chunks share the same parent
// folder after the Phase 2 backfill). Returns "" when empty.
func timestampFolderLink(chunks []ChunkState) string {
	if len(chunks) > 0 {
		return chunks[0].TimestampDriveFolderLink
	}
	return ""
}

// timestampFolderID returns the TimestampFolderID from the first chunk.
// Symmetric with timestampFolderLink.
func timestampFolderID(chunks []ChunkState) string {
	if len(chunks) > 0 {
		return chunks[0].TimestampFolderID
	}
	return ""
}

// writeAndHashPerClipMetadata stages a per-clip metadata.json
// envelope (StockRunMetadata with Chunks[] = [cs]) to a temp
// file, computes its SHA256, and returns (LocalPath, SHA256,
// SizeBytes, error). PR-PER-CLIP-METADATA (July 2026, DoD 5):
// the per-clip envelope reuses the canonical buildStockRunMetadata
// helper with a singleton chunks slice so the wire shape is
// consistent with the run-level metadata.json — only the
// chunks[] cardinality differs.
//
// godlike/06 SSOT (one canonical owner per fact): this is the
// canonical writer of per-clip metadata envelopes. The
// singleton-chunks reuse of buildStockRunMetadata keeps
// StockRunMetadata as the SOLE metadata wire shape — no new
// envelope types are introduced. The per-clip envelope is NOT
// a minimal ChunkMetadataEntry JSON (godlike/07 typed-shape
// contract: re-using the canonical StockRunMetadata envelope
// lets a downstream consumer (Qdrant semantic-payload
// enrichment) locate the run via ANY metadata.json in the
// subtree — all carry the same JobID/RunFingerprint/PolicyVersion
// fields the consumer keys off).
//
// godlike/07 minimum-blast-radius: mirrors writeAndHashMetadata
// exactly (same MarshalIndent / CreateTemp / Write / Close /
// ComputeSHA256 / cleanup pattern) so the per-clip path inherits
// the same defensive contract (errors.Is probes, close-error
// surfacing, fail-closed on the nil RunInput guard). The
// additional empty-ArtifactID guard catches a programming bug
// (caller forgot to stamp ChunkState.ArtifactID before invoking
// the per-clip writer) before the temp file write would
// silently emit a malformed envelope.
func writeAndHashPerClipMetadata(in *RunInput, cs ChunkState, runFingerprint string) (string, string, int64, error) {
	if in == nil {
		return "", "", 0, errors.New("writeAndHashPerClipMetadata: nil RunInput")
	}
	if cs.ArtifactID == "" {
		return "", "", 0, errors.New("writeAndHashPerClipMetadata: empty ChunkState.ArtifactID")
	}
	meta := buildStockRunMetadata(in, []ChunkState{cs}, runFingerprint)
	raw, mErr := json.MarshalIndent(meta, "", "  ")
	if mErr != nil {
		return "", "", 0, fmt.Errorf("writeAndHashPerClipMetadata: marshal: %w", mErr)
	}
	sizeBytes := int64(len(raw))

	f, cErr := os.CreateTemp("", "pipelinegen-stock-clip-metadata-*.json")
	if cErr != nil {
		return "", "", 0, fmt.Errorf("writeAndHashPerClipMetadata: create temp: %w", cErr)
	}
	cleanup := func() { _ = os.Remove(f.Name()) }

	if _, wErr := f.Write(raw); wErr != nil {
		closeErr := f.Close() // P3 fix: surface close error alongside write error (godlike/07 no silent error)
		cleanup()
		if closeErr != nil {
			return "", "", 0, fmt.Errorf("writeAndHashPerClipMetadata: write %s: %w (close: %v)", f.Name(), wErr, closeErr)
		}
		return "", "", 0, fmt.Errorf("writeAndHashPerClipMetadata: write %s: %w", f.Name(), wErr)
	}
	if cErr := f.Close(); cErr != nil {
		cleanup()
		return "", "", 0, fmt.Errorf("writeAndHashPerClipMetadata: close %s: %w", f.Name(), cErr)
	}
	h, hErr := job.ComputeSHA256(f.Name())
	if hErr != nil {
		cleanup()
		return "", "", 0, fmt.Errorf("writeAndHashPerClipMetadata: hash %s: %w", f.Name(), hErr)
	}
	return f.Name(), h, sizeBytes, nil
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
