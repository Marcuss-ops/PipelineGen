// Package cliprender — stage_timing.go owns the canonical observability
// STAGE names for the clip.render pipeline.
//
// These names are the STAGE dimension (business phase boundaries) recorded
// on the kernel RunReport. The clip.render worker is strictly sequential
// (preparer → subtitle compile → renderer → probe → publisher), so each
// stage's wall time IS its critical-path contribution
// within the job: the RunReport breakdown orders them by start time and the
// benchmark derives the per-phase critical path from exactly these stages.
// The external technical calls (rust.render_clip, drive upload, ...) are
// recorded separately as OperationReport observations so a stage wall time
// is never confused with an accumulated dependency time.
//
// Stages are recorded with kernobs.RecordStage using the worker's own
// measured anchors (the worker owns the clock for its phases — the same
// anchors already feed metrics_v2), never with a second ad-hoc timer.
package cliprender

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

const (
	// StageClipDestinationResolve is the one-time Drive leaf-folder
	// resolution (root + destination.subfolder_name → resolved leaf) that
	// runs before preparation when the request names a script/batch
	// subfolder. Recorded only when destination.subfolder_name is set — a
	// request publishing into a pre-resolved folder has no resolution stage.
	StageClipDestinationResolve kernobs.StageName = "clip.destination_resolve"
	// StageClipPrepare is the parallel preparation phase (asset resolution +
	// materialization + transcript lookup/reuse). Hosts the transcript
	// generation (ASR) work when a fresh transcript is required.
	StageClipPrepare kernobs.StageName = "clip.prepare"
	// StageClipSubtitles is the deterministic ASS compile phase (burn-in
	// artifact). Recorded only when subtitles are enabled.
	StageClipSubtitles kernobs.StageName = "clip.subtitles"
	// StageClipRender is the RenderingGen/Chronon render boundary (queue +
	// single-pass render). Its wall includes the whole renderer port call;
	// the chronon.render_clip operation carries the accumulated work.
	StageClipRender kernobs.StageName = kernobs.StageName(job.TypeClipRender)
	// StageClipProbe is the post-render byte certification (probe + exact
	// contract validation). Part of the render-side serial chain.
	StageClipProbe kernobs.StageName = "clip.probe"
	// NOTE: there is no overlay stage. A declared overlay is resolved BEFORE
	// the plan is sealed and composited inside the render pass (a timed video
	// layer), so its cost is part of StageClipRender — the historical
	// post-render blend stage was demolished with the second-transcode path.
	// StageClipPublish is the Drive publication + asset commit boundary —
	// the clip.render "drive" phase, distinct from the render-side probe
	// stage that precedes it.
	StageClipPublish kernobs.StageName = "clip.publish"
)
