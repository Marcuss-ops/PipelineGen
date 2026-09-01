package entities

import (
	"context"
	"fmt"
	"strings"

	assetpersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	assetkernel "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// EntityAssetRequest is the stable identity sent to the asset resolver. The
// canonical id is derived when omitted; callers never manufacture an id from
// a URL or provider-specific key.
type EntityAssetRequest struct {
	EntityType    string
	CanonicalName string
	CanonicalID   string
}

// EntityAssetSource is the provider fallback seam. Provider code returns a
// verified content-addressed EntityAsset; it does not write SQLite, Drive or
// Qdrant itself.
type EntityAssetSource interface {
	Acquire(context.Context, EntityAssetRequest) (EntityAsset, error)
}

// EntityAssetCommitPort is deliberately narrower than the full persistence
// service while still pointing at the canonical AssetCommitter implementation.
type EntityAssetCommitPort interface {
	CommitAsset(context.Context, assetpersistence.AssetCommitRequest) (assetpersistence.CommittedAsset, error)
}

// EntityAssetResolver implements local-first entity media resolution with a
// single provider fallback and an atomic canonical commit. It owns no storage
// schema: EntityMediaResolver remains the local index and AssetCommitter owns
// SQLite/outbox persistence.
type EntityAssetResolver struct {
	local     *EntityMediaResolver
	source    EntityAssetSource
	committer EntityAssetCommitPort
}

func NewEntityAssetResolver(local *EntityMediaResolver, source EntityAssetSource, committer EntityAssetCommitPort) (*EntityAssetResolver, error) {
	if local == nil {
		return nil, fmt.Errorf("entity asset resolver: local resolver is required")
	}
	if source == nil {
		return nil, fmt.Errorf("entity asset resolver: fallback source is required")
	}
	if committer == nil {
		return nil, fmt.Errorf("entity asset resolver: canonical committer is required")
	}
	return &EntityAssetResolver{local: local, source: source, committer: committer}, nil
}

func (r *EntityAssetResolver) Resolve(ctx context.Context, req EntityAssetRequest) (capabilityoverlay.BoundAsset, error) {
	if r == nil || r.local == nil {
		return capabilityoverlay.BoundAsset{}, fmt.Errorf("entity asset resolver: not configured")
	}
	canonicalID := strings.TrimSpace(req.CanonicalID)
	if canonicalID == "" {
		canonicalID = CanonicalEntityID(req.EntityType, req.CanonicalName)
	}
	if canonicalID == "" {
		return capabilityoverlay.BoundAsset{}, fmt.Errorf("entity asset resolver: empty canonical entity identity")
	}
	if ref, err := r.local.ResolveBest(canonicalID); err == nil {
		return capabilityoverlay.BoundAsset{EntityID: canonicalID, AssetID: ref.AssetID, ContentHash: ref.SHA256, SourceURL: ref.URL, Verified: true}, nil
	}
	acquired, err := r.source.Acquire(ctx, EntityAssetRequest{EntityType: req.EntityType, CanonicalName: req.CanonicalName, CanonicalID: canonicalID})
	if err != nil {
		return capabilityoverlay.BoundAsset{}, fmt.Errorf("entity asset resolver: acquire %q: %w", canonicalID, err)
	}
	acquired.EntityID = canonicalID
	if err := acquired.Validate(); err != nil {
		return capabilityoverlay.BoundAsset{}, fmt.Errorf("entity asset resolver: acquired asset: %w", err)
	}
	lifecycle, indexState := assetkernel.NewIndexableAssetState()
	if _, err := r.committer.CommitAsset(ctx, assetpersistence.AssetCommitRequest{
		AssetID: acquired.AssetID, Source: acquired.Source, Name: req.CanonicalName,
		Filename: acquired.AssetID, MediaType: mediaTypeFor(acquired.AssetType),
		ContentHash: acquired.SHA256, SourceURL: acquired.StorageURL,
		LifecycleState: string(lifecycle), IndexState: string(indexState),
	}); err != nil {
		return capabilityoverlay.BoundAsset{}, fmt.Errorf("entity asset resolver: commit %q: %w", canonicalID, err)
	}
	if err := r.local.index.IndexForCanonicalID(canonicalID, acquired); err != nil {
		return capabilityoverlay.BoundAsset{}, fmt.Errorf("entity asset resolver: local index: %w", err)
	}
	return capabilityoverlay.BoundAsset{EntityID: canonicalID, AssetID: acquired.AssetID, ContentHash: acquired.SHA256, DriveFileID: acquired.DriveFileID, Width: acquired.Width, Height: acquired.Height, SourceURL: acquired.StorageURL, Verified: true}, nil
}
