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
//
// Each phase is implemented by one instrumented helper (instrumentedResolve /
// instrumentedMaterialize / instrumentedResolveContract /
// instrumentedTranscriptLookup / instrumentedTranscriptGenerate) that owns the
// phase's timing sample, its structured debug lines and its typed error.
// Prepare itself owns ONLY the fan-out: which phases run, and what each one
// waits on.

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
//
// Prepare orchestrates the two waves; the observability and error contract of
// every individual phase lives in its instrumented helper below, so the
// fan-out stays legible.
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
	// Only `reuse` looks the track up. `generate` is an explicit manual repair
	// that deliberately skips the lookup and re-runs ASR.
	lookupTranscript := req.Transcript.Mode == TranscriptModeReuse

	var wave1 errgroup.Group
	wave1.Go(func() error {
		ref, err := p.instrumentedResolve(ctx, runID, "source", req.SourceAssetID, tracker, true)
		if err != nil {
			return err
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
			ref, err := p.instrumentedResolve(ctx, runID, "watermark", req.Watermark.AssetID, tracker, false)
			if err != nil {
				return err
			}
			watermarkRef = ref
			return nil
		})
	}
	if backgroundAsset {
		wave1.Go(func() error {
			ref, err := p.instrumentedResolve(ctx, runID, "background", req.Background.AssetID, tracker, false)
			if err != nil {
				return err
			}
			backgroundRef = ref
			return nil
		})
	}
	if lookupTranscript {
		wave1.Go(func() error {
			res, found, err := p.instrumentedTranscriptLookup(ctx, runID, req, tracker)
			if err != nil {
				return err
			}
			existing, existingFound = res, found
			return nil
		})
	}
	wave1.Go(func() error {
		c, err := p.instrumentedResolveContract(ctx, runID, req, tracker)
		if err != nil {
			return err
		}
		contract = c
		return nil
	})
	if err := wave1.Wait(); err != nil {
		return nil, err
	}

	// Resolve the background media family ONCE, before any byte is materialized:
	// it is a sealed-plan input (image vs video selects a different render
	// layer), so it is decided here and never inferred by the render worker from
	// a filename. resolveBackgroundKind owns the rule; a plate we cannot
	// classify fails closed.
	backgroundKind := ""
	if backgroundRef != nil {
		resolved, err := resolveBackgroundKind(req.Background.Kind, backgroundRef.AssetID, backgroundRef.MediaType)
		if err != nil {
			return nil, err
		}
		backgroundKind = resolved
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

	// ASR never runs implicitly inside a render: only the explicit `generate`
	// repair request generates. (The legacy `reuse_or_generate` implicit-ASR
	// branch was DELETED in the 2026-09-13 audit.)
	generateTranscript := req.Transcript.Mode == TranscriptModeGenerate

	var wave2 errgroup.Group
	wave2.Go(func() error {
		// The publish callback fires immediately after the materializer port
		// returns and BEFORE the phase is instrumented: the transcript
		// generator is released as soon as the source bytes exist, never
		// gated on the watermark/background downloads or on the phase logging.
		_, err := p.instrumentedMaterialize(ctx, runID, "source", sourceRef, tracker, func(mat *MaterializedAsset, err error) {
			sourceMat, sourceErr = mat, err
			close(sourceReady)
		})
		return err
	})
	if watermarkRef != nil {
		wave2.Go(func() error {
			mat, err := p.instrumentedMaterialize(ctx, runID, "watermark", watermarkRef, tracker, nil)
			if err != nil {
				return err
			}
			watermarkMat = mat
			return nil
		})
	}
	if backgroundRef != nil {
		wave2.Go(func() error {
			mat, err := p.instrumentedMaterialize(ctx, runID, "background", backgroundRef, tracker, nil)
			if err != nil {
				return err
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
			res, err := p.instrumentedTranscriptGenerate(ctx, runID, req, sourceMat, tracker)
			if err != nil {
				return err
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
	// prepare.done carries the aggregate durations only; it is Debug like the
	// per-wave detail. Info stays reserved for the job-level milestones
	// (started → submitted → remote render completed → publish completed →
	// failed) so the operator log is not flooded by internal wave chatter.
	p.log.Debug("clip.render.prepare.done", phaseFields...)

	return &Prepared{
		RunID:          runID,
		Source:         sourceMat,
		Watermark:      watermarkMat,
		Background:     backgroundMat,
		BackgroundKind: backgroundKind,
		Transcript:     transcript,
		Contract:       contract,
		Timings:        timings,
	}, nil
}

// ── Instrumented phase helpers ───────────────────────────────────────
//
// One helper per prepare phase. Each owns the phase's observability contract
// (start/done/failed debug lines + the tracker sample with its notes) and its
// typed error wrap, so the phases stay individually visible in the operator
// log and in the timing projection. The helpers hold no scheduling state:
// Prepare alone decides which phases run and what they wait on.

// instrumentedResolve runs a wave-1 asset resolution for one role. kind is the
// phase key ("source", "watermark", "background"): it drives the tracker key
// (resolve_<kind>), the debug phase names (resolve_<kind>_start/_done) and the
// error prefix. withDuration adds the source-only duration_ms_ref field.
func (p *Preparer) instrumentedResolve(ctx context.Context, runID, kind, assetID string, tracker *timingTracker, withDuration bool) (*AssetRef, error) {
	t0 := time.Now()
	p.log.Debug("clip.render.prepare.phase",
		zap.String("subsystem", "cliprender_preparer"),
		zap.String("phase", "resolve_"+kind+"_start"),
		zap.String("run_id", runID),
		zap.String("asset_id", assetID),
	)
	ref, err := p.assets.ResolveAsset(ctx, assetID)
	notes := map[string]any{"asset_id": assetID}
	if ref != nil {
		notes["media_type"] = string(ref.MediaType)
		notes["has_local"] = ref.LocalPath != ""
		notes["has_drive"] = ref.DriveFileID != ""
		if withDuration {
			notes["duration_ms"] = ref.DurationMS
		}
	}
	tracker.recordWith("resolve_"+kind, time.Since(t0), notes)
	if err != nil {
		return nil, fmt.Errorf("clip.render: resolve %s %q: %w", kind, assetID, err)
	}
	phaseMS := time.Since(t0).Milliseconds()
	fields := []zap.Field{
		zap.String("subsystem", "cliprender_preparer"),
		zap.String("phase", "resolve_"+kind+"_done"),
		zap.String("run_id", runID),
		zap.String("asset_id", assetID),
		zap.String("local_path", ref.LocalPath),
		zap.String("drive_file_id", ref.DriveFileID),
	}
	if withDuration {
		fields = append(fields, zap.Int64("duration_ms_ref", ref.DurationMS))
	}
	fields = append(fields, zap.Int64("phase_ms", phaseMS))
	p.log.Debug("clip.render.prepare.phase", fields...)
	return ref, nil
}

// instrumentedMaterialize runs a wave-2 materialization for one role and
// records the canonical timing/log contract (materialize_<kind>). When publish
// is non-nil it fires immediately after the materializer port returns, before
// the phase is instrumented — the source phase uses it to hand the bytes to
// the transcript generator and release its gate.
func (p *Preparer) instrumentedMaterialize(ctx context.Context, runID, kind string, ref *AssetRef, tracker *timingTracker, publish func(*MaterializedAsset, error)) (*MaterializedAsset, error) {
	t0 := time.Now()
	p.log.Debug("clip.render.prepare.phase",
		zap.String("subsystem", "cliprender_preparer"),
		zap.String("phase", "materialize_"+kind+"_start"),
		zap.String("run_id", runID),
		zap.String("asset_id", ref.AssetID),
	)
	mat, err := p.material.Materialize(ctx, *ref)
	if publish != nil {
		publish(mat, err)
	}
	phaseMS := time.Since(t0).Milliseconds()
	notes := map[string]any{}
	if mat != nil {
		notes["asset_id"] = mat.AssetID
		notes["size_bytes"] = mat.SizeBytes
		notes["from_cache"] = mat.FromCache
	}
	tracker.recordWith("materialize_"+kind, time.Since(t0), notes)
	if err != nil {
		p.log.Error("clip.render.prepare.phase",
			zap.String("subsystem", "cliprender_preparer"),
			zap.String("phase", "materialize_"+kind+"_failed"),
			zap.String("run_id", runID),
			zap.String("asset_id", ref.AssetID),
			zap.Int64("phase_ms", phaseMS),
			zap.Error(err),
		)
		return nil, fmt.Errorf("clip.render: materialize %s %q: %w", kind, ref.AssetID, err)
	}
	p.log.Debug("clip.render.prepare.phase",
		zap.String("subsystem", "cliprender_preparer"),
		zap.String("phase", "materialize_"+kind+"_done"),
		zap.String("run_id", runID),
		zap.String("asset_id", mat.AssetID),
		zap.String("local_path", mat.LocalPath),
		zap.Int64("size_bytes", mat.SizeBytes),
		zap.Bool("from_cache", mat.FromCache),
		zap.Int64("phase_ms", phaseMS),
	)
	return mat, nil
}

// instrumentedResolveContract resolves the sealed output contract and records
// the resolve_contract phase, including the resolved codec/geometry facts.
func (p *Preparer) instrumentedResolveContract(ctx context.Context, runID string, req *RenderRequest, tracker *timingTracker) (*ResolvedContract, error) {
	t0 := time.Now()
	p.log.Debug("clip.render.prepare.phase",
		zap.String("subsystem", "cliprender_preparer"),
		zap.String("phase", "resolve_contract_start"),
		zap.String("run_id", runID),
	)
	c, err := p.contract.Resolve(ctx, req)
	if err != nil {
		tracker.recordWith("resolve_contract", time.Since(t0), map[string]any{"error": err.Error()})
		return nil, fmt.Errorf("clip.render: resolve output contract: %w", err)
	}
	tracker.recordWith("resolve_contract", time.Since(t0), map[string]any{
		"contract_id": c.ContractID,
		"video_codec": c.VideoCodec,
		"audio_codec": c.AudioCodec,
		"width":       c.Width,
		"height":      c.Height,
		"fps_num":     c.FPSNum,
		"fps_den":     c.FPSDen,
	})
	p.log.Debug("clip.render.prepare.phase",
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
	return c, nil
}

// instrumentedTranscriptLookup runs the reuse-mode transcript DB lookup and
// records the transcript_resolve phase (found + language facts).
func (p *Preparer) instrumentedTranscriptLookup(ctx context.Context, runID string, req *RenderRequest, tracker *timingTracker) (*TranscriptResult, bool, error) {
	t0 := time.Now()
	p.log.Debug("clip.render.prepare.phase",
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
		return nil, false, fmt.Errorf("clip.render: transcript lookup: %w", err)
	}
	p.log.Debug("clip.render.prepare.phase",
		zap.String("subsystem", "cliprender_preparer"),
		zap.String("phase", "transcript_resolve_done"),
		zap.String("run_id", runID),
		zap.Bool("found", found),
		zap.Int64("phase_ms", time.Since(t0).Milliseconds()),
	)
	return res, found, nil
}

// instrumentedTranscriptGenerate runs the explicit `generate` repair, sourcing
// the ASR input from the already-materialized source bytes, and records the
// transcript_generate phase.
func (p *Preparer) instrumentedTranscriptGenerate(ctx context.Context, runID string, req *RenderRequest, source *MaterializedAsset, tracker *timingTracker) (*TranscriptResult, error) {
	t0 := time.Now()
	p.log.Debug("clip.render.prepare.phase",
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
		SourceSHA256: source.SHA256,
	}, source)
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
		return nil, fmt.Errorf("clip.render: transcript generation: %w", err)
	}
	p.log.Debug("clip.render.prepare.phase",
		zap.String("subsystem", "cliprender_preparer"),
		zap.String("phase", "transcript_generate_done"),
		zap.String("run_id", runID),
		zap.Int("cue_count", len(res.Cues)),
		zap.String("text_sha256", res.TextSHA256),
		zap.Bool("reused", res.Reused),
		zap.Int64("phase_ms", phaseMS),
	)
	return res, nil
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
