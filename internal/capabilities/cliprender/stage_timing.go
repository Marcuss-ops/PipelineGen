// Package cliprender — stage_timing.go owns the canonical observability
// STAGE names for the clip.render pipeline.
//
// These names are the STAGE dimension (business phase boundaries) recorded
// on the kernel RunReport. The clip.render worker is strictly sequential
// (preparer → subtitle compile → renderer → probe → overlay compositor →
// publisher), so each stage's wall time IS its critical-path contribution
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

import kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

const (
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
	StageClipRender kernobs.StageName = "clip.render"
	// StageClipProbe is the post-render byte certification (probe + exact
	// contract validation). Part of the render-side serial chain.
	StageClipProbe kernobs.StageName = "clip.probe"
	// StageClipOverlay is the overlay compositing pass (resolve segment +
	// blend). Recorded only when the request declares an overlay.
	StageClipOverlay kernobs.StageName = "clip.overlay"
	// StageClipPublish is the Drive publication + asset commit boundary —
	// the clip.render "drive" phase, distinct from the render-side probe and
	// overlay stages that precede it.
	StageClipPublish kernobs.StageName = "clip.publish"
)
