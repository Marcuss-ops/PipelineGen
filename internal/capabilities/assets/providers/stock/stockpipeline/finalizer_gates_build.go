// Package stockpipeline — finalizer_gates_build.go — BuildFinalizationRequest.
//
// Composes both verification gates + Lease + ResultManifest + chunk/metadata
// projections into the canonical FinalizationRequest the JobFinalizer accepts.
package stockpipeline

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// BuildFinalizationRequest composes both gates + Lease + ResultManifest
// + chunk projections + metadata projection into the canonical
// FinalizationRequest the JobFinalizer accepts.
//
// Idempotency: same inputs → byte-stable FinalizationRequest. A retry
// with the same triple (jobID, attempt, sha256-set) is byte-equivalent
// to the prior request, so the JobFinalizer.IdempotencyCache +
// UNIQUE(job_id, attempt, result_hash) surfaces collapse to one row.
//
// SourceVersion per chunk = Index + 1 (1-based). SourceVersion per
// metadata = 1 (single metadata per run). These are logical versions
// in the asset_versions table; monotone per asset_id via the
// MAX(version_number)+1 SELECT inside AssetTxFinalizer.
//
// IdempotencyKey per artifact = "stock:" + sha256[:16] — same content
// → same key → publisher.Publish returns PublishSkipped on retry, and
// the AssetTxFinalizer.OperationResult is invariant across replays.
// stockMetadataArtifactMetadata builds the run-level metadata artifact's
// metadata map. `job_id` is always carried (provenance for the catalog when
// the artifact IS committed); `drive_upload_skipped` is the explicit opt-out
// signal consumed by the finalizer's catalog-commit gate.
func stockMetadataArtifactMetadata(jobID string, driveUploadSkipped bool) map[string]any {
	if !driveUploadSkipped {
		// Published metadata keeps its historical envelope: nil map, no fields.
		return nil
	}
	return map[string]any{
		"job_id":               jobID,
		"drive_upload_skipped": true,
	}
}

func BuildFinalizationRequest(
	jobID string,
	lease finalization.Lease,
	resultData []byte,
	chunks []ChunkState,
	metadata MetadataState,
	runFingerprint string,
) (*finalization.FinalizationRequest, error) {
	// Gates first — fail-fast before composing the request.
	if err := VerifyChunks(chunks); err != nil {
		return nil, err
	}
	if err := VerifyMetadata(metadata); err != nil {
		return nil, err
	}
	if jobID == "" {
		return nil, fmt.Errorf("stock: BuildFinalizationRequest: jobID empty")
	}
	if lease.JobID != jobID {
		return nil, fmt.Errorf("stock: BuildFinalizationRequest: lease.JobID=%q != jobID=%q",
			lease.JobID, jobID)
	}

	arts := make([]finalization.PublishedArtifact, 0, 1+len(chunks))
	// Stock is the asset family. The acquisition provider remains in
	// source_provider for provenance and must not change unified filters.

	// (1) Metadata artifact (always present, Required:true).
	metaIdemKey, errMeta := asset.SHA256IdempotencyKey("stock", metadata.SHA256)
	if errMeta != nil {
		return nil, errors.Join(
			ErrStockMetadataHashInvalid,
			fmt.Errorf("metadata"),
			errMeta,
		)
	}
	arts = append(arts, finalization.PublishedArtifact{
		ArtifactID:     jobID + ":" + string(finalization.KindMetadata),
		Kind:           finalization.KindMetadata,
		Filename:       "metadata.json",
		MIMEType:       "application/json",
		SizeBytes:      metadata.SizeBytes,
		SHA256:         metadata.SHA256,
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: metaIdemKey,
		// PR-STOCK-METADATA-LOCAL-ONLY (Sept 2026): the artifact stays
		// declared (the manifest contract requires it) but with
		// RuntimeConfig.SkipMetadataUpload it has no Drive location, so it
		// MUST NOT become a media_assets row — an asset with no home is
		// noise the operator cannot act on (and it made every run leave a
		// DISCOVERED "metadata.json" row behind). The finalizer reads this
		// flag and skips the catalog commit.
		ArtifactMetadata: stockMetadataArtifactMetadata(jobID, metadata.DriveUploadSkipped),
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       metadata.RemoteFileID,
			WebViewLink:  metadata.RemoteWebViewLink,
			DownloadLink: "",
			FolderID:     "",
			FolderPath:   "",
			Action:       finalization.PublishCreated,
		},
		Source: "stock",
	})

	// (2) Chunk artifacts (one per ChunkState, Required:true).
	for _, c := range chunks {
		chunkIdemKey, errChunk := asset.SHA256IdempotencyKey("stock", c.SHA256)
		if errChunk != nil {
			return nil, errors.Join(
				ErrStockChunkHashInvalid,
				fmt.Errorf("chunk[%d] (artifact=%s)", c.Index, c.ArtifactID),
				errChunk,
			)
		}
		chunkDuration := c.EndSec - c.StartSec
		if chunkDuration < 0 {
			chunkDuration = 0
		}
		// The chunk artifact's Source is the ACQUISITION PROVIDER, not the
		// "stock" family label. Two independent producers commit the same
		// clip into media_assets — the stock pipeline's own post-publication
		// commit and this single-TX spine write — and the canonical outbox
		// event_key is provider-scoped
		// (idempotency.OutboxKey(event, source, assetID, sourceVersion)).
		// Labelling the spine write "stock" while the post-publication commit
		// labels the same bytes "youtube" produced TWO asset.index.requested
		// rows for one asset, and the later write also clobbered the persisted
		// `source` (losing the YouTube provenance), because the media upsert
		// writes source insert-wins / unconditional. Using the acquisition
		// provider makes the keys collide and the second write a genuine no-op
		// on the outbox, while `source_provider`/`source_video_id` keep the
		// provider provenance explicit. The stock FAMILY is carried by
		// asset_kind="stock_video" / semantic_role="stock" below, which is
		// what the unified taxonomy filters read.
		chunkSource := c.SourceProvider
		if chunkSource == "" {
			chunkSource = "stock"
		}
		chunkMeta := map[string]any{
			"asset_kind":                  StockAssetKind,
			"semantic_role":               StockSemanticRole,
			"folder_id":                   c.TimestampFolderID,
			"title":                       c.Title,
			"description":                 c.Description,
			"start_sec":                   c.StartSec,
			"end_sec":                     c.EndSec,
			"source_url":                  c.SourceURL,
			"source_provider":             c.SourceProvider,
			"source_video_id":             c.SourceVideoID,
			"total_chunks":                c.TotalChunks,
			"drive_path":                  c.DrivePath,
			"timestamp_drive_folder_link": c.TimestampDriveFolderLink,
			"timestamp_folder_id":         c.TimestampFolderID,
			"policy_version":              c.PolicyVersion,
			"indexing_status":             "INDEXING_PENDING",
			"chunk_index":                 c.Index,
			"job_id":                      jobID,
			"run_fingerprint":             runFingerprint,
			"chunk_filename":              c.Filename,
			"chunk_duration_sec":          chunkDuration,
			"chunk_drive_file_id":         c.RemoteFileID,
			"chunk_drive_link":            c.RemoteWebViewLink,
			"timestamp_title":             c.Title,
			"timestamp_slug":              c.Slug,
			"timestamp_start_sec":         c.StartSec,
			"timestamp_end_sec":           c.EndSec,
		}
		if c.Round > 0 {
			chunkMeta["round"] = c.Round
		}
		if len(c.Tags) > 0 {
			chunkMeta["tags"] = c.Tags
		}
		if c.Category != "" {
			chunkMeta["category"] = c.Category
		}
		if c.Slug != "" {
			chunkMeta["slug"] = c.Slug
		}

		arts = append(arts, finalization.PublishedArtifact{
			ArtifactID:       c.ArtifactID,
			Kind:             finalization.KindVideo,
			Filename:         c.Filename,
			MIMEType:         "video/mp4",
			SizeBytes:        c.SizeBytes,
			SHA256:           c.SHA256,
			SourceVersion:    int64(c.Index + 1),
			Requirement:      finalization.ArtifactRequirementRequired,
			IdempotencyKey:   chunkIdemKey,
			Description:      c.Description,
			ArtifactMetadata: chunkMeta,
			Source:           chunkSource,
			Location: finalization.AssetLocation{
				Provider:     "drive",
				FileID:       c.RemoteFileID,
				WebViewLink:  c.RemoteWebViewLink,
				DownloadLink: c.RemoteDownloadLink,
				// The parent Drive folder of the published chunk. This was
				// hardcoded "" while the chunk's real folder was already known
				// (ChunkState.TimestampFolderID), and because the media upsert
				// writes folder_id unconditionally the empty value BLANKED the
				// folder id the post-publication commit had just persisted —
				// every stock row ended up with folder_id="" and no operator
				// could navigate to the Drive folder from a search hit.
				FolderID:   c.TimestampFolderID,
				FolderPath: "",
				Action:     finalization.PublishCreated,
			},
		})
	}

	return &finalization.FinalizationRequest{
		Lease: lease,
		Result: finalization.ResultManifest{
			SchemaVersion: "v1",
			JobID:         jobID,
			Attempt:       lease.Attempt,
			Data:          json.RawMessage(resultData),
		},
		Artifacts: arts,
	}, nil
}

// ── OrchestrationResult — Service-side carrier ─────────────────────

// OrchestrationResult is the typed envelope Service.HandleJob
// receives after Orchestrator.Run completes. It carries the
// worker-side ArtifactManifest (so the JobStatusResponse and the
// legacy-shape projections still render), the per-chunk states
// (Chunks), and the per-run metadata state (Metadata) — exactly
// the inputs the canonical BuildFinalizationRequest needs.
//
// Service.HandleJob stitches the Lease + ResultData + jobID
// into a FinalizationRequest and calls finalizer.CompleteWithArtifacts.
// The orchestrator does NOT construct the FinalizationRequest
// itself because the Lease is owned by Service.HandleJob (it
// reads LeaseID/WorkerID/Attempt from job+tools), so the
// per-attempt fingerprint is stable across the orchestrator→finReq
// handoff.
//
// On gate failure Orchestrator.Run returns (nil, error); the
// OrchestrationResult struct is only seen on the happy path.
type OrchestrationResult struct {
	// Manifest is the canonical wire artefact (5-artifact or per-chunk
	// envelope post-Cutover) — surfaced via "__artifact_manifest" key
	// in the worker result map straight after the orchestrator returns.
	Manifest *job.ArtifactManifest

	// Chunks is the run's prepared+published chunk states (from the
	// future Commit 4-7 chunk-rendering ladder). Empty today; the
	// verify_chunks gate raises ErrStockNoChunksFinalized on every
	// production run until Commit 4-7 lands.
	Chunks []ChunkState

	// Metadata is the run's metadata.json state. Empty today;
	// the verify_metadata gate raises ErrStockMetadataNotPublished
	// until Commit 4-7 populates the field.
	Metadata MetadataState
}
