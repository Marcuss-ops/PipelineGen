package contract

import (
	"time"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptrunner "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runner"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

type GenerateResult struct {
	// LanguageDeliveries is populated on the parent run when one script.generate
	// request fans out into independent final videos, one durable child run per
	// requested target language.
	LanguageDeliveries []LanguageDeliveryResult `json:"language_deliveries,omitempty"`
	// Build provenance is captured once at the durable-result boundary so a
	// job report can prove which executable and Rust release produced it.
	BinarySHA256 string `json:"binary_sha256,omitempty"`
	RustRelease  string `json:"rust_release,omitempty"`
	// RustGitSHA and RustBinarySHA256 are the BrainRust worktree commit and the
	// installed semantic-engine digest, read from bin/BRAINRUST_RELEASE.json.
	// BinarySHA256 above is the Go executable; keeping the engine separate lets
	// a report prove which Rust planner actually produced the plan.
	RustGitSHA       string `json:"rust_git_sha,omitempty"`
	RustBinarySHA256 string `json:"rust_binary_sha256,omitempty"`
	// RenderFirstStartedAt/RenderLastFinishedAt are runtime-only aggregation
	// state. They are deliberately not serialized; RenderMetrics exposes the
	// derived wall/work values.
	RenderFirstStartedAt time.Time `json:"-"`
	RenderLastFinishedAt time.Time `json:"-"`
	// Aligner is the run-scoped Rust alignment seam. It is runtime-only
	// (never serialized) so localized entity timelines can reuse the same
	// worker that produced PhraseTimings.
	Aligner PhraseAligner `json:"-"`
	// Output is the canonical plain-text result projection. It is derived once
	// from the ordered scenes and is never independently generated.
	Output GenerateOutput `json:"output"`
	// Render is the requested clip materialization contract. It is mirrored
	// into the canonical SpecScene response so selected clip bindings and the
	// render options travel together.
	Render scriptpkg.VideoRenderSpec `json:"render,omitempty"`
	// SourceTrace is the durable retrieval trace. It is kept alongside the
	// capability result so the broker/API cannot lose the accepted pgvector
	// clip IDs between the durable runner and the legacy job envelope.
	SourceTrace scriptpkg.SourceTrace `json:"source,omitempty"`
	// Scenes is the ordered list of generated scenes.
	Scenes []Scene `json:"scenes"`

	// Summary and ranked editorial takeaways are extractive data products from
	// the narration. They do not alter scenes, TTS, or rendered captions.
	Summary         string                        `json:"summary,omitempty"`
	BulletPoints    []string                      `json:"bullet_points,omitempty"`
	HeavySentences  []scriptpkg.ImportantSentence `json:"heavy_sentences,omitempty"`
	ChapterManifest *scriptpkg.ChapterManifest    `json:"chapter_manifest,omitempty"`
	// SceneHighlights is the per-scene editorial product (editorial.v1).
	// Global Summary/BulletPoints above stay untouched for compatibility.
	SceneHighlights []scriptpkg.SceneHighlight   `json:"scene_highlights,omitempty"`
	Editorial       *scriptpkg.EditorialManifest `json:"editorial,omitempty"`
	// SemanticBindingIssues records plan hits that need acquisition or a
	// target before they can become annotation images. It is a diagnosis, not
	// a second binding path.
	SemanticBindingIssues []SemanticBindingIssue `json:"semantic_binding_issues,omitempty"`
	// EntityImageDecisionOwner names the one flow allowed to select and bind
	// entity images for this run. It is resolved from the requested surface,
	// never from which flow happened to find a hit first.
	EntityImageDecisionOwner EntityImageDecisionOwner `json:"entity_image_decision_owner,omitempty"`

	// Segments is the compatibility projection of the canonical VidRush
	// enrichment results. Each entry preserves insights.entities for legacy
	// consumers; it is never a second extraction source.
	Segments []scriptpkg.VidRushSegmentResult `json:"segments,omitempty"`
	// OverlayImages are explicit local still assets bound to overlay scenes;
	// they never enter stock-video resolution or provider selection.
	OverlayImages []scriptpkg.OverlayImageAsset `json:"overlay_images,omitempty"`

	// Artifacts contains compatibility projections derived from this result.
	Artifacts *GenerateArtifacts `json:"artifacts,omitempty"`

	// ResolvedScenes is the sealed technical projection consumed by canonical
	// timeline/audio/render compilation. Scenes remain editorial input.
	ResolvedScenes []ResolvedScene `json:"resolved_scenes,omitempty"`

	// AudioOutput is the audio-phase result contract: canonical timeline,
	// compiled plan, speech timings and certified final audio. It is embedded
	// so the durable result keeps its flat wire keys while contracts/audio
	// owns the fields.
	AudioOutput `json:",inline"`

	// Entities is the deterministic typed entity aggregate (persons /
	// places / concepts) projected from the run's VidRush segment
	// enrichment results. It is the durable-surface mirror of the legacy
	// Artifacts.Entities block: consumers read the typed fields and never
	// parse raw JSON. Nil when the incremental enrichment plane did not
	// run (or produced no entities).
	Entities *scriptpkg.EntityResult `json:"entities,omitempty"`

	// ScriptID is the canonical SQLite scripts-row identifier returned by
	// the single persistence port. Zero means persistence was not requested.
	ScriptID int64 `json:"script_id,omitempty"`

	// EntityTimeline is the canonical entity→timestamp projection: every
	// entity occurrence of the source language is anchored to the real word
	// timing of the voiceover actually used (audio boundaries come from the
	// SpeechTimingArtifact, never from text-length estimates) and mapped onto
	// the final combined timeline via the scene's canonical offset. It is
	// the SSOT the overlay resolver reads to place entity cards. Nil when no
	// scene carried both annotations and word timing.
	EntityTimeline *capabilityentities.EntityTimeline `json:"entity_timeline,omitempty"`

	// SemanticConsolidation is the script-level selective production view the
	// unified semantic worker returned in the same batch: canonical facts,
	// deduplicated compositions and explicit coverage reasons. It is the raw
	// planner input, never a second extraction. Nil when the worker or the
	// configuration predates the projection.
	SemanticConsolidation *SemanticVisualConsolidation `json:"semantic_consolidation,omitempty"`
	// SemanticProductionPlan is the executable projection of the consolidation:
	// which compositions this run produces, which asset is searched once and
	// reused, and which facts the run budget deferred. Nil when there was no
	// consolidation or it did not validate.
	SemanticProductionPlan *SemanticProductionPlan `json:"semantic_production_plan,omitempty"`
	// SemanticSearchFeedback reports the planner's retrieval outcomes in the
	// engine's handshake contract (`pending` / `satisfied` / `failed` plus a
	// `broaden` / `skip_visual` retry). Nil without a plan.
	SemanticSearchFeedback *SemanticSearchFeedback `json:"semantic_search_feedback,omitempty"`
	// SemanticSearchReport is the measured result of executing the plan's
	// queries against the real retrieval provider: every attempt with its
	// hit/miss and latency, the aggregate hit rate and the p50/p95 latency.
	// Nil when the batch planner is not wired or no composition searched.
	SemanticSearchReport *SemanticSearchBatchReport `json:"semantic_search_report,omitempty"`

	// OverlayPlan is the semantic overlay plan derived from the run's
	// certified timing surfaces (phrase timings + entity timeline + scene
	// annotations): IMPORTANT_PHRASE / IMPORTANT_WORD / IMAGE_OVERLAY /
	// PERSON / NUMBER / QUOTE / LOCATION / PRODUCT / LOGO, each terminating
	// in a canonical primitive (Text / Image / Video / Shape). It is the
	// semantic renderinggen.overlay-plan.v1 PipelineGen submits to the
	// RenderingGen queue; lowercasing to chronon.render-plan.v2 is owned by
	// RenderingGen. Nil when the run carried no derivable overlay surface.
	OverlayPlan *capmodel.OverlayPlan `json:"overlay_plan,omitempty"`
	// LocalizedOverlayPlans carries the equivalent plans for translated
	// voiceover languages. The source-language plan remains in OverlayPlan for
	// compatibility; these plans are keyed by their target language and contain
	// phrases and timing derived from that language's translated annotations and
	// certified speech timing.
	LocalizedOverlayPlans map[Language]*capmodel.OverlayPlan `json:"localized_overlay_plans,omitempty"`
	// PhraseOverlayBudget records the global editorial ceiling and the number
	// of unique grounded phrases materialized into OverlayPlan. It is result
	// telemetry and is deliberately excluded from the renderer wire contract.
	PhraseOverlayBudget *capabilityoverlay.PhraseOverlayBudget `json:"phrase_overlay_budget,omitempty"`
	// OverlayPlanIntake explains the sealed OverlayPlan against the run's
	// request: the budgets the canvas allowed, how many candidate items each
	// producer supplied, how many the plan kept, and which admission stage
	// removed the rest. It is result telemetry and is deliberately excluded
	// from the renderer wire contract, so it never changes render identity.
	OverlayPlanIntake *capmodel.OverlayPlanIntake `json:"overlay_plan_intake,omitempty"`

	// SemanticRenderBundle is the cross-stage audit contract assembled from
	// the same certified surfaces as OverlayPlan. It is a projection, never a
	// second extraction, resolver or timing source.
	SemanticRenderBundle *capabilityoverlay.SemanticRenderBundleV1 `json:"semantic_render_bundle,omitempty"`
	// OverlayAudit is the human-readable inventory of detected entities and
	// the semantic overlays admitted to the render plan. It is derived from
	// the same sealed bundle and plan; it does not perform a second extraction.
	OverlayAudit *OverlayAudit `json:"overlay_audit,omitempty"`
	// FinalJobPreflight is the durable marker that the image contract of the
	// sealed OverlayPlan was verified BEFORE the overlay render was submitted.
	// It is keyed by the plan's render digest, so a repaired or re-frozen plan
	// re-arms the gate; the late handoff checks reuse it instead of downloading
	// the same images twice.
	FinalJobPreflight *FinalJobPreflight `json:"final_job_preflight,omitempty"`

	// OverlayRender is populated after the timing-frozen Chronon render has
	// completed and its media contract has been certified.
	OverlayRender *RenderReference `json:"overlay_render,omitempty"`
	// RemoteFinalJob is the durable receipt for an explicitly requested remote
	// final render, recorded by the publishing stage after successful handoff.
	RemoteFinalJob *RemoteFinalJobResult `json:"remote_final_job,omitempty"`
	// RemoteFinalJobHistory retains terminal Master receipts when a new remote
	// attempt is allocated for the same logical render.
	RemoteFinalJobHistory []RemoteFinalJobResult `json:"remote_final_job_history,omitempty"`
	// PrepareJoin is result telemetry for the SceneTextReady fan-out join. It
	// records which branch (semantic or assets) was still running at the join
	// and how long each branch took, so the stage stops being opaque. It is
	// deliberately not part of render identity.
	PrepareJoin *scriptrunner.PrepareJoinBreakdown `json:"prepare_join,omitempty"`
	// LocalizedOverlayRenders contains certified Chronon outputs for the
	// translated plans above. OverlayRender remains the source-language
	// compatibility projection.
	LocalizedOverlayRenders map[Language]RenderReference `json:"localized_overlay_renders,omitempty"`
	// OverlayRenderHistory archives overlay renders that no longer bind to the
	// sealed plan (see RepairFinalJobOverlayCheckpoint). The artifacts stay
	// addressable for audit instead of being deleted, while OverlayRender stops
	// naming them, so nothing downstream can ship them by accident.
	OverlayRenderHistory []RenderReference `json:"overlay_render_history,omitempty"`

	// OverlayIntents are the pre-timing entity→template bindings, created
	// immediately after entity extraction. Each intent binds one entity
	// occurrence to its resolved template without timing dependency,
	// enabling overlay.prepare to start template resolution and asset
	// prefetch in parallel with TTS. Empty when no entities were extracted.
	OverlayIntents []capmodel.OverlayIntent `json:"overlay_intents,omitempty"`
	// ResolvedOverlayIntents is the render-boundary projection of the same
	// intents after certified timing, preset and asset lowering. The original
	// OverlayIntents remains immutable for the parallel overlay.prepare stage.
	ResolvedOverlayIntents []capmodel.OverlayIntent `json:"resolved_overlay_intents,omitempty"`

	// EditingTimeline is the canonical editing projection built from frozen
	// facts (CanonicalTimeline + FinalAudio + OverlayPlan + EntityTimeline).
	// It is the single JSON document downstream editing consumes; no
	// component maintains a second independently calculated timeline.
	EditingTimeline *EditingTimelineV1 `json:"editing_timeline,omitempty"`

	// Title is the output title (mirrors GenerateRequest.Title).
	Title string `json:"title,omitempty"`
	// SourceLanguage identifies which overlay/render projections use the
	// compatibility source-language fields.
	SourceLanguage Language `json:"source_language,omitempty"`

	// OutputName is the caller-specified output name.
	OutputName string `json:"output_name,omitempty"`

	// WordCount is the generated BODY word count. It excludes DisplayText and
	// all text/content attached to protected fixed-media scenes; those remain
	// part of the timeline/specscene surface only.
	WordCount      int    `json:"word_count"`
	VoiceoverGroup string `json:"voiceover_group,omitempty"`

	// LocalFinalVideo is the job's own MP4, joined from the certified
	// per-item overlay renders on the plan timeline and muxed with the
	// certified final audio. It is the local-lane counterpart of
	// RemoteFinalVideo (a Master render receipt): this run produced the
	// pixels and this run joined them, so a render-enabled job is never
	// delivered as audio only.
	LocalFinalVideo    *LocalFinalVideoArtifact    `json:"local_final_video,omitempty"`
	TranslationMetrics *TranslationPipelineMetrics `json:"translation_metrics,omitempty"`

	// LocalizedRenders are the certified produced videos of the localized
	// render fan-out: one entry per successfully rendered + uploaded video
	// with its asset id, sha256, and Drive identity. Nil when the fan-out
	// produced no certified video.
	LocalizedRenders []LocalizedRenderResult `json:"localized_renders,omitempty"`
	// LocalizedRenderStaged contains locally certified RENDERED artifacts
	// whose Drive publication has not yet been confirmed. It is the durable
	// hand-off point used by resume: these entries must be uploaded without
	// invoking Chronon again.
	LocalizedRenderStaged   []LocalizedRenderResult  `json:"localized_render_staged,omitempty"`
	LocalizedRenderFailures []LocalizedRenderFailure `json:"localized_render_failures,omitempty"`
	RenderMetrics           *RenderMetrics           `json:"render_metrics,omitempty"`
	ExpectedRenderCount     int                      `json:"expected_render_count,omitempty"`

	// StageProgress is the parent-facing workflow-progress projection of THIS
	// run, keyed by job.StageName. It is the durable lane's counterpart of the
	// postprocessor map: the localized render fan-out has no postprocessor, so
	// without it the `render` stage would never reach a parent job or the run
	// result. Persisted with the run and projected by DurableResultToDomain.
	StageProgress map[string]job.StageProgress `json:"stage_progress,omitempty"`
}

// LanguageDeliveryResult is the parent-run index for one completed target.
// The complete script, timeline, and render receipt remain in the child run.
type LanguageDeliveryResult struct {
	Language    string `json:"language"`
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	ScriptText  string `json:"script_text,omitempty"`
	RemoteJobID string `json:"remote_job_id,omitempty"`
	ArtifactURL string `json:"artifact_url,omitempty"`
	ArtifactSHA string `json:"artifact_sha256,omitempty"`
}

type RenderMetrics struct {
	Expected   int   `json:"expected"`
	Successful int   `json:"successful"`
	Failed     int   `json:"failed"`
	WallMS     int64 `json:"wall_ms"`
	// WorkMS is the sum of child localized-render work spans (admission →
	// publication). It may exceed WallMS when renders overlap and must not be
	// used as wall-clock time. A render's wait for a slot is NOT work: it is
	// accumulated separately in QueueWaitMS.
	WorkMS int64 `json:"work_ms,omitempty"`
	// QueueWaitMS is the summed admission wait of the localized renders
	// (submission → render slot). It is the part of the lane that a wider
	// concurrency (or a shorter queue) can remove, and the reason WorkMS is
	// smaller than the old "call start → return" sum on a saturated lane.
	QueueWaitMS   int64 `json:"queue_wait_ms,omitempty"`
	MaterializeMS int64 `json:"materialize_ms,omitempty"`
	RenderMS      int64 `json:"render_ms,omitempty"`
	UploadMS      int64 `json:"upload_ms,omitempty"`
	CPUFallbacks  int   `json:"cpu_fallbacks,omitempty"`
	GPUOOMs       int   `json:"gpu_ooms,omitempty"`
	Concurrency   int   `json:"concurrency,omitempty"`
}

// TranslationPipelineMetrics is a read-only API projection of canonical
// translation operations. The RunReport/observability database is the only
// measurement authority.
type TranslationPipelineMetrics struct {
	Calls       int   `json:"calls"`
	Concurrency int   `json:"concurrency"`
	WallMS      int64 `json:"wall_ms"`
}

type RenderReference struct {
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
	// CacheHits and RenderedItems split the per-item overlay renders behind
	// this reference into work reused from the content-addressed cache and
	// work that paid for a new Chronon render. The pair is the per-run
	// evidence that reuse is actually working; zero/zero is a legacy
	// reference that predates the counters.
	CacheHits     int `json:"cache_hits,omitempty"`
	RenderedItems int `json:"rendered_items,omitempty"`
	// PlanDigest names the EXACT plan version these bytes were rendered from
	// (OverlayPlanRenderDigest). It is the identity that makes reuse safe: a
	// reference whose digest differs from the current plan describes different
	// pixels and must never satisfy a resume, a cache hit or a handoff. Empty
	// means the checkpoint predates digest binding, so the caller must prove
	// compatibility from the content itself before reusing it.
	PlanDigest string `json:"plan_digest,omitempty"`
	// Items carries the per-overlay render lineage when production renders
	// each semantic item as its own short video. Artifact remains the first
	// item for backward-compatible document/publication consumers.
	Items []OverlayItemRenderReference `json:"items,omitempty"`
	// Batches records fused windows. Items retain their editorial identity and
	// point back to one certified composite artifact when several items share a
	// render.
	Batches []OverlayBatchRenderReference `json:"batches,omitempty"`
	// Manifest is the P5 overlay manifest (item_id → track/in_us/out_us/
	// content-hash) that indexes a single certified timeline artifact. It is
	// the manifest+timeline render path's identity document and receipt:	// non-nil only when this reference was produced by that path.
	Manifest *capmodel.OverlayManifest `json:"manifest,omitempty"`
}

// OverlayItemRenderReference binds one semantic overlay item to the queue job
// and certified artifact that rendered it. It is the kernel wire type, aliased
// so this capability keeps its historical spelling.
type OverlayItemRenderReference = renderqueue.OverlayItemRenderReference

// OverlayBatchRenderReference binds one certified artifact to the semantic
// items and frame window it composites. Times use the plan's canonical
// integer-microsecond clock.
type OverlayBatchRenderReference struct {
	BatchID  string          `json:"batch_id"`
	SceneID  string          `json:"scene_id,omitempty"`
	ItemIDs  []string        `json:"item_ids"`
	StartUS  int64           `json:"start_us"`
	EndUS    int64           `json:"end_us"`
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
	// Manifest preserves the certified per-batch timing and packed mapping.
	// A split reference cannot safely reconstruct these facts from item rows.
	Manifest *capmodel.OverlayManifest `json:"manifest,omitempty"`
}
