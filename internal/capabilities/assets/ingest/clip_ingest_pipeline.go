// Package ingest — clip_ingest_pipeline.go (PR-CLIPINGEST-PIPELINE, July 2026).
//
// godlike/06 SSOT — one canonical owner per fact:
//
//   - "What are the canonical 9 components every clip ingest pipeline MUST
//     declare?" lives here.
//   - The ClipIngestPipeline struct instance below is the SINGLE canonical
//     owner of the 9-component wiring. Any code path that needs a typed
//     pipeline MUST consume the typed ports declared in this file — never
//     re-declare them.
//
// godlike/07 fail-closed contract: NewClipIngestPipeline rejects any nil
// component with a typed ErrClipIngestPipelineFailClosed sentinel +
// failing component name. Ingest rejects empty SourceURL with
// ErrClipIngestPipelineSourceRefEmpty. Component errors are wrapped
// via %w so callers can errors.Is probe the canonical sentinels.
//
// The 9 components (user spec verbatim):
//
//  1. Downloader            — source bytes (production: YouTubeStager /
//     ArtlistStager / StockStager).
//  2. MediaNormalizer       — canonical container/codec normalization.
//  3. ContentHasher         — canonical SHA-256 (AssetCommitter
//     supersede-gate fingerprint).
//  4. ArtifactStore         — typed-narrow companion writer for
//     locations/processing.
//  5. Transcriber           — audio transcript (WhisperTranscriberPort).
//  6. ClipEnricher          — semantic + visual metadata attachment.
//  7. TextTrackTranslator  — localize source-language text tracks.
//  8. SearchTextComposer    — BM25 + vector-search text envelope.
//  9. AssetCommitter — canonical SSOT atomic media_assets +
//     outbox_events write surface (QDRANT-002).
//
// All 3 providers (YouTube / Artlist / Stock) MUST flow through the
// SAME persistence.AssetCommitter instance (composition-root enforces).
//
// State-traversal mapping (per PR-CATALOG-MULTILINGUA step 7's canonical
// 14-state ASSET STATE MACHINES — see internal/domain/asset):
//
//	DISCOVERED → DOWNLOADED → NORMALIZED → HASHED → UPLOADED →
//	TRANSCRIBED → ENRICHED → TRANSLATED → INDEX_PENDING → INDEXED →
//	READY → READY_MULTILINGUAL
//
// The Ingest method walks the first 8 stages. The remaining transitions
// (INDEX_PENDING → INDEXED, INDEXED → READY, READY → READY_MULTILINGUAL)
// are out of pipeline scope (outbox worker, readiness gate, dashboard
// process them asynchronously).
package ingest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	ports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Port interfaces (godlike/06 SSOT) ────────────────────────────────────────

// Downloader downloads source bytes for a clip asset. Production
// candidates: existing YouTubeStager / ArtlistStager / StockStager —
// each implements ports.SourceStager whose StageSource signature
// matches Downloader.Download.
type Downloader interface {
	Download(ctx context.Context, ref ports.SourceRef) (*ports.StagedAsset, error)
}

// MediaNormalizer normalizes a staged source asset into the canonical
// media container / codec (e.g., MP4 / H.264 / AAC).
type MediaNormalizer interface {
	Normalize(ctx context.Context, staged *ports.StagedAsset) (NormalizedMedia, error)
}

// ContentHasher computes the canonical content fingerprint (SHA-256).
type ContentHasher interface {
	Hash(ctx context.Context, media NormalizedMedia) (ContentHashResult, error)
}

// ArtifactStore persists a freshly-staged asset's metadata + locations
// (companion typed-narrow writer for asset_locations / asset_renditions
// — NOT the atomic media_assets write).
type ArtifactStore interface {
	StoreArtifact(ctx context.Context, rec *artifacts.MediaRecord) error
}

// Transcriber renders an audio transcript from a normalized asset.
// Production wire-up: youtubeports.WhisperTranscriberPort +
// internal/infrastructure/youtube/whisper_transcriber.go via the
// application-layer facade.
type Transcriber interface {
	Transcribe(ctx context.Context, media NormalizedMedia) (asset.TranscriptResult, error)
}

// ClipEnricher attaches semantic + visual metadata to a freshly-stored
// asset.
type ClipEnricher interface {
	Enrich(ctx context.Context, assetID string, media NormalizedMedia, transcript asset.TranscriptResult) error
}

// TextTrackTranslator localizes the source-language text tracks into
// registered target languages.
type TextTrackTranslator interface {
	Translate(ctx context.Context, assetID string, transcript asset.TranscriptResult) ([]asset.TextTrack, error)
}

// SearchTextComposer composes the BM25 + vector-search text envelope.
type SearchTextComposer interface {
	Compose(ctx context.Context, ctxBag ClipIngestContext) (string, error)
}

// ── Canonical value types (godlike/06 SSOT) ─────────────────────────────────

// NormalizedMedia is the post-normalize artifact produced by
// MediaNormalizer and consumed by ContentHasher + Transcriber.
type NormalizedMedia struct {
	LocalPath string
	Bytes     int64
	Container string
	Codec     string
	Width     int
	Height    int
	Duration  int64 // milliseconds
}

// ContentHashResult is the post-hash artifact produced by ContentHasher.
type ContentHashResult struct {
	ContentHash string
	Algorithm   string // e.g. "sha256"
	Bytes       int64
}

// ClipIngestContext bundles the in-pipeline inputs the SearchTextComposer
// needs to compose the canonical search text.
type ClipIngestContext struct {
	AssetID             string
	Source              string
	NormalizedMedia     NormalizedMedia
	ContentHash         string
	Transcript          asset.TranscriptResult
	EnrichmentArtifacts map[string]any
	Translations        []asset.TextTrack
}

// ClipIngestResult is the Ingest return shape. FinalState is one of the
// 14 canonical ASSET STATE values from internal/domain/asset.
type ClipIngestResult struct {
	OK         bool
	AssetID    string
	Source     string
	FinalState asset.AssetState
	SearchText string
}

// ── Typed sentinels (godlike/07 fail-closed) ─────────────────────────────────

// ErrClipIngestPipelineFailClosed is returned when ANY of the 9
// components is nil. The message includes the failing component name.
var ErrClipIngestPipelineFailClosed = errors.New("clip_ingest_pipeline: nil component")

// ErrClipIngestPipelineSourceRefEmpty is returned when Ingest receives
// a SourceRef whose URL is empty.
var ErrClipIngestPipelineSourceRefEmpty = errors.New("clip_ingest_pipeline: source ref URL is empty")

// ── The canonical struct (godlike/06 SSOT) ───────────────────────────────────

// ClipIngestPipeline is the canonically-named 9-component pipeline
// (godlike/06 SSOT — single canonical owner per fact).
type ClipIngestPipeline struct {
	Downloader          Downloader
	MediaNormalizer     MediaNormalizer
	ContentHasher       ContentHasher
	ArtifactStore       ArtifactStore
	Transcriber         Transcriber
	ClipEnricher        ClipEnricher
	TextTrackTranslator TextTrackTranslator
	SearchTextComposer  SearchTextComposer
	AssetCommitter      persistence.AssetCommitter
}

// MediaProcessingDeps bundles the first 4 clip-ingest pipeline ports
// so ClipIngestPipelineDeps stays under the archcheck 8-field cap.
type MediaProcessingDeps struct {
	Downloader      Downloader
	MediaNormalizer MediaNormalizer
	ContentHasher   ContentHasher
	ArtifactStore   ArtifactStore
}

// EnrichmentDeps bundles the enrichment/translation ports of the
// clip-ingest pipeline.
type EnrichmentDeps struct {
	Transcriber         Transcriber
	ClipEnricher        ClipEnricher
	TextTrackTranslator TextTrackTranslator
	SearchTextComposer  SearchTextComposer
}

// ClipIngestPipelineDeps mirrors the 9 fields for the constructor
// (single positional arg keeps max_constructor_deps: 8 satisfied).
type ClipIngestPipelineDeps struct {
	MediaProcessing MediaProcessingDeps
	Enrichment      EnrichmentDeps
	AssetCommitter  persistence.AssetCommitter
	Log             *zap.Logger
}

// depsValidate fails fast if any of the 9 components is nil.
func depsValidate(d ClipIngestPipelineDeps) error {
	if d.MediaProcessing.Downloader == nil {
		return fmt.Errorf("%w: Downloader", ErrClipIngestPipelineFailClosed)
	}
	if d.MediaProcessing.MediaNormalizer == nil {
		return fmt.Errorf("%w: MediaNormalizer", ErrClipIngestPipelineFailClosed)
	}
	if d.MediaProcessing.ContentHasher == nil {
		return fmt.Errorf("%w: ContentHasher", ErrClipIngestPipelineFailClosed)
	}
	if d.MediaProcessing.ArtifactStore == nil {
		return fmt.Errorf("%w: ArtifactStore", ErrClipIngestPipelineFailClosed)
	}
	if d.Enrichment.Transcriber == nil {
		return fmt.Errorf("%w: Transcriber", ErrClipIngestPipelineFailClosed)
	}
	if d.Enrichment.ClipEnricher == nil {
		return fmt.Errorf("%w: ClipEnricher", ErrClipIngestPipelineFailClosed)
	}
	if d.Enrichment.TextTrackTranslator == nil {
		return fmt.Errorf("%w: TextTrackTranslator", ErrClipIngestPipelineFailClosed)
	}
	if d.Enrichment.SearchTextComposer == nil {
		return fmt.Errorf("%w: SearchTextComposer", ErrClipIngestPipelineFailClosed)
	}
	if d.AssetCommitter == nil {
		return fmt.Errorf("%w: AssetCommitter", ErrClipIngestPipelineFailClosed)
	}
	return nil
}

// NewClipIngestPipeline constructs the canonical ClipIngestPipeline.
// godlike/07 fail-closed: any nil component surfaces a typed error so
// composition-root mistakes do NOT propagate to runtime.
func NewClipIngestPipeline(deps ClipIngestPipelineDeps) (*ClipIngestPipeline, error) {
	if err := depsValidate(deps); err != nil {
		return nil, err
	}
	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipIngestPipeline{
		Downloader:          deps.MediaProcessing.Downloader,
		MediaNormalizer:     deps.MediaProcessing.MediaNormalizer,
		ContentHasher:       deps.MediaProcessing.ContentHasher,
		ArtifactStore:       deps.MediaProcessing.ArtifactStore,
		Transcriber:         deps.Enrichment.Transcriber,
		ClipEnricher:        deps.Enrichment.ClipEnricher,
		TextTrackTranslator: deps.Enrichment.TextTrackTranslator,
		SearchTextComposer:  deps.Enrichment.SearchTextComposer,
		AssetCommitter:      deps.AssetCommitter,
	}, nil
}

// Ingest walks the 8 in-pipeline stages (DISCOVERED → TRANSLATED →
// INDEX_PENDING side-effect). Final-state in the result is
// StateAssetIndexed (post-compose; outbox handler advances to READY
// asynchronously).
//
// godlike/07 fail-closed contract: empty SourceURL rejected with
// ErrClipIngestPipelineSourceRefEmpty. nil receiver rejected with
// ErrClipIngestPipelineFailClosed. Component errors wrapped via %w.
func (p *ClipIngestPipeline) Ingest(ctx context.Context, ref ports.SourceRef) (*ClipIngestResult, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: nil receiver", ErrClipIngestPipelineFailClosed)
	}
	if ref.URL == "" {
		return nil, fmt.Errorf("%w: source ref URL is empty", ErrClipIngestPipelineSourceRefEmpty)
	}

	// Stage 1 — Downloader.
	staged, err := p.Downloader.Download(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("clip_ingest_pipeline.Downloader.Download: %w", err)
	}

	// Stage 2 — MediaNormalizer.
	normalized, err := p.MediaNormalizer.Normalize(ctx, staged)
	if err != nil {
		return nil, fmt.Errorf("clip_ingest_pipeline.MediaNormalizer.Normalize: %w", err)
	}

	// Stage 3 — ContentHasher.
	hash, err := p.ContentHasher.Hash(ctx, normalized)
	if err != nil {
		return nil, fmt.Errorf("clip_ingest_pipeline.ContentHasher.Hash: %w", err)
	}

	// Stage 4a — ArtifactStore (typed-narrow companion write).
	if err := p.ArtifactStore.StoreArtifact(ctx, &artifacts.MediaRecord{
		LegacyFileMD5: hash.ContentHash,
		LocalPath:     normalized.LocalPath,
	}); err != nil {
		return nil, fmt.Errorf("clip_ingest_pipeline.ArtifactStore.StoreArtifact: %w", err)
	}

	// Stage 4b — Transcriber.
	transcript, err := p.Transcriber.Transcribe(ctx, normalized)
	if err != nil {
		return nil, fmt.Errorf("clip_ingest_pipeline.Transcriber.Transcribe: %w", err)
	}

	// Stage 5 — AssetCommitter (QDRANT-002 invariant: atomic
	// media_assets UPSERT + outbox_events INSERT in single tx).
	if _, err := p.AssetCommitter.CommitAndIndex(ctx, persistence.CommitRequest{
		AssetID:        hash.ContentHash,
		Source:         ref.URL,
		Filename:       filepath.Base(normalized.LocalPath),
		MediaType:      "video",
		ContentHash:    hash.ContentHash,
		LifecycleState: "DISCOVERED",
		IndexState:     "INDEX_PENDING",
		EmitIndexEvent: true,
	}); err != nil {
		return nil, fmt.Errorf("clip_ingest_pipeline.AssetCommitter.CommitAndIndex: %w", err)
	}

	assetID := hash.ContentHash

	// Stage 6 — ClipEnricher.
	if err := p.ClipEnricher.Enrich(ctx, assetID, normalized, transcript); err != nil {
		return nil, fmt.Errorf("clip_ingest_pipeline.ClipEnricher.Enrich: %w", err)
	}

	// Stage 7 — TextTrackTranslator.
	translations, err := p.TextTrackTranslator.Translate(ctx, assetID, transcript)
	if err != nil {
		return nil, fmt.Errorf("clip_ingest_pipeline.TextTrackTranslator.Translate: %w", err)
	}

	// Stage 8 — SearchTextComposer.
	ctxBag := ClipIngestContext{
		AssetID:         assetID,
		Source:          ref.URL,
		NormalizedMedia: normalized,
		ContentHash:     hash.ContentHash,
		Transcript:      transcript,
		Translations:    translations,
	}
	searchText, err := p.SearchTextComposer.Compose(ctx, ctxBag)
	if err != nil {
		return nil, fmt.Errorf("clip_ingest_pipeline.SearchTextComposer.Compose: %w", err)
	}

	return &ClipIngestResult{
		OK:         true,
		AssetID:    assetID,
		Source:     ref.URL,
		FinalState: asset.StateAssetIndexed,
		SearchText: searchText,
	}, nil
}
