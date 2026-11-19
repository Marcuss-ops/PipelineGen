// Package asset — clip_identity.go is the canonical domain-level SSOT
// for the (asset_id, content_hash, index_event_key) triple that every
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
//   - YouTubeClipAssetID is the single owner of the YouTube asset_id
//     format `yt_{videoID}_{start}_{end}_{policy}`.
//
// The per-source identity builders and the outbox index-event key
// constructor were retired with the clip indexer on 2026-11-17; callers
// build ClipIdentity from their own source row and the media plane owns
// the index event. The container stays because live ingestion and the
// PostgreSQL media tests still carry the triple.
package detail

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

// DefaultYouTubeClipPolicyVersion is the canonical fallback policy version
// for a YouTube clip identity. It matches the extraction pipeline's
// usecase.ProcessSegmentPolicyVersion ("v1") so every ingest path that has
// no explicit policy produces the SAME asset id for the same window.
const DefaultYouTubeClipPolicyVersion = "v1"

// YouTubeClipAssetID builds the canonical YouTube clip asset id
// `yt_<videoID>_<startSec>_<endSec>_<policyVersion>`.
//
// godlike/06 SSOT: this function is the SOLE owner of the YouTube asset-id
// FORMAT. Both ingest paths call it instead of re-deriving the string:
//
//   - the canonical extraction path (youtube/usecase/process_segment_step1.go)
//   - the sourcing registration path
//     (assets/sourcing/youtube/usecase/download_hash.go::deriveClipID)
//
// Keeping ONE owner is what makes the identity — and therefore the
// media_assets primary key — converge across paths: two ingest routes that
// compute `yt_<videoID>_<start>_<end>_<policy>` for the same window upsert the
// SAME row instead of creating a second asset for the same clip.
//
// The identity is a pure function of (videoID, window, policyVersion) and
// deliberately does NOT include any content hash: the bytes are the CONTENT
// identity (media_assets.content_sha256 / content_hash + the supersede gate),
// never the logical clip identity. A re-cut with different bytes keeps the same
// asset id and is handled by the index-event supersede gate.
//
// policyVersion == "" falls back to DefaultYouTubeClipPolicyVersion.
func YouTubeClipAssetID(videoID string, startSec, endSec int, policyVersion string) (string, error) {
	trimmed := strings.TrimSpace(videoID)
	if trimmed == "" {
		return "", ErrClipIdentityEmptyAssetID
	}
	if endSec < startSec {
		return "", ErrClipIdentityInvalidTimestamps
	}
	policyVer := strings.TrimSpace(policyVersion)
	if policyVer == "" {
		policyVer = DefaultYouTubeClipPolicyVersion
	}
	return fmt.Sprintf("yt_%s_%d_%d_%s", trimmed, startSec, endSec, policyVer), nil
}

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

// ErrYouTubeClipAssetIDShape signals an asset id that does not match the
// canonical `yt_{videoID}_{startSec}_{endSec}_{policyVer}` contract.
var ErrYouTubeClipAssetIDShape = errors.New("clip identity: asset_id does not match yt_{videoID}_{startSec}_{endSec}_{policyVer}")

// ── Stock builder ───────────────────────────────────────────────────

// ── Generic builder ─────────────────────────────────────────────────

// ── Index event key construction ────────────────────────────────────
