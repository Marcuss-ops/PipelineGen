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
		p.log.Info("clip.render.prepare.phase",
			zap.String("subsystem", "cliprender_preparer"),
			zap.String("phase", "resolve_source_start"),
			zap.String("run_id", runID),
			zap.String("asset_id", req.SourceAssetID),
		)
		ref, err := p.assets.ResolveAsset(ctx, req.SourceAssetID)
		notes := map[string]any{"asset_id": req.SourceAssetID}
		if ref != nil {
			notes["media_type"] = string(ref.MediaType)
			notes["has_local"] = ref.LocalPath != ""
			notes["has_drive"] = ref.DriveFileID != ""
			notes["duration_ms"] = ref.DurationMS
		}
		tracker.recordWith("resolve_source", time.Since(t0), notes)
		if err != nil {
			return fmt.Errorf("clip.render: resolve source %q: %w", req.SourceAssetID, err)
		}
		sourceRef = ref
		p.log.Info("clip.render.prepare.phase",
			zap.String("subsystem", "cliprender_preparer"),
			zap.String("phase", "resolve_source_done"),
			zap.String("run_id", runID),
			zap.String("asset_id", req.SourceAssetID),
			zap.String("local_path", ref.LocalPath),
			zap.String("drive_file_id", ref.DriveFileID),
			zap.Int64("duration_ms_ref", ref.DurationMS),
			zap.Int64("phase_ms", time.Since(t0).Milliseconds()),
		)
		return nil
	})
	// Text watermarks are rendered directly by the compositor and do not
	// require an asset lookup/materialization. Only asset-backed watermarks
	// participate in the resolver waves.
	watermarkAsset := watermarkEnabled && req.Watermark != nil && strings.TrimSpace(req.Watermark.Text) == ""
	if watermarkAsset {
		wave1.Go(func() error {
			t0 := time.Now()
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "resolve_watermark_start"),
				zap.String("run_id", runID),
				zap.String("asset_id", req.Watermark.AssetID),
			)
			ref, err := p.assets.ResolveAsset(ctx, req.Watermark.AssetID)
			notes := map[string]any{"asset_id": req.Watermark.AssetID}
			if ref != nil {
				notes["media_type"] = string(ref.MediaType)
				notes["has_local"] = ref.LocalPath != ""
				notes["has_drive"] = ref.DriveFileID != ""
			}
			tracker.recordWith("resolve_watermark", time.Since(t0), notes)
			if err != nil {
				return fmt.Errorf("clip.render: resolve watermark %q: %w", req.Watermark.AssetID, err)
			}
			watermarkRef = ref
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "resolve_watermark_done"),
				zap.String("run_id", runID),
				zap.String("asset_id", req.Watermark.AssetID),
				zap.String("local_path", ref.LocalPath),
				zap.String("drive_file_id", ref.DriveFileID),
				zap.Int64("phase_ms", time.Since(t0).Milliseconds()),
			)
			return nil
		})
	}
	if backgroundAsset {
		wave1.Go(func() error {
			t0 := time.Now()
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "resolve_background_start"),
				zap.String("run_id", runID),
				zap.String("asset_id", req.Background.AssetID),
			)
			ref, err := p.assets.ResolveAsset(ctx, req.Background.AssetID)
			notes := map[string]any{"asset_id": req.Background.AssetID}
			if ref != nil {
				notes["media_type"] = string(ref.MediaType)
				notes["has_local"] = ref.LocalPath != ""
				notes["has_drive"] = ref.DriveFileID != ""
			}
			tracker.recordWith("resolve_background", time.Since(t0), notes)
			if err != nil {
				return fmt.Errorf("clip.render: resolve background %q: %w", req.Background.AssetID, err)
			}
			backgroundRef = ref
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "resolve_background_done"),
				zap.String("run_id", runID),
				zap.String("asset_id", req.Background.AssetID),
				zap.String("local_path", ref.LocalPath),
				zap.String("drive_file_id", ref.DriveFileID),
				zap.Int64("phase_ms", time.Since(t0).Milliseconds()),
			)
			return nil
		})
	}
	if lookupTranscript {
		wave1.Go(func() error {
			t0 := time.Now()
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "transcript_resolve_start"),
				zap.String("run_id", runID),
				zap.String("asset_id", req.SourceAssetID),
				zap.String("language", req.Transcript.Language),
				zap.String("mode", string(req.Transcript.Mode)),
			)
			res, found, err := p.transcript.Lookup(ctx, TranscriptInput{
				AssetID:  req.SourceAssetID,
				Language: req.Transcript.Language,
				Mode:     req.Transcript.Mode,
				Persist:  req.Transcript.Persist,
			})
			tracker.recordWith("transcript_resolve", time.Since(t0), map[string]any{
				"found":    found,
				"language": req.Transcript.Language,
			})
			if err != nil {
				return fmt.Errorf("clip.render: transcript lookup: %w", err)
			}
			existing, existingFound = res, found
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "transcript_resolve_done"),
				zap.String("run_id", runID),
				zap.Bool("found", found),
				zap.Int64("phase_ms", time.Since(t0).Milliseconds()),
			)
			return nil
		})
	}
	wave1.Go(func() error {
		t0 := time.Now()
		p.log.Info("clip.render.prepare.phase",
			zap.String("subsystem", "cliprender_preparer"),
			zap.String("phase", "resolve_contract_start"),
			zap.String("run_id", runID),
		)
		c, err := p.contract.Resolve(ctx, req)
		tracker.recordWith("resolve_contract", time.Since(t0), map[string]any{
			"contract_id": c.ContractID,
			"video_codec": c.VideoCodec,
			"audio_codec": c.AudioCodec,
			"width":       c.Width,
			"height":      c.Height,
			"fps_num":     c.FPSNum,
			"fps_den":     c.FPSDen,
		})
		if err != nil {
			return fmt.Errorf("clip.render: resolve output contract: %w", err)
		}
		contract = c
		p.log.Info("clip.render.prepare.phase",
			zap.String("subsystem", "cliprender_preparer"),
			zap.String("phase", "resolve_contract_done"),
			zap.String("run_id", runID),
			zap.String("contract_id", c.ContractID),
			zap.Int("width", c.Width),
			zap.Int("height", c.Height),
			zap.Int("fps_num", c.FPSNum),
			zap.String("video_codec", c.VideoCodec),
			zap.String("audio_codec", c.AudioCodec),
			zap.Int64("phase_ms", time.Since(t0).Milliseconds()),
		)
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
		p.log.Info("clip.render.prepare.phase",
			zap.String("subsystem", "cliprender_preparer"),
			zap.String("phase", "materialize_source_start"),
			zap.String("run_id", runID),
			zap.String("asset_id", sourceRef.AssetID),
		)
		mat, err := p.material.Materialize(ctx, *sourceRef)
		sourceMat, sourceErr = mat, err
		close(sourceReady)
		phaseMS := time.Since(t0).Milliseconds()
		notes := map[string]any{}
		if mat != nil {
			notes["asset_id"] = mat.AssetID
			notes["size_bytes"] = mat.SizeBytes
			notes["from_cache"] = mat.FromCache
		}
		tracker.recordWith("materialize_source", time.Since(t0), notes)
		if err != nil {
			p.log.Error("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "materialize_source_failed"),
				zap.String("run_id", runID),
				zap.String("asset_id", sourceRef.AssetID),
				zap.Int64("phase_ms", phaseMS),
				zap.Error(err),
			)
			return fmt.Errorf("clip.render: materialize source %q: %w", sourceRef.AssetID, err)
		}
		p.log.Info("clip.render.prepare.phase",
			zap.String("subsystem", "cliprender_preparer"),
			zap.String("phase", "materialize_source_done"),
			zap.String("run_id", runID),
			zap.String("asset_id", mat.AssetID),
			zap.String("local_path", mat.LocalPath),
			zap.Int64("size_bytes", mat.SizeBytes),
			zap.Bool("from_cache", mat.FromCache),
			zap.Int64("phase_ms", phaseMS),
		)
		return nil
	})
	if watermarkRef != nil {
		wave2.Go(func() error {
			t0 := time.Now()
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "materialize_watermark_start"),
				zap.String("run_id", runID),
				zap.String("asset_id", watermarkRef.AssetID),
			)
			mat, err := p.material.Materialize(ctx, *watermarkRef)
			phaseMS := time.Since(t0).Milliseconds()
			notes := map[string]any{}
			if mat != nil {
				notes["asset_id"] = mat.AssetID
				notes["size_bytes"] = mat.SizeBytes
				notes["from_cache"] = mat.FromCache
			}
			tracker.recordWith("materialize_watermark", time.Since(t0), notes)
			if err != nil {
				p.log.Error("clip.render.prepare.phase",
					zap.String("subsystem", "cliprender_preparer"),
					zap.String("phase", "materialize_watermark_failed"),
					zap.String("run_id", runID),
					zap.String("asset_id", watermarkRef.AssetID),
					zap.Int64("phase_ms", phaseMS),
					zap.Error(err),
				)
				return fmt.Errorf("clip.render: materialize watermark %q: %w", watermarkRef.AssetID, err)
			}
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "materialize_watermark_done"),
				zap.String("run_id", runID),
				zap.String("asset_id", mat.AssetID),
				zap.String("local_path", mat.LocalPath),
				zap.Int64("size_bytes", mat.SizeBytes),
				zap.Bool("from_cache", mat.FromCache),
				zap.Int64("phase_ms", phaseMS),
			)
			watermarkMat = mat
			return nil
		})
	}
	if backgroundRef != nil {
		wave2.Go(func() error {
			t0 := time.Now()
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "materialize_background_start"),
				zap.String("run_id", runID),
				zap.String("asset_id", backgroundRef.AssetID),
			)
			mat, err := p.material.Materialize(ctx, *backgroundRef)
			phaseMS := time.Since(t0).Milliseconds()
			notes := map[string]any{}
			if mat != nil {
				notes["asset_id"] = mat.AssetID
				notes["size_bytes"] = mat.SizeBytes
				notes["from_cache"] = mat.FromCache
			}
			tracker.recordWith("materialize_background", time.Since(t0), notes)
			if err != nil {
				p.log.Error("clip.render.prepare.phase",
					zap.String("subsystem", "cliprender_preparer"),
					zap.String("phase", "materialize_background_failed"),
					zap.String("run_id", runID),
					zap.String("asset_id", backgroundRef.AssetID),
					zap.Int64("phase_ms", phaseMS),
					zap.Error(err),
				)
				return fmt.Errorf("clip.render: materialize background %q: %w", backgroundRef.AssetID, err)
			}
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "materialize_background_done"),
				zap.String("run_id", runID),
				zap.String("asset_id", mat.AssetID),
				zap.String("local_path", mat.LocalPath),
				zap.Int64("size_bytes", mat.SizeBytes),
				zap.Bool("from_cache", mat.FromCache),
				zap.Int64("phase_ms", phaseMS),
			)
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
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "transcript_generate_start"),
				zap.String("run_id", runID),
				zap.String("asset_id", req.SourceAssetID),
				zap.String("language", req.Transcript.Language),
			)
			res, err := p.transcript.Generate(ctx, TranscriptInput{
				AssetID:      req.SourceAssetID,
				Language:     req.Transcript.Language,
				Mode:         req.Transcript.Mode,
				Persist:      req.Transcript.Persist,
				SourceSHA256: sourceMat.SHA256,
			}, sourceMat)
			phaseMS := time.Since(t0).Milliseconds()
			notes := map[string]any{"language": req.Transcript.Language}
			if res != nil {
				notes["cue_count"] = len(res.Cues)
				notes["text_sha256"] = res.TextSHA256
				notes["reused"] = res.Reused
			}
			tracker.recordWith("transcript_generate", time.Since(t0), notes)
			if err != nil {
				p.log.Error("clip.render.prepare.phase",
					zap.String("subsystem", "cliprender_preparer"),
					zap.String("phase", "transcript_generate_failed"),
					zap.String("run_id", runID),
					zap.Int64("phase_ms", phaseMS),
					zap.Error(err),
				)
				return fmt.Errorf("clip.render: transcript generation: %w", err)
			}
			p.log.Info("clip.render.prepare.phase",
				zap.String("subsystem", "cliprender_preparer"),
				zap.String("phase", "transcript_generate_done"),
				zap.String("run_id", runID),
				zap.Int("cue_count", len(res.Cues)),
				zap.String("text_sha256", res.TextSHA256),
				zap.Bool("reused", res.Reused),
				zap.Int64("phase_ms", phaseMS),
			)
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
	// Emit one structured zap field per phase so dashboards can index them
	// without parsing the nested array. Field names follow the phase names
	// recorded by the tracker (e.g. resolve_source_ms, materialize_source_ms).
	phaseFields := make([]zap.Field, 0, len(timings.Phases)+4)
	for _, p := range timings.Phases {
		phaseFields = append(phaseFields, zap.Int64(p.Phase+"_ms", p.WallMS))
	}
	phaseFields = append(phaseFields,
		zap.String("run_id", runID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.Int64("total_wall_ms", timings.TotalWallMS),
		zap.Int64("total_work_ms", timings.TotalWorkMS),
		zap.Bool("parallel", timings.Parallel),
		zap.Bool("transcript_reused", transcript.Reused),
	)
	p.log.Info("clip.render.prepare.done", phaseFields...)

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

// recordWith is the notes-aware variant of record. The notes map is captured
// verbatim so the Preparer can surface phase-specific facts (cache_hit,
// bytes_downloaded, transcript cue count, ...) without losing the timing.
func (t *timingTracker) recordWith(phase string, work time.Duration, notes map[string]any) {
	ms := work.Milliseconds()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phases = append(t.phases, PhaseTiming{Phase: phase, WallMS: ms, WorkMS: ms, Notes: notes})
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
