// Package artlist — semantic_enricher.go is the slim orchestrator for the
// 3-file split of the pre-PR 509 LoC semantic_enricher.go (LONG-FILES-DECOMPOSITION-V2-2026-07-06 P3 BASSA, July 2026).
//
// File layout (per godlike/06 one-canonical-owner-per-fact):
//
//   - semantic_enricher.go          (this file) — slim orchestrator: SemanticEnricher struct + NewSemanticEnricher + dispatchOrIndexAndUpsert + newDispatchBridge + acquireEnrichFolderLock (per-folder keyed lock registry used by Enrich in semantic_enricher_enrich.go)
//   - semantic_enricher_enrich.go   (sibling)   — Enrich entry-point + buildArtlistPrompt + deduplicateStrings pure helpers
//   - semantic_enricher_metadata.go (sibling)   — updateCumulativeMetadataJSON (the RMW path for cumulative Drive metadata.json sync)
//
// All cross-file symbol resolution works via same-package scope visibility.
// Pure code-motion split — zero behavior change, zero new exported symbols.
package artlist

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	searchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// enrichFolderLocks serialises the cumulative metadata.json RMW
// (read-modify-write) cycle PER Drive folder across concurrent Enrich
// invocations. The former package-level enrichMetaMu serialised ALL
// folders under one global mutex: a slow Drive round-trip for one folder
// blocked semantic enrichment for every other folder. The per-folder
// critical section keeps the same-folder semantics (one folder is never
// read-modify-written concurrently) while unrelated folders proceed in
// parallel.
//
// LOCK-CONSOLIDATION (September 2026, P3 cleanup): the reference-counted
// per-key registry implementation now lives in the canonical
// pkg/concurrent.KeyedLocker primitive (godlike/06 one-owner-per-fact —
// no private per-key registry copies). The package-scope registry here is
// intentional and MUST stay package-scope: the guarded RMW spans
// goroutines that may outlive a single SemanticEnricher instance —
// multiple enrichers may point at the same Drive folder.
var enrichFolderLocks = concurrent.NewKeyedLocker()

// acquireEnrichFolderLock returns a release func for an exclusive
// per-key lock. The caller MUST invoke the release func exactly once when
// the guarded RMW cycle completes. Delegates to the canonical
// KeyedLocker (same same-key-serialises / cross-key-parallel contract,
// pinned by both TestAcquireEnrichFolderLock_* and
// TestKeyedLocker_*).
func acquireEnrichFolderLock(key string) func() {
	return enrichFolderLocks.Lock(key)
}

// SemanticEnricher arricchisce un clip Artlist con metadati semantici.
// Usa il semantic_tagger.py per generare search_text, concept_tags, subjects, mood,
// e un embedding compatto (concept_tags serializzati come JSON) per la ricerca ibrida.
//
// L'enrichment viene eseguito in background dopo il salvataggio iniziale del clip,
// quindi non blocca mai la pipeline principale di download.
//
// F2.11 (June 2026): the driveManager (DriveFolderManager) field was
// RETIRED entirely (override brutal). The metadata.json read-modify-
// write path in updateCumulativeMetadataJSON is now backed by:
//
//   - drive.Reader for ListByQuery (mapped to SearchFiles) +
//     Download (mapped to DownloadFile). The canonical Pattern 0
//     Reader port in internal/platform/drive/ports.go owns
//     the read surface.
//
//   - delivery.Publisher.Publish for the upload of the regenerated
//     metadata.json. The Publisher owns the write surface.
//
//   - drive.FileLifecycle for Trash on the previous metadata.json
//     (CAR-D3 split out from DriveFolderManager in PR2.7; preserved
//     unchanged). The implementation in *FileLifecycleAdapter still
//     wraps the raw *driveapi.Service so the SDK is hidden.
//
// PR2.5: dispatcher is now a constructor argument (was SetDispatcher
// setter previously — removed). The composition root in
// module_sources.go::WireArtlist wires the canonical outbox.Dispatcher at
// construction time so Enrich() can atomically combine UpsertClip +
// indexed-Qdrant in a single transaction. Indexer is the canonical
// port (was *clipindexer.Service concrete); nil-fallback path remains.
type SemanticEnricher struct {
	repo             AssetStore
	indexer          Indexer
	metaWriter       semantic.MetadataWriterPort
	searchDocBuilder searchtext.SearchDocumentBuilder
	log              *zap.Logger
	// dispatcher is the canonical media_index_outbox dispatcher used by
	// Enrich() to combine UpsertClip + indexed-Qdrant in a single tx.
	// When nil, falls back to the legacy indexer path. PR2.5: this is
	// a constructor argument (no SetDispatcher setter anymore).
	// PR2.4: typed as Dispatcher port (was *outbox.Dispatcher concrete).
	dispatcher Dispatcher
	// publisher is the canonical Drive upload canal (F2.11). Used by
	// updateCumulativeMetadataJSON to ship the regenerated metadata.json
	// back to Drive. The FolderRegistry.ensure-exists path lives in the
	// Publisher's folder-resolution machinery (ResolveFolder) so the
	// metadata.json upload is symmetric with artlist's regular upload
	// flow (root via DestinationArtlist policy + path segment = term).
	publisher delivery.Publisher
	// reader is the canonical Drive read port (F2.11). Used by
	// updateCumulativeMetadataJSON to list + download the existing
	// metadata.json before re-uploading. Drives off the composition
	// root's bundle.DriveUploader (concrete *drive.Uploader satisfies
	// drive.Reader structurally per the compile-time assertion at
	// internal/platform/drive/ports.go).
	reader drivepkg.Reader
	// CARD-3 (June 2026): file-lifecycle port split out from
	// DriveFolderManagerAdapter per godlike/06 "one owner per fact".
	// Owns Trash/Move/Rename/Cleanup; previously driveManager.Trash
	// lived on the folder manager and violated the seam.
	// *drivepkg.FileLifecycleAdapter is constructed in
	// module_sources.go::WireArtlist and threaded in via the constructor.
	lifecycle drivepkg.FileLifecycle
}

// SemanticEnricherDeps carries the dependencies for NewSemanticEnricher.
// Grouping them keeps the constructor under the archcheck 8-parameter cap
// while preserving the canonical artlist semantic-enricher surface.
type SemanticEnricherDeps struct {
	Repo             AssetStore
	Indexer          Indexer
	MetaWriter       semantic.MetadataWriterPort
	SearchDocBuilder searchtext.SearchDocumentBuilder
	Publisher        delivery.Publisher
	Reader           drivepkg.Reader
	Dispatcher       Dispatcher
	Lifecycle        drivepkg.FileLifecycle
	Log              *zap.Logger
}

// dispatchOrIndexAndUpsert performs UpsertClip + IndexClip atomically via
// the canonical media_index_outbox dispatcher.
//
// The decision logic lives in dispatchBridge (dispatch_bridge.go) so this
// method is a thin alias and can be removed in a follow-up once all
// callers route directly through the bridge.
func (e *SemanticEnricher) dispatchOrIndexAndUpsert(ctx context.Context, clip *asset.Asset, hash string) {
	bridge, err := e.newDispatchBridge()
	if err != nil {
		e.log.Warn("dispatchOrIndexAndUpsert: dispatcher not wired", zap.Error(err))
		return
	}
	if err := bridge.Dispatch(ctx, clip, hash); err != nil {
		e.log.Warn("dispatchOrIndexAndUpsert: dispatch failed",
			zap.String("clip_id", clip.ID), zap.Error(err))
	}
}

// newDispatchBridge is the enricher-local mirror of Service.newDispatchBridge.
// It pulls the four upstream deps from the enricher's own struct fields so
// callers don't have to construct dispatchBridge{} by hand. Symmetric with
// the Service variant; if the enricher is ever refactored to hold a *Service
// reference, both methods collapse into one.
//
// PR2.5: clipsRepo → repo (AssetStore port), clipIndexer → indexer
// (Indexer port), both swapped cleanly because both ports declare the
// methods this bridge uses (UpsertClip / IndexClip + IsEnabled).
func (e *SemanticEnricher) newDispatchBridge() (*dispatchBridge, error) {
	if e.dispatcher == nil {
		return nil, fmt.Errorf("artlist: dispatcher is required")
	}
	return &dispatchBridge{
		dispatcher: e.dispatcher,
		assetStore: e.repo,
		indexer:    e.indexer,
		log:        e.log,
	}, nil
}
