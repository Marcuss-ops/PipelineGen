// Package adapters — service.go holds the shared Service struct consumed by
// the extraction, manifest, segment, and intelligence files in this package.
//
// Phase 1b (June 2026): the original mega-package youtube.Service was moved to
// usecase/, but 6 files in adapters/ still reference a local *Service receiver
// for methods like saveManifest, processSegment, etc.
// This file defines the minimal struct those methods need so they compile
// without creating an adapters → usecase cycle (adapters already imports
// usecase for BuildClipMetadataInput and ExtractionCallbacks — no new cycle).
//
// Phase 1c closure (June 2026): the prior orchestration-fold marker
// referred to a structural refactor (fold the orchestration methods
// into ExtractionService in usecase/, delete this Service struct) —//	per godlike/07 §deprecations (id + owner + replacement + deadline),
//
//		the refactor is tracked as a follow-up in CHANGELOG.md under
//		`### Deferred` rather than as a source-code marker. Source-
//		code markers drift toward fake-availability when the ticket
//	 tracker goes stale; external tracking in CHANGELOG /
//	 architecture/current.yaml flips the polarity and forces a real
//	 gate. The 5 sibling receiver files (~1,798 LoC combined) remain
//
// untouched pending the fold. The Service struct itself continues
// to be valid — all 13 receivers on it are real implementations,
// not fake-availability stubs (per godlike/07).
package adapters

import (
	"context"
	"errors"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// Service is the shared state container consumed by the adapters-level
// extraction, manifest, segment, and intelligence methods. Every field
// is nil-safe: methods guard against nil receivers before access.
//
// Composition root (internal/app/composition.go) wires this struct once
// when constructing the Service used by the extraction pipeline.
// Fields are unexported because all accessors are methods in this package.
type Service struct {
	log           *zap.Logger
	cfg           youtubetypes.RuntimeConfig
	clips         youtubeports.ClipStorePort
	monitors      youtubeports.MonitorsStorePort
	folderMemory  youtubeports.FolderMemoryPort
	callbacks     usecase.ExtractionCallbacks
	cache         youtubeports.CachePort
	segmentsSvc   *usecase.SegmentsService
	videoPipeline youtubeports.VideoPipelinePort
	ollama        youtubeports.OllamaClientPort
	assetRepo     detail.Repository
}

// ServiceDeps is the PR1.6/1.7 constructor envelope for Service.
// Log is mandatory (composition root supplies a real logger).
// AssetRepo is required by dispatchOrIndex (the canonical
// detail.Repository.Upsert writer); passing a nil AssetRepo is a
// hard-fail at the first dispatchOrIndex call rather than a silent
// fall-through.
type ServiceDeps struct {
	Log       *zap.Logger
	AssetRepo detail.Repository
}

// NewService constructs a Service from a ServiceDeps envelope. Only
// the fields exercised by the current test surface (Log + AssetRepo)
// are wired; the remaining fields keep their zero values and the
// corresponding adapters methods stay nil-safe.
func NewService(deps ServiceDeps) *Service {
	return &Service{
		log:       deps.Log,
		assetRepo: deps.AssetRepo,
	}
}

// dispatchOrIndex is the canonical PR1.6 single-writer entry point for
// the YouTube extraction pipeline: it routes the supplied clip
// through detail.Repository.Upsert and returns the typed error when
// no AssetRepo is wired (composition-time contract: a missing writer
// is a hard fail, not a silent fall-through to the legacy
// outbox/clipsRepo paths). The hash argument is reserved for
// de-duplication of legacy callers and is currently unused; it stays
// in the signature so the dispatch surface remains stable.
func (s *Service) dispatchOrIndex(ctx context.Context, clip *asset.Asset, hash string) error {
	if s == nil {
		return errors.New("dispatchOrIndex: nil service")
	}
	if s.assetRepo == nil {
		return fmt.Errorf("dispatchOrIndex: AssetRepo not wired (clip_id=%q)", clip.ID)
	}
	return s.assetRepo.Upsert(ctx, clip)
}
