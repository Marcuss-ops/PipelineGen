// Package mediacommit defines the canonical MediaCommitter contract.
//
// MediaCommitter is the SINGLE transactional commit gate for media assets.
// Every producer (YouTube, Artlist, Stock, Drive/Images, AI, Voiceover,
// Audio, …) must converge on CommitMediaAsset instead of writing its own
// slightly-different media_assets row. The commit runs the full canonical
// sequence inside ONE SQLite transaction:
//
//  1. resolve canonical identity (asset existence → Created, source id)
//  2. upsert media_assets (+ asset_locations + typed metadata + taxonomy
//     columns namespace / asset_kind / source_type / semantic_role)
//  3. RegisterSource (media_asset_sources)
//  4. LinkContent (content_sha256) when the bytes are known
//  5. upsert transcript/text tracks (asset_text_tracks)
//  6. append registry event (registry_events) → registry seq
//  7. write asset.index.requested outbox event when indexable
//
// godlike/06 SSOT: after cutover, no other package may INSERT/UPSERT
// media_assets, media_asset_sources, or the asset.index.requested outbox
// event directly. Legacy writers keep their surface but delegate here.
package mediacommit

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// AssetDraft is the asset shape the MediaCommitter persists. It carries the
// fields needed to write media_assets, asset_locations, and the typed
// metadata_json envelope.
type AssetDraft struct {
	AssetID        string
	Source         string // source_type: youtube | artlist | stock | drive | image | voiceover | ai | audio
	Name           string
	Filename       string
	MediaType      string
	Category       string
	DurationMs     int64
	ContentHash    string
	Description    string
	SearchText     string
	LifecycleState string
	IndexState     string
	LocalPath      string
	FolderID       string
	FolderPath     string
	ThumbnailURL   string
	SourceURL      string
	Title          string
	SourceProvider string
	SourceVideoID  string
	StartMs        int64
	EndMs          int64
	Metadata       asset.TypedMetadata
	Locations      []asset.LocationCommit
	Image          *ImageDraft
}

// ImageDraft preserves image-specific media_assets columns while the common
// asset transaction remains owned by MediaCommitter.
type ImageDraft struct {
	URL          string
	TagsJSON     string
	TagsNorm     string
	Width        int
	Height       int
	RelativePath string
	Origin       string
	Provider     string
}

// AssetSourceDraft is the provenance record written to media_asset_sources.
// Source identity is metadata, never identity — content identity comes from
// ContentIdentity.ContentSHA256 (CAS invariant).
type AssetSourceDraft struct {
	SourceType    string // drive | youtube | artlist | manual | url | stock | image | voiceover
	SourceURI     string
	SourceVersion string // etag / provider version / modified_time
	IsPrimary     bool
}

// ContentIdentity identifies the physical bytes (content_sha256). When set,
// the committer links the asset to its content object (step 4).
type ContentIdentity struct {
	ContentSHA256 string
}

// TextTrack is one text track (transcript / description / summary) upserted
// to asset_text_tracks (step 6).
type TextTrack struct {
	LanguageCode string
	TextKind     string // transcript | description | summary
	TextContent  string
	SourceType   string // provided | translation | ai | ...
}

// IndexPolicy decides whether the asset is semantic-searchable and with what
// outbox priority. Registered ≠ indexable: e.g. voiceover and final_audio are
// REGISTERED but not necessarily SEARCHABLE.
type IndexPolicy struct {
	Indexable bool
	Priority  int
}

// CommitMediaAssetRequest is the canonical input for the MediaCommitter.
type CommitMediaAssetRequest struct {
	Asset       AssetDraft
	Source      AssetSourceDraft
	Taxonomy    mediaregistry.AssetTaxonomy
	Content     *ContentIdentity
	TextTracks  []TextTrack
	IndexPolicy IndexPolicy

	// Actor and RunID stamp the registry event (step 7).
	Actor string
	RunID string
}

// CommitMediaAssetResult is the canonical outcome.
type CommitMediaAssetResult struct {
	AssetID              string
	Created              bool
	SourceID             string
	ContentSHA256        string
	RegistrySeq          int64
	OutboxEventKey       string
	OutboxInserted       bool
	OutboxExistingStatus string
}

// MediaCommitter is the single transactional commit gate for media assets.
type MediaCommitter interface {
	CommitMediaAsset(ctx context.Context, req CommitMediaAssetRequest) (CommitMediaAssetResult, error)
}

// ── Validation ─────────────────────────────────────────────────────────

var (
	ErrAssetIDRequired       = errors.New("media commit: Asset.AssetID is required")
	ErrSourceRequired        = errors.New("media commit: Asset.Source is required")
	ErrFilenameRequired      = errors.New("media commit: Asset.Filename is required")
	ErrMediaTypeRequired     = errors.New("media commit: Asset.MediaType is required")
	ErrLifecycleRequired     = errors.New("media commit: Asset.LifecycleState is required")
	ErrTaxonomyInvalid       = errors.New("media commit: Taxonomy is invalid")
	ErrIndexTaxonomyRequired = errors.New("media commit: indexable assets require complete valid taxonomy")
)

// Validate performs the pre-flight validation shared by all adapters.
func (r CommitMediaAssetRequest) Validate() error {
	if r.Asset.AssetID == "" {
		return ErrAssetIDRequired
	}
	if r.Asset.Source == "" {
		return ErrSourceRequired
	}
	if r.Asset.Filename == "" {
		return ErrFilenameRequired
	}
	if r.Asset.MediaType == "" {
		return ErrMediaTypeRequired
	}
	if r.Asset.LifecycleState == "" {
		return ErrLifecycleRequired
	}
	if r.Taxonomy.AssetID != "" && r.Taxonomy.AssetID != r.Asset.AssetID {
		return errors.New("media commit: Taxonomy.AssetID must be empty or match Asset.AssetID")
	}
	if r.IndexPolicy.Indexable {
		taxonomy := r.Taxonomy
		if taxonomy.AssetID == "" {
			taxonomy.AssetID = r.Asset.AssetID
		}
		if taxonomy.IsZero() || taxonomy.Namespace == "" || taxonomy.SourceType == "" || taxonomy.MediaType == "" || taxonomy.AssetKind == "" || taxonomy.MediaType != mediaregistry.MediaType(r.Asset.MediaType) {
			return ErrIndexTaxonomyRequired
		}
		if err := taxonomy.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrIndexTaxonomyRequired, err)
		}
	}
	return nil
}
