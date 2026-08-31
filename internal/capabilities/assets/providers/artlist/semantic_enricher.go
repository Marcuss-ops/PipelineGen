// Package artlist — semantic_enricher.go is the slim orchestrator for the
// 3-file split of the pre-PR 509 LoC semantic_enricher.go (LONG-FILES-DECOMPOSITION-V2-2026-07-06 P3 BASSA, July 2026).
//
// File layout (per godlike/06 one-canonical-owner-per-fact):
//
//   - semantic_enricher.go          (this file) — slim orchestrator: SemanticEnricher struct + NewSemanticEnricher + dispatchOrIndexAndUpsert + newDispatchBridge + enrichMetaMu (package-level lock used by Enrich in semantic_enricher_enrich.go)
//   - semantic_enricher_enrich.go   (sibling)   — Enrich entry-point + buildArtlistPrompt + deduplicateStrings pure helpers
//   - semantic_enricher_metadata.go (sibling)   — updateCumulativeMetadataJSON (the RMW path for cumulative Drive metadata.json sync)
//
// All cross-file symbol resolution works via same-package scope visibility.
// Pure code-motion split — zero behavior change, zero new exported symbols.
package artlist

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	searchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// enrichMetaMu serialises access to the cumulative metadata.json file
// across concurrent Enrich invocations on the same folder.
//
// Lives in the slim orchestrator because the lock is acquired in
// semantic_enricher_enrich.go's Enrich method (the only caller in
// the package); the var is package-scope so the sibling file can
// reach it without re-declaring. The lock is intentionally
// package-scope (not embed-on-struct) because it is a
// per-Folder resource guarded across goroutines that may outlive
// a single SemanticEnricher (test fixtures can spawn many
// enrichers pointing at the same folder).
//
// PR-CODE-HEALTH-C3 audit-pin (2026-07-09): this is one of only 2
// package-level mutexes in production code. It CANNOT be replaced
// with sync.Once because it guards repeated RMW (read-modify-write)
// cycles to metadata.json across concurrent Enrich invocations, not
// a one-time initialisation. It CANNOT be moved into a struct field
// because the lock spans across goroutines that outlive a single
// SemanticEnricher instance — multiple enrichers may point at the
// same Drive folder. The per-folder serialisation is the correct
// semantics; the package-level var is the least-bad home.
var enrichMetaMu sync.Mutex

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

// NewSemanticEnricher crea un enricher pronto per il package artlist.
// Usa semantic.MetadataWriterPort (chiamato GeneratePayload) invece di
// chiamare Tagger() direttamente, per garantire che tutto il metadata
// passi dal percorso centralizzato.
//
// F2.11 (June 2026): the `driveManager` parameter was replaced by
// `publisher delivery.Publisher + reader drivepkg.Reader` (the canonical
// write + read ports per DRIVE-005 closure). The Publisher is mandatory
// at composition (ErrPublisherUnavailable guard lives in Service.NewService);
// the Reader is mandatory only when the metadata.json sync path is
// wired (production) — test fixtures that opt out of cumulative
// metadata.json writes can pass nil reader (the call site already
// nil-tolerates because some deployments use local-only mode).
//
// PR2.5: dispatcher param added. Pass nil only in tests / for the
// legacy fallback path; production wiring always passes the canonical
// outbox.Dispatcher so Enrich() routes UpsertClip + IndexClip through
// the dispatcher rather than the legacy clipIndexer.IndexClip.
// Indexer is the canonical port (PR2.5 wiring: bundle.ClipIndexerService
// satisfies it directly because *clipindexer.Service has IndexClip +
// IsEnabled matching the port).
// PR2.7 → F2.11: driveUploader/driverManager replaced by
// publisher + reader (canonical ports). Pass nil for reader in tests.
//
// P0-#2 (July 2026): the `metaWriter` parameter type is now
// `semantic.MetadataWriterPort` (the canonical narrow typed surface),
// NOT `semantic.MetadataWriterPort` (the retired fake concrete). The
// production composition root passes `nil` when the real semantic
// tagger is absent; tests pass `semantic.NewNopMetadataWriter(log)`
// for the explicit-nop path. The Enrich method already nil-checks
// `e.metaWriter == nil` so both paths are fail-closed.
func NewSemanticEnricher(deps SemanticEnricherDeps) *SemanticEnricher {
	return &SemanticEnricher{
		repo:             deps.Repo,
		indexer:          deps.Indexer,
		metaWriter:       deps.MetaWriter,
		searchDocBuilder: deps.SearchDocBuilder,
		publisher:        deps.Publisher,
		reader:           deps.Reader,
		dispatcher:       deps.Dispatcher,
		lifecycle:        deps.Lifecycle,
		log:              deps.Log,
	}
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
