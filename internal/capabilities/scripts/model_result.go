// model_result.go — rendered-output contract: GenerateResult,
// final references and pipeline metrics (split from model.go).
package scriptgeneration

import (
	"math"
	"strconv"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type GenerateResult struct {
	// renderFirstStartedAt/renderLastFinishedAt are runtime-only aggregation
	// state. They are deliberately not serialized; RenderMetrics exposes the
	// derived wall/work values.
	renderFirstStartedAt time.Time
	renderLastFinishedAt time.Time
	// Output is the canonical plain-text result projection. It is derived once
	// from the ordered scenes and is never independently generated.
	Output GenerateOutput `json:"output"`
	// Render is the requested clip materialization contract. It is mirrored
	// into the canonical SpecScene response so selected clip bindings and the
	// render options travel together.
	Render scriptpkg.VideoRenderSpec `json:"render,omitempty"`
	// SourceTrace is the durable retrieval trace. It is kept alongside the
	// capability result so the broker/API cannot lose the accepted Qdrant
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

	// Segments is the compatibility projection of the canonical VidRush
	// enrichment results. Each entry preserves insights.entities for legacy
	// consumers; it is never a second extraction source.
	Segments []scriptpkg.VidRushSegmentResult `json:"segments,omitempty"`

	// Artifacts contains compatibility projections derived from this result.
	Artifacts *GenerateArtifacts `json:"artifacts,omitempty"`

	// ResolvedScenes is the sealed technical projection consumed by canonical
	// timeline/audio/render compilation. Scenes remain editorial input.
	ResolvedScenes []ResolvedScene `json:"resolved_scenes,omitempty"`

	// CanonicalTimeline and AudioPlan are persisted so the renderer and
	// audio compiler consume the same timing decision rather than deriving
	// independent offsets on either side of the enqueue boundary.
	CanonicalTimeline *capabilityaudio.CanonicalTimeline `json:"canonical_timeline,omitempty"`
	AudioPlan         *capabilityaudio.CompiledAudioPlan `json:"audio_plan,omitempty"`

	// PhraseTimings is the deterministic per-scene phrase→timestamp
	// projection. Each entry anchors one script phrase to the canonical
	// word timing (local span) and the final combined timeline (global
	// span = the scene's canonical timeline offset + local span). It is a
	// read-only projection of the canonical SpeechTimingArtifact files —
	// recomputable and verifiable, never a source of truth.
	PhraseTimings []capabilityaudio.PhraseTiming `json:"phrase_timings,omitempty"`

	// SceneSpeechTimings is the scene-level speech timing projection: one
	// entry per scene that captured canonical word timing, bundling that
	// scene's word boundaries with its derived phrase spans. It is the
	// durable, document-facing mirror of PhraseTimings (the flat projection);
	// both are read-only projections of the canonical SpeechTimingArtifact
	// files, never a source of truth.
	SceneSpeechTimings []capabilityaudio.SceneSpeechTiming `json:"scene_speech_timings,omitempty"`

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
	// `broaden` / `drop_entity` / `skip_visual` retry). Nil without a plan.
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
	OverlayPlan *capabilityoverlay.OverlayPlan `json:"overlay_plan,omitempty"`
	// LocalizedOverlayPlans carries the equivalent plans for translated
	// voiceover languages. The source-language plan remains in OverlayPlan for
	// compatibility; these plans are keyed by their target language and contain
	// phrases and timing derived from that language's translated annotations and
	// certified speech timing.
	LocalizedOverlayPlans map[Language]*capabilityoverlay.OverlayPlan `json:"localized_overlay_plans,omitempty"`
	// PhraseOverlayBudget records the global editorial ceiling and the number
	// of unique grounded phrases materialized into OverlayPlan. It is result
	// telemetry and is deliberately excluded from the renderer wire contract.
	PhraseOverlayBudget *capabilityoverlay.PhraseOverlayBudget `json:"phrase_overlay_budget,omitempty"`
	// OverlayPlanIntake explains the sealed OverlayPlan against the run's
	// request: the budgets the canvas allowed, how many candidate items each
	// producer supplied, how many the plan kept, and which admission stage
	// removed the rest. It is result telemetry and is deliberately excluded
	// from the renderer wire contract, so it never changes render identity.
	OverlayPlanIntake *capabilityoverlay.OverlayPlanIntake `json:"overlay_plan_intake,omitempty"`

	// SemanticRenderBundle is the cross-stage audit contract assembled from
	// the same certified surfaces as OverlayPlan. It is a projection, never a
	// second extraction, resolver or timing source.
	SemanticRenderBundle *capabilityoverlay.SemanticRenderBundleV1 `json:"semantic_render_bundle,omitempty"`

	// OverlayRender is populated after the timing-frozen Chronon render has
	// completed and its media contract has been certified.
	OverlayRender *RenderReference `json:"overlay_render,omitempty"`
	// RemoteFinalJob is the durable receipt for an explicitly requested remote
	// final render, recorded by the publishing stage after successful handoff.
	RemoteFinalJob *RemoteFinalJobResult `json:"remote_final_job,omitempty"`
	// RemoteFinalJobHistory retains terminal Master receipts when a new remote
	// attempt is allocated for the same logical render.
	RemoteFinalJobHistory []RemoteFinalJobResult `json:"remote_final_job_history,omitempty"`
	// LocalizedOverlayRenders contains certified Chronon outputs for the
	// translated plans above. OverlayRender remains the source-language
	// compatibility projection.
	LocalizedOverlayRenders map[Language]RenderReference `json:"localized_overlay_renders,omitempty"`

	// OverlayIntents are the pre-timing entity→template bindings, created
	// immediately after entity extraction. Each intent binds one entity
	// occurrence to its resolved template without timing dependency,
	// enabling overlay.prepare to start template resolution and asset
	// prefetch in parallel with TTS. Empty when no entities were extracted.
	OverlayIntents []capabilityoverlay.OverlayIntent `json:"overlay_intents,omitempty"`
	// ResolvedOverlayIntents is the render-boundary projection of the same
	// intents after certified timing, preset and asset lowering. The original
	// OverlayIntents remains immutable for the parallel overlay.prepare stage.
	ResolvedOverlayIntents []capabilityoverlay.OverlayIntent `json:"resolved_overlay_intents,omitempty"`

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

	AudioMode          capabilityaudio.AudioMode           `json:"audio_mode,omitempty"`
	AudioStrategy      capabilityaudio.AudioRenderStrategy `json:"audio_strategy,omitempty"`
	FinalAudio         *FinalAudioReference                `json:"final_audio,omitempty"`
	AudioMetrics       *AudioPipelineMetrics               `json:"audio_metrics,omitempty"`
	TranslationMetrics *TranslationPipelineMetrics         `json:"translation_metrics,omitempty"`

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

	// AudioPrefetch carries pre-resolved audio assets (BGM/SFX paths +
	// clip audio materialization) populated by the P1.1 prefetch goroutine
	// during TTS. Nil means prefetch was not needed or not wired.
	//
	// It IS serialized: the runtime carriers inside it (resolved paths and
	// cache adapters) are json:"-", while the bounded summary (requested /
	// cached counts, wall time, per-asset outcomes) is what the polling API
	// and the evidence trail consume. Without it, a prefetch that silently
	// delivered nothing looked exactly like one that was never attempted.
	AudioPrefetch *AudioPrefetchResult `json:"audio_prefetch,omitempty"`

	// StageProgress is the parent-facing workflow-progress projection of THIS
	// run, keyed by job.StageName. It is the durable lane's counterpart of the
	// postprocessor map: the localized render fan-out has no postprocessor, so
	// without it the `render` stage would never reach a parent job or the run
	// result. Persisted with the run and projected by DurableResultToDomain.
	StageProgress map[string]job.StageProgress `json:"stage_progress,omitempty"`
}

type RenderMetrics struct {
	Expected   int   `json:"expected"`
	Successful int   `json:"successful"`
	Failed     int   `json:"failed"`
	WallMS     int64 `json:"wall_ms"`
	// WorkMS is the sum of child localized-render durations. It may exceed
	// WallMS when renders overlap and must not be used as wall-clock time.
	WorkMS        int64 `json:"work_ms,omitempty"`
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

// GenerationRun is the canonical aggregate that tracks a single
// execution of the script generation workflow. It is created BEFORE
// any external I/O (verdetto invariant: the POST handler creates
// the pipeline_run first).

// PhraseImpactResult is the kernel-owned extractive editorial summary contract.
// It is aliased here so capability callers keep the local name without a second
// divergent definition.
type PhraseImpactResult = scriptpkg.PhraseImpactResult

// PhraseImpactTimings is the worker's own stage breakdown, aliased for the
// same reason as PhraseImpactResult: callers keep the local name without a
// second divergent definition.
type PhraseImpactTimings = scriptpkg.PhraseImpactTimings

// ChartValueCandidate is a numeric label grounded as an exact source span by
// the VisualNER worker. The scripts layer re-checks Text with the strict chart
// parser before any value can authorize a chart.
type ChartValueCandidate struct {
	Text  string
	Value float64
}

// BulletCandidate is one VisualNER sentence bullet grounded as an exact
// source span. SentenceIndex is the worker's scene-local sentence number;
// the scripts layer re-maps it onto the Go sentence splitter before it can
// become an editorial bullet.
type BulletCandidate struct {
	ID, Kind, Evidence, Provenance string
	Text                           string
	SentenceIndex                  int
	Start                          int
	End                            int
	// Score is the semantic engine's grounded salience; it travels to
	// Phrase Impact so candidate ranking uses one shared priority function.
	Score float64
}

// SetSceneSemanticAnalyzer wires the unified semantic source. A batch-capable
// implementation analyses the whole script in one call; the per-category
// bullet/chart ports were retired with the cutover.
func (r *Runner) SetSceneSemanticAnalyzer(analyzer SceneSemanticAnalyzer) {
	if r != nil {
		r.sceneSemanticAnalyzer = analyzer
	}
}

// ── Visual chart values (merged from editorial_chart_values.go) ──
//
// Merged here to keep the registered scripts hotspot at its committed
// baseline: chart candidates refine the rendered-output contract owned by
// this file. No behaviour change.

// maxEditorialChartValues mirrors the editorial manifest validation limit.
const maxEditorialChartValues = 6

// verifyChartCandidate turns one VisualNER chart row into the verbatim
// source surface it authorizes. The worker frequently mirrors the dirty NLP
// entity surface ("dove si registra solamente l'18") instead of the number
// alone, so the strict parser is applied to the cleaned numeric core and a
// %/currency suffix is recovered from the scene text ("l'18%" → "18%").
// The returned surface must still occur verbatim in the scene text and carry
// the worker's own value; anything else is rejected, never coerced.
func verifyChartCandidate(candidate ChartValueCandidate, sceneText string) (string, float64, bool) {
	if candidate.Text == "" {
		return "", 0, false
	}
	if parsed, ok := capabilityoverlay.ParseChartValue(candidate.Text); ok &&
		strings.Contains(sceneText, candidate.Text) && sameChartValue(parsed, candidate.Value) {
		return candidate.Text, parsed, true
	}
	cleaned := cleanNumericOverlayValue(candidate.Text)
	if cleaned == "" {
		return "", 0, false
	}
	surface := cleaned
	if span, ok := findEntitySpan(sceneText, cleaned); ok {
		surface = recoverNumericSuffix(sceneText, cleaned, []scriptpkg.AnnotationSpan{span})
	}
	if !strings.Contains(sceneText, surface) {
		return "", 0, false
	}
	parsed, ok := capabilityoverlay.ParseChartValue(surface)
	if !ok || !sameChartValue(parsed, candidate.Value) {
		return "", 0, false
	}
	return surface, parsed, true
}

// mergeChartValues appends verified candidates to existing values, keeping
// the manifest's no-duplicate and maximum-count rules.
func mergeChartValues(existing []string, candidates []ChartValueCandidate, sceneText string) []string {
	out := append([]string(nil), existing...)
	seen := make(map[string]struct{}, len(out))
	for _, value := range out {
		if parsed, ok := capabilityoverlay.ParseChartValue(value); ok {
			seen[chartValueIdentity(parsed)] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		if len(out) >= maxEditorialChartValues {
			break
		}
		surface, parsed, ok := verifyChartCandidate(candidate, sceneText)
		if !ok {
			continue
		}
		id := chartValueIdentity(parsed)
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, surface)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sameChartValue(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(a))
}

// applyRustChartValues lets the Rust scene analysis authorize chart candidates
// without turning model output into data: only exact values already present in
// the scene's grounded number annotations survive, with their certified timing.
func applyRustChartValues(input *capabilityoverlay.SceneInput, highlights []scriptpkg.SceneHighlight, sceneID string, certified bool) {
	if input == nil {
		return
	}
	if !certified {
		input.ChartNumbers = []capabilityoverlay.TimedAnnotation{}
		return
	}
	for _, highlight := range highlights {
		if highlight.SceneID != sceneID {
			continue
		}
		if len(highlight.ChartValues) == 0 {
			input.ChartNumbers = []capabilityoverlay.TimedAnnotation{}
			return
		}
		allowed := make(map[string]struct{}, len(highlight.ChartValues))
		for _, value := range highlight.ChartValues {
			parsed, ok := capabilityoverlay.ParseChartValue(value)
			if !ok {
				continue
			}
			allowed[chartValueIdentity(parsed)] = struct{}{}
		}
		if len(allowed) < 2 {
			input.ChartNumbers = []capabilityoverlay.TimedAnnotation{}
			return
		}
		input.ChartNumbers = make([]capabilityoverlay.TimedAnnotation, 0, len(input.Numbers))
		// NLP often emits the same spoken number twice ("42" NUMBER and
		// "42%" PERCENT on one word span). Keep one bar per (value, start):
		// duplicates would render twin bars for a single spoken fact.
		seen := make(map[string]int, len(input.Numbers))
		for _, number := range input.Numbers {
			parsed, ok := capabilityoverlay.ParseChartValue(strings.TrimSpace(number.Text))
			if !ok {
				continue
			}
			if _, ok := allowed[chartValueIdentity(parsed)]; !ok {
				continue
			}
			key := chartValueIdentity(parsed) + "\x00" + strconv.FormatInt(number.StartUS, 10)
			if idx, duplicate := seen[key]; duplicate {
				// Keep the unit-carrying surface ("42%" over "42") so
				// the rendered label never loses the spoken unit.
				if len(number.Text) > len(input.ChartNumbers[idx].Text) {
					input.ChartNumbers[idx] = number
				}
				continue
			}
			seen[key] = len(input.ChartNumbers)
			input.ChartNumbers = append(input.ChartNumbers, number)
		}
		return
	}
	// No Rust scene record means no chart authorization for this scene.
	input.ChartNumbers = []capabilityoverlay.TimedAnnotation{}
}

func chartValueIdentity(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// ── Render references (merged from model_render.go) ──
//
// Merged here to keep model_render.go under max_lines_per_file_strict=600:
// render lineage is part of the rendered-output contract. No behaviour change.

type RenderReference struct {
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
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
	Manifest *capabilityoverlay.OverlayManifest `json:"manifest,omitempty"`
}

// OverlayItemRenderReference binds one semantic overlay item to the queue job
// and certified artifact that rendered it.
type OverlayItemRenderReference struct {
	ItemID   string          `json:"item_id"`
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	BatchID  string          `json:"batch_id,omitempty"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
}

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
}

// overlayArtifactForItem resolves the certified clip for a semantic item from
// either the legacy per-item projection or a fused batch reference.
func overlayArtifactForItem(ref *RenderReference, itemID string) (*RenderArtifact, string, bool) {
	if ref == nil {
		return nil, "", false
	}
	for _, item := range ref.Items {
		if item.ItemID == itemID && item.Artifact != nil {
			return item.Artifact, item.JobID, true
		}
	}
	for _, batch := range ref.Batches {
		for _, id := range batch.ItemIDs {
			if id == itemID && batch.Artifact != nil {
				return batch.Artifact, batch.JobID, true
			}
		}
	}
	return nil, "", false
}
