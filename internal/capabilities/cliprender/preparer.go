package cliprender

// preparer.go implements the parallel preparation phase (feature spec §3-§5,
// §8, §12): materialize source/watermark/background, resolve or generate the
// canonical transcript, and resolve the output contract — concurrently, with
// no artificial serial barriers.
//
// The fan-out is structured as two PARALLEL waves separated only by real data
// dependencies:
//
//	Wave 1 (parallel): resolve source asset, watermark asset, background
//	                   asset, transcript DB lookup, output contract.
//	Wave 2 (parallel): materialize source, watermark, background, and
//	                   generate the transcript when the reuse lookup missed.
//
// The transcript generation in wave 2 waits only on the source materialization
// (the one real dependency: Whisper needs the local source bytes) via a
// channel handoff — it never waits on watermark/background. Independent
// downloads always run concurrently; the anti-pattern the spec forbids
// ("download source → WAIT → download watermark → WAIT") is structurally
// impossible here.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// ── Typed errors ─────────────────────────────────────────────────────

var (
	// ErrTranscriptUnavailable is returned when the transcript policy is
	// "reuse" but no READY canonical track exists. Fail-closed: the pipeline
	// never invents a transcript or silently proceeds without one.
	ErrTranscriptUnavailable = errors.New("clip.render: transcript reuse requested but no READY track exists")
	// ErrTranscriptGenerationUnavailable is returned when generation is
	// required (reuse missed) but no generation source is available.
	ErrTranscriptGenerationUnavailable = errors.New("clip.render: transcript generation required but no generation source available")
)

// Preparer is the canonical parallel-preparation orchestrator. It is
// constructed with the narrow ports; the composition root wires the concrete
// adapters.
type Preparer struct {
	assets     AssetResolver
	material   AssetMaterializer
	transcript TranscriptResolver
	contract   ContractResolver
	log        *zap.Logger
}

// NewPreparer constructs the Preparer. Fail-closed: every mandatory port is
// required — a nil port is a wiring bug, never a silently-degraded path.
func NewPreparer(
	assets AssetResolver,
	material AssetMaterializer,
	transcript TranscriptResolver,
	contract ContractResolver,
	log *zap.Logger,
) (*Preparer, error) {
	if assets == nil {
		return nil, fmt.Errorf("cliprender.NewPreparer: AssetResolver is required")
	}
	if material == nil {
		return nil, fmt.Errorf("cliprender.NewPreparer: AssetMaterializer is required")
	}
	if transcript == nil {
		return nil, fmt.Errorf("cliprender.NewPreparer: TranscriptResolver is required")
	}
	if contract == nil {
		return nil, fmt.Errorf("cliprender.NewPreparer: ContractResolver is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Preparer{assets: assets, material: material, transcript: transcript, contract: contract, log: log}, nil
}

// Prepare runs the parallel preparation phase and returns the typed handoff
// to the render step. req is normalized + validated here (idempotent — the
// worker may already have normalized it). runID scopes the attempt.
func (p *Preparer) Prepare(ctx context.Context, req *RenderRequest, runID string) (*Prepared, error) {
	start := time.Now()
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, err
	}

	tracker := newTimingTracker()

	// ── Wave 1: resolve identities + contract + transcript lookup ──────
	var (
		sourceRef     *AssetRef
		watermarkRef  *AssetRef
		backgroundRef *AssetRef
		contract      *ResolvedContract
		existing      *TranscriptResult
		existingFound bool
	)

	watermarkEnabled := req.Watermark != nil && req.Watermark.Enabled
	backgroundAsset := req.Background != nil && req.Background.Mode == BackgroundModeAsset
	lookupTranscript := req.Transcript.Mode == TranscriptModeReuse || req.Transcript.Mode == TranscriptModeReuseOrGenerate

	var wave1 errgroup.Group
	wave1.Go(func() error {
		t0 := time.Now()
		ref, err := p.assets.ResolveAsset(ctx, req.SourceAssetID)
		tracker.record("resolve_source", time.Since(t0))
		if err != nil {
			return fmt.Errorf("clip.render: resolve source %q: %w", req.SourceAssetID, err)
		}
		sourceRef = ref
		return nil
	})
	// Text watermarks are rendered directly by the compositor and do not
	// require an asset lookup/materialization. Only asset-backed watermarks
	// participate in the resolver waves.
	watermarkAsset := watermarkEnabled && req.Watermark != nil && strings.TrimSpace(req.Watermark.Text) == ""
	if watermarkAsset {
		wave1.Go(func() error {
			t0 := time.Now()
			ref, err := p.assets.ResolveAsset(ctx, req.Watermark.AssetID)
			tracker.record("resolve_watermark", time.Since(t0))
			if err != nil {
				return fmt.Errorf("clip.render: resolve watermark %q: %w", req.Watermark.AssetID, err)
			}
			watermarkRef = ref
			return nil
		})
	}
	if backgroundAsset {
		wave1.Go(func() error {
			t0 := time.Now()
			ref, err := p.assets.ResolveAsset(ctx, req.Background.AssetID)
			tracker.record("resolve_background", time.Since(t0))
			if err != nil {
				return fmt.Errorf("clip.render: resolve background %q: %w", req.Background.AssetID, err)
			}
			backgroundRef = ref
			return nil
		})
	}
	if lookupTranscript {
		wave1.Go(func() error {
			t0 := time.Now()
			res, found, err := p.transcript.Lookup(ctx, TranscriptInput{
				AssetID:  req.SourceAssetID,
				Language: req.Transcript.Language,
				Mode:     req.Transcript.Mode,
				Persist:  req.Transcript.Persist,
			})
			tracker.record("transcript_resolve", time.Since(t0))
			if err != nil {
				return fmt.Errorf("clip.render: transcript lookup: %w", err)
			}
			existing, existingFound = res, found
			return nil
		})
	}
	wave1.Go(func() error {
		t0 := time.Now()
		c, err := p.contract.Resolve(ctx, req)
		tracker.record("resolve_contract", time.Since(t0))
		if err != nil {
			return fmt.Errorf("clip.render: resolve output contract: %w", err)
		}
		contract = c
		return nil
	})
	if err := wave1.Wait(); err != nil {
		return nil, err
	}

	// ── Wave 2: materialize + generate (parallel, channel-gated) ───────
	var (
		sourceMat     *MaterializedAsset
		watermarkMat  *MaterializedAsset
		backgroundMat *MaterializedAsset
		transcript    *TranscriptResult
	)

	// sourceReady is the one real data dependency: transcript generation
	// needs the materialized source bytes. Watermark/background never gate it.
	sourceReady := make(chan struct{})
	var sourceErr error

	generateTranscript := req.Transcript.Mode == TranscriptModeGenerate ||
		(req.Transcript.Mode == TranscriptModeReuseOrGenerate && (!existingFound ||
			(req.Subtitles.Enabled && existing != nil && len(existing.Cues) == 0)))

	var wave2 errgroup.Group
	wave2.Go(func() error {
		t0 := time.Now()
		mat, err := p.material.Materialize(ctx, *sourceRef)
		sourceMat, sourceErr = mat, err
		close(sourceReady)
		tracker.record("materialize_source", time.Since(t0))
		if err != nil {
			return fmt.Errorf("clip.render: materialize source %q: %w", sourceRef.AssetID, err)
		}
		return nil
	})
	if watermarkRef != nil {
		wave2.Go(func() error {
			t0 := time.Now()
			mat, err := p.material.Materialize(ctx, *watermarkRef)
			tracker.record("materialize_watermark", time.Since(t0))
			if err != nil {
				return fmt.Errorf("clip.render: materialize watermark %q: %w", watermarkRef.AssetID, err)
			}
			watermarkMat = mat
			return nil
		})
	}
	if backgroundRef != nil {
		wave2.Go(func() error {
			t0 := time.Now()
			mat, err := p.material.Materialize(ctx, *backgroundRef)
			tracker.record("materialize_background", time.Since(t0))
			if err != nil {
				return fmt.Errorf("clip.render: materialize background %q: %w", backgroundRef.AssetID, err)
			}
			backgroundMat = mat
			return nil
		})
	}
	if generateTranscript {
		wave2.Go(func() error {
			// Wait only on the real dependency (materialized source bytes).
			// The wait is a dependency, not work: the phase timing below
			// measures only the Generate call itself.
			<-sourceReady
			if sourceErr != nil {
				return fmt.Errorf("clip.render: transcript generation aborted: source materialization failed: %w", sourceErr)
			}
			t0 := time.Now()
			res, err := p.transcript.Generate(ctx, TranscriptInput{
				AssetID:      req.SourceAssetID,
				Language:     req.Transcript.Language,
				Mode:         req.Transcript.Mode,
				Persist:      req.Transcript.Persist,
				SourceSHA256: sourceMat.SHA256,
			}, sourceMat)
			tracker.record("transcript_generate", time.Since(t0))
			if err != nil {
				return fmt.Errorf("clip.render: transcript generation: %w", err)
			}
			transcript = res
			return nil
		})
	}
	if err := wave2.Wait(); err != nil {
		return nil, err
	}

	// ── Post-wave resolution ────────────────────────────────────────────
	if !existingFound && req.Transcript.Mode == TranscriptModeReuse {
		return nil, fmt.Errorf("%w: asset %q language %q", ErrTranscriptUnavailable, req.SourceAssetID, req.Transcript.Language)
	}
	if existingFound && transcript == nil {
		transcript = existing
	}
	if transcript == nil || !transcript.HasText() {
		return nil, fmt.Errorf("%w: asset %q language %q", ErrTranscriptGenerationUnavailable, req.SourceAssetID, req.Transcript.Language)
	}

	timings := tracker.finish(time.Since(start))
	p.log.Info("clip.render.prepare.done",
		zap.String("run_id", runID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.Int64("total_wall_ms", timings.TotalWallMS),
		zap.Int64("total_work_ms", timings.TotalWorkMS),
		zap.Bool("parallel", timings.Parallel),
		zap.Bool("transcript_reused", transcript.Reused),
	)

	return &Prepared{
		RunID:      runID,
		Source:     sourceMat,
		Watermark:  watermarkMat,
		Background: backgroundMat,
		Transcript: transcript,
		Contract:   contract,
		Timings:    timings,
	}, nil
}

// ── Timing tracker ───────────────────────────────────────────────────

// timingTracker collects per-phase work durations safely across the parallel
// goroutines and projects the wall-vs-work aggregate.
type timingTracker struct {
	mu     sync.Mutex
	phases []PhaseTiming
	workMS int64
}

func newTimingTracker() *timingTracker {
	return &timingTracker{phases: make([]PhaseTiming, 0, 5)}
}

func (t *timingTracker) record(phase string, work time.Duration) {
	ms := work.Milliseconds()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phases = append(t.phases, PhaseTiming{Phase: phase, WallMS: ms, WorkMS: ms})
	t.workMS += ms
}

// finish freezes the phases and computes the aggregate. Parallel is true when
// phases overlapped (wall < work). A single-phase run or a serial fallback
// reports Parallel=false.
func (t *timingTracker) finish(totalWall time.Duration) PreparationTimings {
	t.mu.Lock()
	defer t.mu.Unlock()
	wall := totalWall.Milliseconds()
	timings := PreparationTimings{
		TotalWallMS: wall,
		TotalWorkMS: t.workMS,
		Parallel:    t.workMS > wall,
		Phases:      append([]PhaseTiming(nil), t.phases...),
	}
	return timings
}
