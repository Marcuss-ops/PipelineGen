// Package artlist — dispatch_bridge.go
//
// DispatchBridge is the single entry point for clip persistence and indexing
// in the artlist pipeline. It routes through the canonical media_index_outbox
// dispatcher (atomic upsert + outbox enqueue). The dispatcher is required —
// construction fails if it is nil.
//
// PR2.5: clipsRepo → assetStore (AssetStore port), clipIndexer → indexer
// (Indexer port). Both ports declare exactly the methods this bridge
// uses (UpsertClip + IndexClip + IsEnabled) so the swap is mechanical
// with no behavior change.
package artlist

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// dispatchBridge consolidates the canonical write/index decision into one
// helper. It pulls its building blocks from the surrounding Service so
// callers don't have to plumb them through.
//
// The dispatcher is REQUIRED — production wiring must provide it.
// Calling dispatchBridge.Dispatch routes clip persistence and indexing
// through the canonical outbox dispatcher (atomic upsert + outbox enqueue).
type dispatchBridge struct {
	dispatcher Dispatcher
	assetStore AssetStore
	indexer    Indexer
	log        *zap.Logger
}

// newDispatchBridge returns a bridge wired to the Service's current
// upstream dependencies. Returns an error if the dispatcher is nil.
//
// PR2.5: pulls ports (assetStore, indexer, dispatcher) from the
// surrounding Service. The legacy concrete fields are gone; this is
// the only path the rest of the package uses.
func (s *Service) newDispatchBridge() (*dispatchBridge, error) {
	if s.dispatcher == nil {
		return nil, fmt.Errorf("artlist: dispatcher is required — production wiring must provide it")
	}
	return &dispatchBridge{
		dispatcher: s.dispatcher,
		assetStore: s.assetStore,
		indexer:    s.indexer,
		log:        s.log,
	}, nil
}

// Dispatch routes clip persistence and indexing through the canonical
// media_index_outbox dispatcher (atomic upsert + outbox enqueue).
//
// The dispatcher is required — this method returns an error if the
// bridge was constructed without one.
func (b *dispatchBridge) Dispatch(ctx context.Context, clip *asset.Asset, hash string) error {
	if clip == nil || clip.ID == "" {
		return nil
	}
	if b.dispatcher == nil {
		return fmt.Errorf("dispatch_bridge: dispatcher is nil")
	}
	if err := b.dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
		return fmt.Errorf("dispatch_bridge: dispatcher.EnqueueAndIndex: %w", err)
	}
	return nil
}

// DiagnosticsService fornisce statistiche e diagnostiche sul catalogo Artlist
type DiagnosticsService struct {
	svc *Service
}

// NewDiagnosticsService crea un nuovo servizio diagnostico
func NewDiagnosticsService(svc *Service) *DiagnosticsService {
	return &DiagnosticsService{svc: svc}
}

// GetStats ottiene statistiche generali sul catalogo
func (d *DiagnosticsService) GetStats(ctx context.Context) (*Stats, error) {
	totalClips, err := d.svc.assetStore.CountClips(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count clips: %w", err)
	}

	return &Stats{
		OK:                true,
		ClipsTotal:        totalClips,
		ArtlistClipsTotal: totalClips,
	}, nil
}

// Diagnostics ottiene informazioni diagnostiche per un termine specifico
func (d *DiagnosticsService) Diagnostics(ctx context.Context, term string) (*DiagnosticsResponse, error) {
	resp := &DiagnosticsResponse{
		OK:             true,
		RootFolderID:   ResolveRootFolderID(d.svc.cfg),
		DriveFolderID:  ResolveRootFolderID(d.svc.cfg),
		NodeScraperDir: "node-scraper",
		HasDriveClient: d.svc.assetDestResolver != nil,
		// PR2.6: ArtlistDB unified into MainDB after media.db.sqlite
		// consolidation. HasArtlistDB still reports the main DB ready
		// state (the field name is part of the public Diagnostics
		// response shape and stays for backwards compatibility).
		HasArtlistDB: d.svc.mainDB != nil,
		MainDBReady:  d.svc.mainDB != nil,
	}

	if d.svc.assetStore != nil {
		if total, err := d.svc.assetStore.CountClips(ctx); err == nil {
			resp.ClipsTotal = total
			resp.ArtlistClipsTotal = total
		}
	}

	term = strings.TrimSpace(term)
	if term != "" {
		resp.SearchTerm = term
		if matches, err := d.svc.assetStore.SearchClips(ctx, "artlist", term); err == nil {
			resp.MatchingClips = len(matches)
			resp.EstimatedSize = len(matches)
		}

		if lastProcessedAt, err := d.svc.assetStore.LastUpdatedAtForTerm(ctx, term); err == nil {
			resp.LastProcessedAt = lastProcessedAt
		}
	}

	return resp, nil
}
