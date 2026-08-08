// Package adapters — clip_resolver.go implements the canonical
// ports.ClipResolver adapter, wrapping the typed repo methods on
// *assets.ClipsRepository (this PR adds the typed methods).
//
// This adapter replaces the legacy clip_source_builder.clipsResolverPort
// (now typedClipResolverPort) heuristic that silently mixed identifier
// layers with EXPLICIT per-ReferenceType dispatch — there is no fall-back, only:
//
//	RefTypeMediaAssetID        → ResolveByMediaAssetID
//	RefTypeYouTubeVideoID      → ResolveByYouTubeVideoID (LIKE yt_<videoID>_% fan-out)
//	RefTypeDriveFileID         → ResolveByDriveFileID   (exact drive_file_id match)
//	RefTypeExternalProviderID  → ResolveByExternalProviderID
//
// Missing assets are reported as Unresolved references with
// Reason="not_found" — NEVER auto-ingest (the ingest path is separate).
//
// DB errors propagate as the resolver-level error and the per-
// reference Reason="db_error" simultaneously: the resolver-level
// error signals "this batch is degraded" without throwing away the
// per-reference diagnosis the caller needs for partial-success
// UX.
package adapters

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// clipResolverPortReadOnly is the narrow Read surface the
// clipResolverAdapter aggregates over. Defined here (rather than in
// `ports/`) because it is the concrete-method set the infra repo
// satisfies — keeping it next to NewClipResolver localises
// adapter-internal types and avoids dragging the entire scripts
// application package import graph into the production repo's
// test boundary.
//
// Tests inject a hand-rolled stub via NewClipResolverForTest.
type clipResolverPortReadOnly interface {
	ResolveByMediaAssetID(ctx context.Context, id string) (*asset.Asset, error)
	ResolveByYouTubeVideoID(ctx context.Context, videoID string) ([]*asset.Asset, error)
	ResolveByDriveFileID(ctx context.Context, fileID string) ([]*asset.Asset, error)
	ResolveByExternalProviderID(ctx context.Context, provider, externalID string) ([]*asset.Asset, error)
}

// clipResolverAdapter satisfies ports.ClipResolver by dispatching on
// ClipReference.Type via the typed repo methods above. No
// fall-through between arms — invalid Type surfaces as
// UnresolvedReference{Reason:"invalid_type"}.
type clipResolverAdapter struct {
	repo clipResolverPortReadOnly
	log  *zap.Logger
}

// NewClipResolver constructs the production adapter. The repo
// argument must be the concrete *assets.ClipsRepository — the
// canonical typed methods are wired here. nil repo returns a no-op adapter so test fixtures can
// pass nil without nil-checking at every call site (mirrors the
// nil-safe shape of voiceoverGroupsAdapter).
func NewClipResolver(repo *assets.ClipsRepository, log *zap.Logger) ports.ClipResolver {
	if repo == nil {
		return &clipResolverAdapter{repo: nil, log: log}
	}
	return &clipResolverAdapter{repo: repo, log: log}
}

// NewClipResolverForTest is the test-only constructor accepting the
// narrow port interface rather than the concrete repository.
// Production code MUST use NewClipResolver — the narrow port is
// the seam for stub injection only.
func NewClipResolverForTest(repo clipResolverPortReadOnly, log *zap.Logger) ports.ClipResolver {
	return &clipResolverAdapter{repo: repo, log: log}
}

// Compile-time assertion: the adapter satisfies the canonical port.
// A future method change to ClipResolver surfaces here rather than
// at the first production dispatch.
var _ ports.ClipResolver = (*clipResolverAdapter)(nil)

// Compile-time pin: the concrete *assets.ClipsRepository satisfies
// the narrow clipResolverPortReadOnly surface. Without this assertion,
// a future signature drift on clipResolverPortReadOnly fails only at
// the first adapter dispatch (silent runtime panic), not at compile.
// AGENTS.md Pattern 0 prefers the compile-time gate.
var _ clipResolverPortReadOnly = (*assets.ClipsRepository)(nil)

// Resolve dispatches each input reference to the typed arm.
// Per-reference failures (empty value, invalid type, not found)
// flow into result.Unresolved; DB-level errors flow into BOTH the
// returned error and the matching Unresolved entry (so the
// caller can surface partial success even on a degraded batch).
func (a *clipResolverAdapter) Resolve(ctx context.Context, refs []ports.ClipReference) (*ports.ClipResolutionResult, error) {
	result := &ports.ClipResolutionResult{
		Resolved:   make([]ports.ClipEvidence, 0, len(refs)),
		Unresolved: make([]ports.UnresolvedReference, 0),
	}
	if len(refs) == 0 {
		return result, nil
	}
	var firstDBErr error
	for _, ref := range refs {
		ref := ref // capture for the recursive path
		if err := a.resolveOne(ctx, ref, result); err != nil {
			if firstDBErr == nil {
				firstDBErr = err
			}
			if a.log != nil {
				a.log.Warn("clip resolver: db error",
					zap.String("type", string(ref.Type)),
					zap.String("value", ref.Value),
					zap.Error(err))
			}
		}
	}
	return result, firstDBErr
}

// resolveOne handles one ClipReference. Returns non-nil only on
// DB-level errors; per-reference failures append to result.Unresolved
// and return nil to keep the batch loop alive.
func (a *clipResolverAdapter) resolveOne(ctx context.Context, ref ports.ClipReference, result *ports.ClipResolutionResult) error {
	if ref.Value == "" {
		result.Unresolved = append(result.Unresolved, ports.UnresolvedReference{
			Reference: ref,
			Reason:    ports.ResolveReasonEmptyValue,
		})
		return nil
	}
	if !ref.Type.Valid() {
		result.Unresolved = append(result.Unresolved, ports.UnresolvedReference{
			Reference: ref,
			Reason:    ports.ResolveReasonInvalidType,
		})
		return nil
	}
	if a.repo == nil {
		// nil-repo adapter (test fixture): synthesise "not_found"
		// for every dispatch so callers see the resolution lag
		// without surprise nil-deref panics.
		result.Unresolved = append(result.Unresolved, ports.UnresolvedReference{
			Reference: ref,
			Reason:    ports.ResolveReasonNotFound,
		})
		return nil
	}
	switch ref.Type {
	case ports.RefTypeMediaAssetID:
		assetItem, err := a.repo.ResolveByMediaAssetID(ctx, ref.Value)
		return appendOneAsset(ref, assetItem, err, result)
	case ports.RefTypeYouTubeVideoID:
		list, err := a.repo.ResolveByYouTubeVideoID(ctx, ref.Value)
		return appendManyAssets(ref, list, err, result)
	case ports.RefTypeDriveFileID:
		list, err := a.repo.ResolveByDriveFileID(ctx, ref.Value)
		return appendManyAssets(ref, list, err, result)
	case ports.RefTypeExternalProviderID:
		provider, extID, ok := ports.ParseExternalProviderValue(ref.Value)
		if !ok {
			result.Unresolved = append(result.Unresolved, ports.UnresolvedReference{
				Reference: ref,
				Reason:    ports.ResolveReasonExternalProviderValueFormat,
			})
			return nil
		}
		list, err := a.repo.ResolveByExternalProviderID(ctx, provider, extID)
		return appendManyAssets(ref, list, err, result)
	default:
		// Valid() guards above already filtered; this branch is
		// defensive in case a future ReferenceType constant is
		// added without a matching switch arm.
		result.Unresolved = append(result.Unresolved, ports.UnresolvedReference{
			Reference: ref,
			Reason:    ports.ResolveReasonInvalidType,
		})
		return nil
	}
}

// appendOneAsset handles single-row lookups (RefTypeMediaAssetID).
func appendOneAsset(ref ports.ClipReference, assetItem *asset.Asset, err error, result *ports.ClipResolutionResult) error {
	if err != nil {
		reportDBError(ref, err, result)
		return err
	}
	if assetItem == nil {
		result.Unresolved = append(result.Unresolved, ports.UnresolvedReference{
			Reference: ref,
			Reason:    ports.ResolveReasonNotFound,
		})
		return nil
	}
	result.Resolved = append(result.Resolved, evidenceFromAsset(ref, assetItem))
	return nil
}

// appendManyAssets handles multi-row lookups (Refs that fan out:
// YouTube, Drive, ExternalProvider).
func appendManyAssets(ref ports.ClipReference, list []*asset.Asset, err error, result *ports.ClipResolutionResult) error {
	if err != nil {
		reportDBError(ref, err, result)
		return err
	}
	if len(list) == 0 {
		result.Unresolved = append(result.Unresolved, ports.UnresolvedReference{
			Reference: ref,
			Reason:    ports.ResolveReasonNotFound,
		})
		return nil
	}
	for _, assetItem := range list {
		result.Resolved = append(result.Resolved, evidenceFromAsset(ref, assetItem))
	}
	return nil
}

// reportDBError records a per-reference DB-error entry AND lets the
// err bubble up the call chain. The dual write lets the caller
// surface partial success even when the batch is degraded.
func reportDBError(ref ports.ClipReference, err error, result *ports.ClipResolutionResult) {
	result.Unresolved = append(result.Unresolved, ports.UnresolvedReference{
		Reference: ref,
		Reason:    ports.ResolveReasonDBError,
	})
}

// evidenceFromAsset projects the media_assets.Asset row into the
// port-side ClipEvidence. Name / Filename / DriveLink come from
// canonical column getters; Description / Transcript come from
// metadata_json with both 'description' and
// {'transcript','clean_transcript'} fallback keys (matching the
// legacy clip_source_builder's read pattern — see
// usecase/clip_source_builder.go::BuildClipContext).
func evidenceFromAsset(ref ports.ClipReference, row *asset.Asset) ports.ClipEvidence {
	ev := ports.ClipEvidence{
		AssetID:        row.ID,
		ReferenceValue: ref.Value,
		ReferenceType:  ref.Type,
		Name:           row.Name,
		Filename:       row.Filename,
		DriveLink:      row.DriveLink(),
	}
	if len(row.Tags) > 0 {
		ev.Tags = append(ev.Tags, row.Tags...)
	}
	if md := row.Metadata; md != nil {
		if desc, ok := md["description"].(string); ok {
			ev.Description = desc
		}
		if t, ok := md["transcript"].(string); ok && t != "" {
			ev.TranscriptExcerpt = truncateString(t, 500)
		} else if ct, ok := md["clean_transcript"].(string); ok && ct != "" {
			ev.TranscriptExcerpt = truncateString(ct, 500)
		}
	}
	return ev
}

// truncateString is the canonical 500-char transcript excerpt
// (matches the clip_source_builder.BuildClipContext cap).
func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
