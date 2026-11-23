package finalize

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	capfinalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// ─── finalize bridge ─────────────────────────────────────────────────────────

// SpineAdapter is the compatibility seam between the new neutral
// finalize contract and the existing stockpipeline/finalization types. It is
// intentionally private: callers continue to use the public legacy APIs.
type SpineAdapter struct {
	finalizer    capfinalization.JobFinalizer
	legacyResult *capfinalization.FinalizationResult
}

var _ Port = (*SpineAdapter)(nil)

func (a *SpineAdapter) Complete(ctx context.Context, request Request) (Result, error) {
	if a == nil || a.finalizer == nil {
		return Result{}, fmt.Errorf("%w: finalize adapter has no JobFinalizer", core.ErrFinalizerAbsent)
	}

	legacyRequest, err := BuildFinalizationRequest(
		request.JobID,
		toLegacyLease(request.Lease),
		request.ResultData,
		toLegacyChunks(request.Artifacts),
		toLegacyMetadata(request.Metadata),
		request.Fingerprint,
	)
	if err != nil {
		return Result{}, err
	}

	result, err := a.finalizer.CompleteWithArtifacts(ctx, *legacyRequest)
	if err != nil {
		return Result{}, err
	}
	a.legacyResult = result
	return fromLegacyResult(result), nil
}

func (a *SpineAdapter) legacyFinalizationResult() *capfinalization.FinalizationResult {
	if a == nil {
		return nil
	}
	return a.legacyResult
}

func newStockFinalizeRequest(jobID string, lease capfinalization.Lease, resultData []byte, chunks []core.ChunkState, metadata core.MetadataState, fingerprint string) Request {
	return Request{
		JobID:       jobID,
		Lease:       fromLegacyLease(lease),
		ResultData:  append([]byte(nil), resultData...),
		Fingerprint: fingerprint,
		Artifacts:   fromLegacyChunks(chunks),
		Metadata: Metadata{
			LocalPath:          metadata.LocalPath,
			SHA256:             metadata.SHA256,
			SizeBytes:          metadata.SizeBytes,
			RemoteFileID:       metadata.RemoteFileID,
			RemoteWebViewLink:  metadata.RemoteWebViewLink,
			DriveUploadSkipped: metadata.DriveUploadSkipped,
		},
	}
}

func fromLegacyLease(lease capfinalization.Lease) Lease {
	return Lease{
		JobID:     lease.JobID,
		WorkerID:  lease.WorkerID,
		LeaseID:   lease.LeaseID,
		Attempt:   lease.Attempt,
		ExpiresAt: lease.ExpiresAt,
	}
}

func toLegacyLease(lease Lease) capfinalization.Lease {
	return capfinalization.Lease{
		JobID:     lease.JobID,
		WorkerID:  lease.WorkerID,
		LeaseID:   lease.LeaseID,
		Attempt:   lease.Attempt,
		ExpiresAt: lease.ExpiresAt,
	}
}

func fromLegacyChunks(chunks []core.ChunkState) []Artifact {
	artifacts := make([]Artifact, 0, len(chunks))
	for _, chunk := range chunks {
		artifacts = append(artifacts, Artifact{
			Index:                    chunk.Index,
			ArtifactID:               chunk.ArtifactID,
			Filename:                 chunk.Filename,
			LocalPath:                chunk.LocalPath,
			SourceURL:                chunk.SourceURL,
			SourceProvider:           chunk.SourceProvider,
			SourceVideoID:            chunk.SourceVideoID,
			TotalChunks:              chunk.TotalChunks,
			DrivePath:                chunk.DrivePath,
			PolicyVersion:            chunk.PolicyVersion,
			TimestampDriveFolderLink: chunk.TimestampDriveFolderLink,
			TimestampFolderID:        chunk.TimestampFolderID,
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
			RemoteFileID:             chunk.RemoteFileID,
			RemoteWebViewLink:        chunk.RemoteWebViewLink,
			RemoteDownloadLink:       chunk.RemoteDownloadLink,
		})
	}
	return artifacts
}

func toLegacyChunks(artifacts []Artifact) []core.ChunkState {
	chunks := make([]core.ChunkState, 0, len(artifacts))
	for _, artifact := range artifacts {
		chunks = append(chunks, core.ChunkState{
			Index:                    artifact.Index,
			ArtifactID:               artifact.ArtifactID,
			Filename:                 artifact.Filename,
			LocalPath:                artifact.LocalPath,
			SourceURL:                artifact.SourceURL,
			SourceProvider:           artifact.SourceProvider,
			SourceVideoID:            artifact.SourceVideoID,
			TotalChunks:              artifact.TotalChunks,
			DrivePath:                artifact.DrivePath,
			PolicyVersion:            artifact.PolicyVersion,
			TimestampDriveFolderLink: artifact.TimestampDriveFolderLink,
			TimestampFolderID:        artifact.TimestampFolderID,
			StartSec:                 artifact.StartSec,
			EndSec:                   artifact.EndSec,
			Title:                    artifact.Title,
			Description:              artifact.Description,
			Round:                    artifact.Round,
			Tags:                     append([]string(nil), artifact.Tags...),
			Category:                 artifact.Category,
			Slug:                     artifact.Slug,
			SHA256:                   artifact.SHA256,
			SizeBytes:                artifact.SizeBytes,
			RemoteFileID:             artifact.RemoteFileID,
			RemoteWebViewLink:        artifact.RemoteWebViewLink,
			RemoteDownloadLink:       artifact.RemoteDownloadLink,
		})
	}
	return chunks
}

func toLegacyMetadata(metadata Metadata) core.MetadataState {
	return core.MetadataState{
		LocalPath:          metadata.LocalPath,
		SHA256:             metadata.SHA256,
		SizeBytes:          metadata.SizeBytes,
		RemoteFileID:       metadata.RemoteFileID,
		RemoteWebViewLink:  metadata.RemoteWebViewLink,
		DriveUploadSkipped: metadata.DriveUploadSkipped,
	}
}

func fromLegacyResult(result *capfinalization.FinalizationResult) Result {
	if result == nil {
		return Result{}
	}
	refs := make([]ArtifactRef, 0, len(result.ArtifactRefs))
	for _, ref := range result.ArtifactRefs {
		refs = append(refs, ArtifactRef{
			ArtifactID:    ref.ArtifactID,
			AssetID:       ref.AssetID,
			Kind:          string(ref.Kind),
			SourceVersion: ref.SourceVersion,
			ContentHash:   ref.ContentHash,
		})
	}
	return Result{
		JobID:        result.JobID,
		Status:       result.Status,
		CompletedAt:  result.CompletedAt,
		ArtifactRefs: refs,
	}
}
