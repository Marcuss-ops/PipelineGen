package cliprender

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

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
	// A (nil, nil) return is a port-contract violation. Convert it to a typed
	// error BEFORE the publish callback and before any dereference: the source
	// gate would otherwise close with a nil error and the transcript generator
	// (or the success log below) would dereference nil and panic the worker.
	if err == nil && mat == nil {
		err = fmt.Errorf("%w: materialize %s %q", ErrMaterializerReturnedNoArtifact, kind, ref.AssetID)
	}
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
		"contract_id": c.ID,
		"video_codec": c.VideoCodec,
		"audio_codec": c.AudioCodec,
		"width":       c.Width,
		"height":      c.Height,
		"fps_num":     c.FPS.Num,
		"fps_den":     c.FPS.Den,
	})
	p.log.Debug("clip.render.prepare.phase",
		zap.String("subsystem", "cliprender_preparer"),
		zap.String("phase", "resolve_contract_done"),
		zap.String("run_id", runID),
		zap.String("contract_id", c.ID),
		zap.Int("width", c.Width),
		zap.Int("height", c.Height),
		zap.Int("fps_num", c.FPS.Num),
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
