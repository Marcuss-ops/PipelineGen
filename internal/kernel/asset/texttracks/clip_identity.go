// Package asset — clip_identity.go is the canonical domain-level SSOT
// for the (asset_id, content_hash, index_event_key) triple that every
// pipeline (YouTube, Stock, Artlist, Voiceover) needs to mint when
// writing a new media asset row + outbox event.
//
// Problem this solves (July 2026):
//
//	YouTube constructs asset_id as `yt_{videoID}_{start}_{end}_{policy}`
//	(process_segment.go:208) and Stock constructs it as
//	`planner:{sha256-prefix}:{index}` (planner.go:buildClipPlan). Both
//	pipelines independently derive source_version (content hash) and
//	idempotency_key (outbox event key). The formats are different but
//	the CONTRACT is identical: a triple that locks the outbox dedup +
//	supersede gate.
//
// Design (godlike/06 SSOT — one canonical owner per fact):
//
//   - ClipIdentity is the immutable output container: AssetID +
//     ContentHash + IndexEventKey.
//   - NewYouTubeClipIdentity builds the YouTube-specific asset_id
//     format and derives the index event key from it.
//   - NewStockClipIdentity builds the Stock-specific asset_id format
//     and derives the index event key from it.
//   - BuildIndexEventKey is the canonical outbox event_key constructor.
//     The infra-layer `indexEventKey` in dispatcher_index.go currently
//     calls `clipindexer.EmbeddingModel()` etc. — those infra-layer
//     dependencies are passed in as parameters here so the domain
//     layer stays leaf-only (no internal/ imports).
//
// godlike/07 minimum-blast-radius: additive-only. Existing per-pipeline
// ID construction continues working; callers that adopt the builder
// deprecate their local derivation over time.
package texttracks

import (
	"errors"
	"fmt"
	"strings"
)

// ClipIdentity is the canonical triple that every asset-writing pipeline
// must produce. It locks:
//
//   - AssetID: the media_assets.id primary key (format varies by source).
//   - ContentHash: the SHA-256 fingerprint of the asset's content
//     (video bytes, searchable text hash, etc.). This is the
//     source_version that the IndexingHandler supersede gate uses.
//   - IndexEventKey: the outbox event_key for asset.index.requested.
//     Derived deterministically from (AssetID, ContentHash, model,
//     version, collection). The infra-layer's indexEventKey in
//     dispatcher_index.go currently computes this independently;
//     this domain SSOT is the canonical construction.
//
// All three fields are immutable after construction. Zero-value
// ClipIdentity is structurally valid but semantically incomplete —
// callers MUST check ErrClipIdentityEmptyAssetID before using.
type ClipIdentity struct {
	// AssetID is the canonical media_assets.id. Format varies by source:
	// YouTube: yt_{videoID}_{startSec}_{endSec}_{policyVer}
	// Stock:   planner:{sha256-prefix}:{index}
	AssetID string

	// ContentHash is the SHA-256 fingerprint of the asset's content.
	// For YouTube: SHA-256 of searchable text (title+summary+topics+...).
	// For Stock: SHA-256 of the video file bytes.
	// This value becomes the source_version in the outbox envelope and
	// the supersede gate's CAS fence. Empty = caller did not provide a
	// fingerprint (godlike/07 fail-closed: the supersede gate will
	// reject the event).
	ContentHash string

	// IndexEventKey is the deterministic outbox event_key for
	// asset.index.requested. Format: index:{assetID}:{contentHash}:
	// {model}:{version}:{collection}. Derived from the other two fields
	// plus the indexing metadata constants.
	IndexEventKey string
}

// ── Typed sentinels ──────────────────────────────────────────────────

// ErrClipIdentityEmptyAssetID signals that the caller passed an empty
// asset ID. The media_assets.id primary key is NOT NULL — an empty
// ID would violate the schema constraint.
var ErrClipIdentityEmptyAssetID = errors.New("clip identity: asset_id must not be empty")

// ErrClipIdentityEmptyContentHash signals that the caller passed an
// empty content hash. The supersede gate in IndexingHandler requires a
// non-empty source_version to function — an empty hash would allow
// stale events to pass through unchecked.
var ErrClipIdentityEmptyContentHash = errors.New("clip identity: content_hash must not be empty (supersede gate requires a fingerprint)")

// ErrClipIdentityInvalidTimestamps signals that endSec < startSec in
// a YouTube clip identity. A negative-duration clip ID would produce
// a misleading asset_id that doesn't match the actual clip window.
var ErrClipIdentityInvalidTimestamps = errors.New("clip identity: end_sec must be >= start_sec")

// ── YouTube builder ─────────────────────────────────────────────────

// YouTubeClipIdentityParams carries the inputs for NewYouTubeClipIdentity.
// Grouping them keeps the constructor signature under the archcheck
// 8-parameter limit while preserving the canonical asset_id format.
type YouTubeClipIdentityParams struct {
	VideoID     string
	StartSec    int
	EndSec      int
	PolicyVer   string
	ContentHash string
	Model       string
	Version     string
	Collection  string
}

// NewYouTubeClipIdentity constructs the canonical ClipIdentity for a
// YouTube clip. The asset_id format is the well-known
// `yt_{videoID}_{startSec}_{endSec}_{policyVer}` that has been in
// production since the YouTube pipeline launched.
//
// Parameters:
//   - params.VideoID: the YouTube video ID (11-char alphanumeric + - and _)
//   - params.StartSec / params.EndSec: the clip window boundaries (int, matching
//     the process_segment.go convention of int-cast seconds)
//   - params.PolicyVer: the policy version string (e.g. "v1")
//   - params.ContentHash: the SHA-256 fingerprint of the asset's searchable
//     content (title+summary+topics+...). Must be non-empty.
//   - params.Model / params.Version / params.Collection: the indexing metadata
//     from the clipindexer configuration. Callers pass these through from
//     their composition root (the domain layer cannot import clipindexer).
//
// Returns:
//   - ClipIdentity: the canonical triple
//   - error: ErrClipIdentityEmptyAssetID if videoID is empty,
//     ErrClipIdentityEmptyContentHash if contentHash is empty,
//     ErrClipIdentityInvalidTimestamps if endSec < startSec
//
// godlike/06 SSOT: this is the SOLE canonical owner of the
// yt_{videoID}_{start}_{end}_{policy} asset_id format. The
// process_segment.go inline `fmt.Sprintf` is the legacy surface;
// callers that adopt this builder deprecate the inline derivation.
func NewYouTubeClipIdentity(params YouTubeClipIdentityParams) (ClipIdentity, error) {
	videoID := strings.TrimSpace(params.VideoID)
	if videoID == "" {
		return ClipIdentity{}, ErrClipIdentityEmptyAssetID
	}
	contentHash := strings.TrimSpace(params.ContentHash)
	if contentHash == "" {
		return ClipIdentity{}, ErrClipIdentityEmptyContentHash
	}
	if params.EndSec < params.StartSec {
		return ClipIdentity{}, ErrClipIdentityInvalidTimestamps
	}
	policyVer := params.PolicyVer
	if policyVer == "" {
		policyVer = "v1"
	}
	assetID := fmt.Sprintf("yt_%s_%d_%d_%s", videoID, params.StartSec, params.EndSec, policyVer)
	eventKey := BuildIndexEventKey(assetID, contentHash, params.Model, params.Version, params.Collection)
	return ClipIdentity{
		AssetID:       assetID,
		ContentHash:   contentHash,
		IndexEventKey: eventKey,
	}, nil
}

// ── Stock builder ───────────────────────────────────────────────────

// NewStockClipIdentity constructs the canonical ClipIdentity for a
// Stock pipeline chunk. The asset_id format is the well-known
// `planner:{sha256-prefix}:{index}` that has been in production
// since the Stock Cutover (July 2026).
//
// Parameters:
//   - hashPrefix: the first 16 hex chars of SHA-256(URL|index|policy).
//     This is the deterministic OutputLogicalID prefix from the
//     planner's buildClipPlan function. Must be non-empty.
//   - index: the zero-based chunk index within the plan
//   - contentHash: the SHA-256 fingerprint of the video file bytes.
//     Must be non-empty.
//   - model / version / collection: the indexing metadata from the
//     clipindexer configuration.
//
// Returns:
//   - ClipIdentity: the canonical triple
//   - error: ErrClipIdentityEmptyAssetID if hashPrefix is empty,
//     ErrClipIdentityEmptyContentHash if contentHash is empty
//
// godlike/06 SSOT: this is the SOLE canonical owner of the
// planner:{hash}:{index} asset_id format. The planner.go inline
// `fmt.Sprintf` is the legacy surface; callers that adopt this
// builder deprecate the inline derivation.
func NewStockClipIdentity(
	hashPrefix string,
	index int,
	contentHash string,
	model, version, collection string,
) (ClipIdentity, error) {
	hashPrefix = strings.TrimSpace(hashPrefix)
	if hashPrefix == "" {
		return ClipIdentity{}, ErrClipIdentityEmptyAssetID
	}
	contentHash = strings.TrimSpace(contentHash)
	if contentHash == "" {
		return ClipIdentity{}, ErrClipIdentityEmptyContentHash
	}
	assetID := fmt.Sprintf("planner:%s:%d", hashPrefix, index)
	eventKey := BuildIndexEventKey(assetID, contentHash, model, version, collection)
	return ClipIdentity{
		AssetID:       assetID,
		ContentHash:   contentHash,
		IndexEventKey: eventKey,
	}, nil
}

// ── Generic builder ─────────────────────────────────────────────────

// NewClipIdentity constructs a ClipIdentity for any source type when
// the caller already has a pre-computed asset_id. This is the escape
// hatch for sources that don't use the YouTube or Stock ID formats
// (e.g. Artlist uses `artlist_{hash}`; Voiceover uses its own format).
//
// Parameters:
//   - assetID: the pre-computed media_assets.id. Must be non-empty.
//   - contentHash: the SHA-256 fingerprint. Must be non-empty.
//   - model / version / collection: the indexing metadata.
//
// WARNING: callers that already have a pre-computed asset_id in the
// YouTube (yt_) or Stock (planner:) format MUST prefer
// NewYouTubeClipIdentity / NewStockClipIdentity over this generic
// builder — the format-specific builders enforce source-specific
// invariants (e.g. endSec >= startSec for YouTube) that this
// function does not check.
//
// godlike/06 SSOT: callers that use this function own their own
// asset_id format. The domain layer only enforces the non-empty
// invariants and derives the index event key.
func NewClipIdentity(
	assetID string,
	contentHash string,
	model, version, collection string,
) (ClipIdentity, error) {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return ClipIdentity{}, ErrClipIdentityEmptyAssetID
	}
	contentHash = strings.TrimSpace(contentHash)
	if contentHash == "" {
		return ClipIdentity{}, ErrClipIdentityEmptyContentHash
	}
	eventKey := BuildIndexEventKey(assetID, contentHash, model, version, collection)
	return ClipIdentity{
		AssetID:       assetID,
		ContentHash:   contentHash,
		IndexEventKey: eventKey,
	}, nil
}

// ── Index event key construction ────────────────────────────────────

// BuildIndexEventKey is the canonical outbox event_key constructor for
// asset.index.requested.v1 envelopes. It produces a deterministic
// colon-separated string that the IndexingHandler's supersede gate
// uses to deduplicate re-index requests.
//
// Format: index:{assetID}:{contentHash}:{model}:{version}:{collection}
//
// This is the domain-level SSOT for the key format. The infra-layer's
// `indexEventKey` in dispatcher_index.go currently calls
// `clipindexer.EmbeddingModel()` etc. directly; callers of this
// function pass those values through from their composition root.
//
// godlike/06 SSOT: the infra-layer `indexEventKey` should eventually
// delegate to this function (or to a thin wrapper that injects the
// clipindexer constants). This is the forward-pointer that preserves
// the domain-level ownership of the format.
func BuildIndexEventKey(assetID, contentHash, model, version, collection string) string {
	return fmt.Sprintf("index:%s:%s:%s:%s:%s",
		assetID,
		contentHash,
		model,
		version,
		collection,
	)
}
