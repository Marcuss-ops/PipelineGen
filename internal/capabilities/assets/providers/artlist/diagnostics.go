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
package assets

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

// ProbeSet is the typed return-value of the SystemProber port (Fase 2,
// July 2026, godlike/07 NO-FAKE-AVAILABILITY §22). Exactly 10
// ProbeResult fields, one per dependency. Renaming any field here is
// a contract break for the /api/artlist/diagnostics wire shape.
//
// godlike/06 SSOT: the 10 probe field names are the CANONICAL
// identifier set shared between (a) the SystemProber port return
// type in application code, (b) the AdminSystemProber concrete
// implementation in infrastructure, (c) the DiagnosticsResponse wire
// shape in types.go, and (d) the consumers (operator dashboards,
// health probes). Drift across any of these surfaces is a godlike/06
// regression that breaks forward-compat with the operators' scripts.
type ProbeSet struct {
	Scraper           ProbeResult `json:"scraper"`
	Browser           ProbeResult `json:"browser"`
	Session           ProbeResult `json:"session"`
	Downloader        ProbeResult `json:"downloader"`
	FFmpegBinary      ProbeResult `json:"ffmpeg_binary"`
	DriveFolder       ProbeResult `json:"drive_folder"`
	SQLiteWritable    ProbeResult `json:"sqlite_writable"`
	OutboxDispatcher  ProbeResult `json:"outbox_dispatcher"`
	QdrantReachable   ProbeResult `json:"qdrant_reachable"`
	EmbeddingProvider ProbeResult `json:"embedding_provider"`
}

// SystemProber is the canonical godlike/06 port (Fase 2, July 2026)
// that the composition root injects via ServicePorts. ProbeAll
// returns the 10 ProbeResult fields WITHOUT aggregating to a single
// OK / overall-status field — each probe is its own honest check
// (godlike/07).
//
// Return contract: ProbeAll NEVER returns an error. Per-probe
// failures are encoded per-field via ProbeResult.Error. A fatal
// ProbeAll failure (probe runner panicked, internal unrecoverable
// error) is surfaced by DiagnosticsService.Diagnostics as the
// DiagnosticsResponse.Error top-level field, NOT by an error return.
type SystemProber interface {
	ProbeAll(ctx context.Context) ProbeSet
}

// DiagnosticsService fornisce statistiche e diagnostiche sul catalogo Artlist.
// (Fase 2, July 2026): the diagnostics surface now goes through
// systemProber.ProbeAll for the 10 wire-by-wire probes, plus
// runRepo.LatestRun + assetStore.CountBySource for the special
// informational fields, plus the legacy assetStore term-search for
// the term-keyed surface. NO aggregated top-level OK field — godlike/07.
type DiagnosticsService struct {
	svc          *Service
	systemProber SystemProber
}

// NewDiagnosticsService crea un nuovo servizio diagnostico.
// systemProber is REQUIRED at composition time — Fase 2 wire-by-wire
// contract. Passing nil falls back to a stub that reports every probe
// as failed (Error="system prober not wired at composition time")
// rather than the legacy fake-availability `OK: true` aggregate,
// honoring godlike/07 fail-closed semantics.
func NewDiagnosticsService(svc *Service, systemProber SystemProber) *DiagnosticsService {
	if systemProber == nil {
		systemProber = stubSystemProber{}
	}
	return &DiagnosticsService{svc: svc, systemProber: systemProber}
}

// stubSystemProber is the godlike/07 fail-closed fallback when
// composition forgot to inject SystemProber (forbidden per Fase 1
// gates, but constructing one defensively here protects tests and
// unusual composition paths). Reports every probe as failed; never
// fake-availability.
type stubSystemProber struct{}

func (stubSystemProber) ProbeAll(ctx context.Context) ProbeSet {
	f := func(errMsg string) ProbeResult {
		return ProbeResult{
			OK:        false,
			Error:     errMsg,
			Detail:    "stubSystemProber activated: composition root forgot to wire SystemProber (godlike/07 fail-closed)",
			ElapsedMs: 0,
		}
	}
	return ProbeSet{
		Scraper:           f("stub prober"),
		Browser:           f("stub prober"),
		Session:           f("stub prober"),
		Downloader:        f("stub prober"),
		FFmpegBinary:      f("stub prober"),
		DriveFolder:       f("stub prober"),
		SQLiteWritable:    f("stub prober"),
		OutboxDispatcher:  f("stub prober"),
		QdrantReachable:   f("stub prober"),
		EmbeddingProvider: f("stub prober"),
	}
}

// GetStats ottiene statistiche generali sul catalogo
//
// PR-P2-DIAGNOSTICS-REALE (July 2026): Stats is the legacy surface with
// `OK: true` aggregate, kept for backward-compat with operator scripts
// that pre-date the Fase 2 endpoint refactor. The aggregate `OK` is
// preserved here ONLY because Stats is wired to /api/artlist/stats which
// has 4 explicit script consumers (scripts/artlist_stock_e2e*.sh).
// Migrating Stats to wire-by-wire probes is out of scope for the Fase 2
// /api/artlist/diagnostics endpoint rewrite; do it in a follow-up that
// also updates the 4 stock-e2e script consumers in lockstep.
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

// Diagnostics ottiene informazioni diagnostiche wire-by-wire per il
// modulo Artlist (Fase 2 rewrite, July 2026 — replaces the v1
// audit-fail aggregate `OK: true` + Has* object-existence checks).
//
// godlike/07 NO-FAKE-AVAILABILITY §22 contract:
//   - NEVER aggregates to a top-level OK bool.
//   - Every probe is its own honest check; failures surface via
//     per-probe `ProbeResult.Error`.
//   - Special fields (LatestRun, LastError, ClipsArtlistTotal) are
//     sourced from canonical SSOT (RunRepository / ClipsRepository),
//     not from object-existence pointer checks.
//
// Per-probe execution is sequential (godlike/07 fail-closed prefers
// sequential for audit simplicity): a worker pool would mask trace
// order. Per-probe default timeout is the SystemProber's own concern
// (infrastructure layer).
func (d *DiagnosticsService) Diagnostics(ctx context.Context, term string) (*DiagnosticsResponse, error) {
	probeSet := d.systemProber.ProbeAll(ctx)

	resp := &DiagnosticsResponse{
		RootFolderID: ResolveRootFolderID(d.svc.cfg),
		// 10 wire-by-wire probes — never aggregated.
		Scraper:           probeSet.Scraper,
		Browser:           probeSet.Browser,
		Session:           probeSet.Session,
		Downloader:        probeSet.Downloader,
		FFmpegBinary:      probeSet.FFmpegBinary,
		DriveFolder:       probeSet.DriveFolder,
		SQLiteWritable:    probeSet.SQLiteWritable,
		OutboxDispatcher:  probeSet.OutboxDispatcher,
		QdrantReachable:   probeSet.QdrantReachable,
		EmbeddingProvider: probeSet.EmbeddingProvider,
	}

	// Special informational surfaces (Fase 2): LatestRun + LastError
	// sourced from RunRepository (canonical SSOT for artlist_runs
	// aggregate writer). CountBySource('artlist') sourced from
	// ClipsRepository (canonical SSOT post-Fase 0 clips_statistics.go).
	if d.svc.runRepo != nil {
		if latest, err := d.svc.runRepo.LatestRun(ctx); err == nil && latest != nil {
			resp.LatestRun = &LatestRunSummary{
				RunID:     latest.RunID,
				Status:    latest.Status,
				Term:      latest.Term,
				Error:     latest.Error, // godlike/06: Error not ErrorMessage (mirror infra→app mapping)
				CreatedAt: latest.CreatedAt,
			}
			resp.LastError = latest.Error
		}
	}

	if d.svc.assetStore != nil {
		// PR-P2-DIAGNOSTICS-REALE (July 2026): per-source count is
		// sourced from the canonical ClipsRepository.CountBySource
		// (godlike/06 SSOT; Fase 0 added this helper). never fall back
		// to v1's CountClips() (which counts everything across sources
		// — wrong attribution for an "artlist indexed clips" surface).
		if count, err := d.svc.assetStore.CountBySource(ctx, "artlist"); err == nil {
			resp.ClipsArtlistTotal = count
		}

		// Legacy term-search surface preserved (no fail-closed
		// enforcement on these fields — they were always informational,
		// not probe-shaped, and operator scripts/queries rely on them).
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
	}

	return resp, nil
}
