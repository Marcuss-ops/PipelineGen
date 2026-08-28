// Package stockpipeline keeps legacy naming entry points for compatibility.
//
// This file is the single "legacy compatibility seam" for the split
// (PR-STOCKPIPELINE-PHASE-SPLIT): it holds the legacy naming helpers AND
// the neutral-boundary bridges (cleanup / ingest / publish / reconcile /
// finalize) that let the legacy stockpipeline package drive the new
// ownership-neutral sub-packages without adding new production files to
// the registered hotspot. Callers continue to use the public legacy APIs.
package stockpipeline

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	acquisition "github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/cleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/finalize"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/ingest"
	stockpublish "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/publish"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/reconcile"
	capfinalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/shared/pathutil"
)

// ─── Legacy naming entry points ──────────────────────────────────────────────

func namingInput(in *RunInput) stockpublish.NamingInput {
	if in == nil {
		return stockpublish.NamingInput{}
	}
	return stockpublish.NamingInput{FolderName: in.FolderName, Subfolder: in.Subfolder, SearchQueries: in.SearchQueries, DirectURLs: in.DirectURLs, DriveFolderID: in.DriveFolderID, DriveFolderResolved: in.DriveFolderResolved}
}

func RootFolderName(in *RunInput) string {
	if in == nil {
		return "stock"
	}
	// SanitizedRootName maps empty/whitespace input to pathutil's
	// "untitled" sentinel, so it must not short-circuit the fallback
	// cascade here. Only a genuinely non-empty FolderName (after trim)
	// that sanitizes to a real name wins; otherwise we fall through to
	// Subfolder, the legacy query/URL chain, and finally the UTC date
	// fallback owned by the publish capability package.
	if name := SanitizedRootName(in.FolderName); name != "" && name != "untitled" {
		return name
	}
	if name := strings.TrimSpace(in.Subfolder); name != "" {
		return name
	}
	if name := firstLegacyQuery(in.SearchQueries); name != "" {
		return name
	}
	if name := firstLegacyURL(in.DirectURLs); name != "" {
		return name
	}
	return stockpublish.RootFolderName(namingInput(in))
}
func ResolvedFolderID(in *RunInput) string   { return stockpublish.ResolvedFolderID(namingInput(in)) }
func SanitizedRootName(s string) string      { return pathutil.SafeFolderName(strings.TrimSpace(s)) }
func LegacyQuery(queries []string) string    { return domaindelivery.FirstSanitizedQuery(queries) }
func LegacyURLBasename(urls []string) string { return domaindelivery.FirstSanitizedURLBasename(urls) }
func SanitizedURLBasename(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if parsed, err := url.Parse(s); err == nil && parsed.Path != "" {
		s = parsed.Path
	}
	base := filepath.Base(s)
	return SanitizedRootName(strings.TrimSuffix(base, filepath.Ext(base)))
}
func TimestampGroupName(in *RunInput) string { return stockpublish.TimestampGroupName(namingInput(in)) }
func ClipFolderName(in *RunInput, plan ClipPlan, fallback string) string {
	return stockpublish.ClipFolderName(namingInput(in), publishClipNamingInput(plan), fallback)
}
func TimestampParentGroupName(in *RunInput) string {
	return stockpublish.TimestampParentGroupName(namingInput(in))
}
func PerClipLeafName(plan ClipPlan) string {
	if raw := strings.TrimSpace(plan.Slug); raw != "" {
		if safe := SanitizedRootName(raw); safe != "" && safe != "untitled" && domaindelivery.ContainsAlphanumeric(safe) {
			return safe
		}
	}
	if title := strings.TrimSpace(plan.Title); title != "" {
		if slugged := SlugifyTitle(title); slugged != "" && slugged != "untitled" {
			return slugged
		}
	}
	return fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d", int(plan.StartSec)/3600, (int(plan.StartSec)%3600)/60, int(plan.StartSec)%60, int(plan.EndSec)/3600, (int(plan.EndSec)%3600)/60, int(plan.EndSec)%60)
}
func TimestampParentLeafName(plan ClipPlan) string {
	return PerClipLeafName(ClipPlan{Slug: plan.ParentSlug, Title: plan.Title, StartSec: plan.StartSec, EndSec: plan.EndSec})
}
func firstLegacyQuery(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return pathutil.SafeFolderName(strings.TrimSpace(value))
		}
	}
	return ""
}
func firstLegacyURL(values []string) string {
	for _, value := range values {
		if name := SanitizedURLBasename(value); name != "" && name != "untitled" {
			return name
		}
	}
	return ""
}

func SlugifyTitle(title string) string { return stockpublish.SlugifyTitle(title) }

// ─── publish bridge ──────────────────────────────────────────────────────────

func publishNamingInput(in *RunInput) stockpublish.NamingInput {
	if in == nil {
		return stockpublish.NamingInput{}
	}
	return stockpublish.NamingInput{
		FolderName: in.FolderName, Subfolder: in.Subfolder,
		SearchQueries: append([]string(nil), in.SearchQueries...),
		DirectURLs:    append([]string(nil), in.DirectURLs...),
		DriveFolderID: in.DriveFolderID, DriveFolderResolved: in.DriveFolderResolved,
	}
}

func publishClipNamingInput(plan ClipPlan) stockpublish.ClipNamingInput {
	return stockpublish.ClipNamingInput{
		Round: plan.Round, Title: plan.Title, Slug: plan.Slug, ParentSlug: plan.ParentSlug,
		StartSec: plan.StartSec, EndSec: plan.EndSec,
	}
}

func publishRootFolderName(in *RunInput) string {
	return stockpublish.RootFolderName(publishNamingInput(in))
}
func publishResolvedFolderID(in *RunInput) string {
	return stockpublish.ResolvedFolderID(publishNamingInput(in))
}
func publishTimestampGroupName(in *RunInput) string {
	return stockpublish.TimestampGroupName(publishNamingInput(in))
}
func publishTimestampParentGroupName(in *RunInput) string {
	return stockpublish.TimestampParentGroupName(publishNamingInput(in))
}
func publishClipFolderName(in *RunInput, plan ClipPlan, fallback string) string {
	return stockpublish.ClipFolderName(publishNamingInput(in), publishClipNamingInput(plan), fallback)
}
func publishPerClipLeafName(plan ClipPlan) string {
	return stockpublish.PerClipLeafName(publishClipNamingInput(plan))
}
func publishSlugifyTitle(title string) string { return stockpublish.SlugifyTitle(title) }

func toPublishChunk(chunk ChunkState, rootFolder, folderID, leaf string) stockpublish.Chunk {
	return stockpublish.Chunk{Index: chunk.Index, ArtifactID: chunk.ArtifactID, Filename: chunk.Filename,
		LocalPath: chunk.LocalPath, SizeBytes: chunk.SizeBytes, SHA256: chunk.SHA256,
		Description: chunk.Description, RootFolder: rootFolder, FolderID: folderID,
		FolderResolved: chunk.RemoteFileID != "" || folderID != "", PathLeaf: leaf}
}

func toPublishMetadata(metadata MetadataState, artifactID, rootFolder, folderID, leaf string) stockpublish.Metadata {
	return stockpublish.Metadata{ArtifactID: artifactID, Filename: "metadata.json", LocalPath: metadata.LocalPath,
		SizeBytes: metadata.SizeBytes, SHA256: metadata.SHA256, RootFolder: rootFolder,
		FolderID: folderID, FolderResolved: metadata.RemoteFileID != "" || folderID != "", PathLeaf: leaf}
}

// ─── cleanup bridge ──────────────────────────────────────────────────────────

// stockCleanupReleaser is the only bridge from the ownership-neutral cleanup
// package to the existing acquisition lifecycle. Cleanup owns orchestration;
// acquisition remains the owner of release semantics and lease bookkeeping.
type stockCleanupReleaser struct {
	stager acquisition.SourceStager
}

var _ cleanup.Releaser = (*stockCleanupReleaser)(nil)

func (r *stockCleanupReleaser) Release(ctx context.Context, resource cleanup.Resource) error {
	if r == nil || r.stager == nil {
		return fmt.Errorf("stock cleanup: source stager is not wired")
	}
	if resource.LocalPath == "" {
		return fmt.Errorf("stock cleanup: empty local path for source %q", resource.SourceID)
	}
	return r.stager.Release(ctx, resource.LocalPath)
}

// ─── ingest bridge ───────────────────────────────────────────────────────────

// ingestSourceFromClipPlan is the single mapping boundary between the legacy
// planner DTO and the neutral ingest contract. It intentionally performs no
// deduplication; the step owns ordering and calls ingest.UniqueSources.
func ingestSourceFromClipPlan(plan ClipPlan) ingest.Source {
	return ingest.Source{ID: plan.SourceID, URL: stagingSourceURL(plan)}
}

// ingestSourcesFromClipPlans preserves plan order and multiplicity. Callers
// that need one request per source must apply ingest.UniqueSources afterwards.
func ingestSourcesFromClipPlans(plans []ClipPlan) []ingest.Source {
	sources := make([]ingest.Source, 0, len(plans))
	for _, plan := range plans {
		sources = append(sources, ingestSourceFromClipPlan(plan))
	}
	return sources
}

// stockIngestPreparer adapts the canonical acquisition port to the neutral
// ingest boundary. It keeps acquisition request construction in the parent
// package, where policy-version and caller identity are already available.
type stockIngestPreparer struct {
	stager        acquisition.SourceStager
	policyVersion string
}

var _ ingest.SourcePreparer = (*stockIngestPreparer)(nil)

func (p *stockIngestPreparer) Prepare(ctx context.Context, source ingest.Source) (*ingest.PreparedSource, error) {
	if p == nil || p.stager == nil {
		return nil, fmt.Errorf("stock ingest: source stager is not wired")
	}

	ref := acquisition.SourceRef{URL: source.URL}
	keyRef := acquisition.SourceRef{URL: source.URL, PolicyVersion: p.policyVersion}
	prepared, err := p.stager.Prepare(ctx, acquisition.PrepareRequest{
		Source:         ref,
		IdempotencyKey: acquisition.DeriveIdempotencyKey(keyRef),
		CallerRef:      "stock.stage_sources",
	})
	if err != nil {
		return nil, err
	}
	if prepared == nil {
		return nil, nil
	}
	return &ingest.PreparedSource{
		SourceID:  source.ID,
		LocalPath: prepared.LocalPath,
		Bytes:     prepared.SizeBytes,
	}, nil
}

// ─── reconcile bridge ────────────────────────────────────────────────────────

func reconcileBatchProjection(batch StockBatch) reconcile.Batch {
	return reconcile.Batch{ID: batch.ID, Status: string(batch.Status), ExpectedGroups: batch.ExpectedGroups, ExpectedArtifacts: batch.ExpectedClips, VerifiedArtifacts: batch.VerifiedClips}
}
func reconcileGroupProjection(group StockBatchGroup) reconcile.Group {
	return reconcile.Group{ID: group.ID, BatchID: group.BatchID, Status: string(group.Status), ExpectedArtifacts: group.ExpectedClips, VerifiedArtifacts: group.VerifiedClips}
}
func reconcileArtifactProjection(artifact StockArtifact) reconcile.Artifact {
	return reconcile.Artifact{ID: artifact.ID, BatchID: artifact.BatchID, GroupID: artifact.GroupID, Ordinal: artifact.Ordinal, Status: string(artifact.Status), LastError: artifact.LastError}
}

func reconcileSnapshot(batch StockBatch, groups []StockBatchGroup, artifacts []StockArtifact) reconcile.Snapshot {
	out := reconcile.Snapshot{Batch: reconcileBatchProjection(batch), Groups: make([]reconcile.Group, 0, len(groups)), Artifacts: make([]reconcile.Artifact, 0, len(artifacts))}
	for _, group := range groups {
		out.Groups = append(out.Groups, reconcileGroupProjection(group))
	}
	for _, artifact := range artifacts {
		out.Artifacts = append(out.Artifacts, reconcileArtifactProjection(artifact))
	}
	return out
}

// ─── finalize bridge ─────────────────────────────────────────────────────────

// stockFinalizeAdapter is the compatibility seam between the new neutral
// finalize contract and the existing stockpipeline/finalization types. It is
// intentionally private: callers continue to use the public legacy APIs.
type stockFinalizeAdapter struct {
	finalizer    capfinalization.JobFinalizer
	legacyResult *capfinalization.FinalizationResult
}

var _ finalize.Port = (*stockFinalizeAdapter)(nil)

func (a *stockFinalizeAdapter) Complete(ctx context.Context, request finalize.Request) (finalize.Result, error) {
	if a == nil || a.finalizer == nil {
		return finalize.Result{}, fmt.Errorf("%w: finalize adapter has no JobFinalizer", ErrFinalizerAbsent)
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
		return finalize.Result{}, err
	}

	result, err := a.finalizer.CompleteWithArtifacts(ctx, *legacyRequest)
	if err != nil {
		return finalize.Result{}, err
	}
	a.legacyResult = result
	return fromLegacyResult(result), nil
}

func (a *stockFinalizeAdapter) legacyFinalizationResult() *capfinalization.FinalizationResult {
	if a == nil {
		return nil
	}
	return a.legacyResult
}

func newStockFinalizeRequest(jobID string, lease capfinalization.Lease, resultData []byte, chunks []ChunkState, metadata MetadataState, fingerprint string) finalize.Request {
	return finalize.Request{
		JobID:       jobID,
		Lease:       fromLegacyLease(lease),
		ResultData:  append([]byte(nil), resultData...),
		Fingerprint: fingerprint,
		Artifacts:   fromLegacyChunks(chunks),
		Metadata: finalize.Metadata{
			LocalPath:         metadata.LocalPath,
			SHA256:            metadata.SHA256,
			SizeBytes:         metadata.SizeBytes,
			RemoteFileID:      metadata.RemoteFileID,
			RemoteWebViewLink: metadata.RemoteWebViewLink,
		},
	}
}

func fromLegacyLease(lease capfinalization.Lease) finalize.Lease {
	return finalize.Lease{
		JobID:     lease.JobID,
		WorkerID:  lease.WorkerID,
		LeaseID:   lease.LeaseID,
		Attempt:   lease.Attempt,
		ExpiresAt: lease.ExpiresAt,
	}
}

func toLegacyLease(lease finalize.Lease) capfinalization.Lease {
	return capfinalization.Lease{
		JobID:     lease.JobID,
		WorkerID:  lease.WorkerID,
		LeaseID:   lease.LeaseID,
		Attempt:   lease.Attempt,
		ExpiresAt: lease.ExpiresAt,
	}
}

func fromLegacyChunks(chunks []ChunkState) []finalize.Artifact {
	artifacts := make([]finalize.Artifact, 0, len(chunks))
	for _, chunk := range chunks {
		artifacts = append(artifacts, finalize.Artifact{
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

func toLegacyChunks(artifacts []finalize.Artifact) []ChunkState {
	chunks := make([]ChunkState, 0, len(artifacts))
	for _, artifact := range artifacts {
		chunks = append(chunks, ChunkState{
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

func toLegacyMetadata(metadata finalize.Metadata) MetadataState {
	return MetadataState{
		LocalPath:         metadata.LocalPath,
		SHA256:            metadata.SHA256,
		SizeBytes:         metadata.SizeBytes,
		RemoteFileID:      metadata.RemoteFileID,
		RemoteWebViewLink: metadata.RemoteWebViewLink,
	}
}

func fromLegacyResult(result *capfinalization.FinalizationResult) finalize.Result {
	if result == nil {
		return finalize.Result{}
	}
	refs := make([]finalize.ArtifactRef, 0, len(result.ArtifactRefs))
	for _, ref := range result.ArtifactRefs {
		refs = append(refs, finalize.ArtifactRef{
			ArtifactID:    ref.ArtifactID,
			AssetID:       ref.AssetID,
			Kind:          string(ref.Kind),
			SourceVersion: ref.SourceVersion,
			ContentHash:   ref.ContentHash,
		})
	}
	return finalize.Result{
		JobID:        result.JobID,
		Status:       result.Status,
		CompletedAt:  result.CompletedAt,
		ArtifactRefs: refs,
	}
}
