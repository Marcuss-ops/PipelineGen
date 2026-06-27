// Package script — handler_legacy_adapters.go provides adapter handlers
// for legacy script-generation routes that have been superseded by
// POST /api/script/generate (unified endpoint, PR6).
//
// Each legacy handler:
//   1. Binds the deprecated JSON request shape
//   2. Translates it to a canonical GenerationEnvelopeV2
//   3. Enqueues the envelope as a script.generate job
//   4. Adds X-Deprecated: true response header
//   5. Increments the atomic deprecation counter
//
// Legacy routes registered here:
//   - POST /api/script/generate-from-clips   → unified pipeline
//   - POST /api/script/generate-with-images  → unified pipeline (preset=with_images)
//   - POST /api/script/generate-batch        → unified pipeline (multi-item)
//   - POST /api/script/curate                → unified pipeline (query→catalog source)
//
// PR 11 (June 2026): created as part of the legacy-route deprecation wave.
// These adapters will be removed in a future PR once all clients have migrated
// to POST /api/script/generate.

package script

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Deprecation metrics ────────────────────────────────────────────────

// deprecationCounter tracks how many times deprecated routes have been
// invoked since process start. Operators can expose this via
// GET /metrics or admin dashboards to identify clients that haven't
// migrated to the unified endpoint.
var deprecationCounter atomic.Int64

// DeprecationCount returns the current value of the global deprecation
// counter. Thread-safe; can be called from any goroutine.
func DeprecationCount() int64 {
	return deprecationCounter.Load()
}

// addDeprecationHeader injects X-Deprecated: true and
// X-Deprecation-Notice into the response header, then increments
// the global counter. Call from every legacy adapter handler.
func addDeprecationHeader(c *gin.Context) {
	deprecationCounter.Add(1)
	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Notice",
		"POST /api/script/generate is the canonical endpoint. "+
			"This route will be removed in a future release.")
}

// ── Legacy request types ────────────────────────────────────────────────
//
// These types mirror the old per-endpoint request shapes so existing
// API consumers don't need to change their payloads. Each type has
// only the fields necessary for translation to GenerationEnvelopeV2.

// LegacyClipInput is one entry of the documented `clips` array of
// objects on the legacy /api/script/generate-from-clips request.
//
// Only ClipID is required to drive SourceClips selection. Title and
// URL are accepted (omitempty) to keep the wire shape aligned with
// the public documentation; the generation pipeline does not
// consume them in PR 1 (no reader wired yet — reserved for a
// future diagnostics / audit-log pass).
//
// PR 1 (June 2026): the documented `clips` array was previously
// silently dropped because the legacy request type did not declare
// it, which caused clients sending the documented payload shape
// to fall through to SourceText. This type is the canonical
// representation of one entry on that array.
type LegacyClipInput struct {
	ClipID string `json:"clip_id"`
	Title  string `json:"title,omitempty"`
	URL    string `json:"url,omitempty"`
}

// LegacyGenerateFromClipsRequest is the deprecated request for
// POST /api/script/generate-from-clips.
type LegacyGenerateFromClipsRequest struct {
	Topic               string            `json:"topic"`
	SourceText          string            `json:"source_text"`
	Title               string            `json:"title"`
	Language            string            `json:"language"`
	Tone                string            `json:"tone"`
	Model               string            `json:"model"`
	Style               string            `json:"style"`
	ClipIDs             []string          `json:"clip_ids"`
	Clips               []LegacyClipInput `json:"clips"`
	IntroClipIDs        []string          `json:"intro_clip_ids"`
	IntroClips          []string          `json:"intro_clips"`
	NumClips            int               `json:"num_clips"`
	TargetWords         int               `json:"target_words"`
	Duration            int               `json:"duration"`
	SegmentWords        int               `json:"segment_words"`
	SegmentTopics       []string          `json:"segment_topics"`
	SaveToDB            bool              `json:"save_to_db"`
	ForceRefresh        bool              `json:"force_refresh"`
	GenerateSceneImages bool              `json:"generate_scene_images"`
	GenerateVoiceover   bool              `json:"generate_voiceover"`
	GenerateDocument    bool              `json:"generate_document"`
	GenerateDoc         bool              `json:"generate_doc"`
	ExtractEntities     bool              `json:"extract_entities"`
	GenerateMetadata    bool              `json:"generate_metadata"`
	DriveFolderID       string            `json:"drive_folder_id"`

	// ── PR 4 (June 2026): alias fields accepted by the
	//    documented payload but previously dropped on the
	//    floor because the legacy struct didn't declare them.
	//    Each field has a canonical counterpart that the
	//    handler / toEnvelope routes through:
	//      * EnableSceneImages → Output.GenerateSceneImages
	//      * SentencesPerImage  → ScriptSpec.SentencesPerImage
	//      * MinQualityScore    → SourceSpec.MinQualityScore
	//    All three are TEMPORARY compatibility shims: the
	//    cutover PR (#9, when all known clients migrate to
	//    /api/script/generate) will remove these in lock-step
	//    with the X-Deprecated handler path. Tests assert the
	//    `legacy_alias_used` warn emission for ops-migration
	//    visibility.
	EnableSceneImages bool    `json:"enable_scene_images,omitempty"`
	SentencesPerImage int     `json:"sentences_per_image,omitempty"`
	MinQualityScore   float64 `json:"min_quality_score,omitempty"`

	StyleInstructions string `json:"style_instructions"`
	Guidelines        string `json:"guidelines"`
	CustomPrompt      string `json:"custom_prompt"`
	SystemPrompt      string `json:"system_prompt"`
	VoiceoverGroup    string `json:"voiceover_group"`
	VoiceoverFolderID string `json:"voiceover_folder_id"`
	TranscriptPolicy  string `json:"transcript_policy"`
	PromptVersion     string `json:"prompt_version"`
}

// toEnvelope translates a legacy generate-from-clips request into a
// canonical GenerationEnvelopeV2. Clips present → SourceClips;
// topic-only → SourceText.
func (r *LegacyGenerateFromClipsRequest) toEnvelope() domainScript.GenerationEnvelopeV2 {
	introIDs := append([]string(nil), r.IntroClipIDs...)
	introIDs = append(introIDs, r.IntroClips...)

	guidelines := r.StyleInstructions
	if guidelines == "" {
		guidelines = r.Guidelines
	}
	if guidelines == "" {
		guidelines = r.CustomPrompt
	}
	if guidelines == "" {
		guidelines = r.SystemPrompt
	}

	item := domainScript.GenerationItemV2{
		ID:       r.Title,
		Title:    r.Title,
		Language: r.Language,
		Tone:     r.Tone,
		Model:    r.Model,
		Style:    r.Style,
		Source: domainScript.SourceSpec{
			Type:             domainScript.SourceText,
			Topic:            r.Topic,
			SourceText:       r.SourceText,
			Guidelines:       guidelines,
			TranscriptPolicy: r.TranscriptPolicy,
			ForceRefresh:     r.ForceRefresh,
			IntroClipIDs:     introIDs,
		},
		ScriptParams: domainScript.ScriptSpec{
			TargetWords:   r.TargetWords,
			Duration:      r.Duration,
			PromptVersion: r.PromptVersion,
			ForceRefresh:  r.ForceRefresh,
			SegmentWords:  r.SegmentWords,
			SegmentTopics: append([]string(nil), r.SegmentTopics...),
		},
		Output: domainScript.OutputSpec{
			ExtractEntities:     r.ExtractEntities,
			GenerateMetadata:    r.GenerateMetadata,
			GenerateVoiceover:   r.GenerateVoiceover,
			GenerateSceneImages: r.GenerateSceneImages,
			GenerateDocument:    r.GenerateDocument || r.GenerateDoc,
			SaveToDB:            r.SaveToDB,
			VoiceoverGroup:      r.VoiceoverGroup,
			VoiceoverFolderID:   r.VoiceoverFolderID,
			DriveFolderID:       r.DriveFolderID,
		},
	}
	// PR 2 (June 2026): clip-source resolution delegates to
	// deriveClipIDs() which implements the full union + dedup
	// chain (clip_ids first, then clips[] in arrival order,
	// both phases deduplicated against the running set). The
	// documented `clips: [{clip_id,...}]` array (newly accepted
	// in PR 1) is now merged with the explicit `clip_ids` slice
	// when both are present.
	clipIDs, _ := r.deriveClipIDs()
	if len(clipIDs) > 0 {
		item.Source.Type = domainScript.SourceClips
		item.Source.ClipIDs = clipIDs
	}
	item.Source.NumClips = r.NumClips

	// PR 4 (June 2026): documented alias fields that the legacy
	// adapter previously dropped on the floor. Resolve them
	// into the canonical Output / ScriptSpec / SourceSpec slots
	// BEFORE materialising the envelope:
	//   * ResolveAliases has already mutated r so that
	//     r.GenerateSceneImages reflects any EnableSceneImages
	//     alias contribution; that flows into
	//     item.Output.GenerateSceneImages below.
	//   * SentencesPerImage is pass-through to
	//     ScriptSpec.SentencesPerImage (already a field on
	//     domainScript.ScriptSpec).
	//   * MinQualityScore, if non-zero, becomes a typed
	//     *float64 on SourceSpec.MinQualityScore (already a
	//     field on domainScript.SourceSpec).
	item.ScriptParams.SentencesPerImage = r.SentencesPerImage
	if r.MinQualityScore != 0 {
		score := r.MinQualityScore
		item.Source.MinQualityScore = &score
	}
	return domainScript.GenerationEnvelopeV2{
		Version: 2,
		Preset:  domainScript.PresetCustom,
		Items:   []domainScript.GenerationItemV2{item},
	}
}

// deriveClipIDs is the precedence + ordered-union + dedup core
// for /api/script/generate-from-clips. PR 2 (June 2026).
//
// Returns:
//   - out:      the final ordered slice that should populate
//     SourceSpec.ClipIDs (deduplicated across both
//     input sources).
//   - derived:  how many of those IDs originated in r.Clips
//     (the legacy `clips` array of objects) rather
//     than r.ClipIDs — the handler reads it to emit
//     the audit log entry.
//
// Nil-return contract surface — BOTH paths below MUST return
// `(nil, 0)` so callers can rely on a nil return meaning
// "no clip selection" without distinguishing the reason:
//
//  1. `total == 0` at the top of the function — both inputs
//     literally empty (e.g. caller sent `{}` or sent only a
//     topic).//   2. `len(out) == 0` after both loops — inputs were
//     non-empty but every entry was whitespace-only (PR 3
//     strings.TrimSpace in both phases drops them). The
//     all-whitespace case is the only realistic trigger for
//     this second nil-return path: "duplicate against `seen`"
//     cannot independently cause `len(out) == 0` because a
//     duplicate requires a prior successful append in the
//     same pass (otherwise `seen` is empty). Restores the
//     nil-vs-`[]string{}` asymmetry that PR 2's review
//     explicitly preferred.
//
// Precedence chain:
//
//  1. Explicit `clip_ids` slice is appended first, in arrival
//     order. Duplicates encountered within the same slice are
//     collapsed.
//  2. Legacy `clips[]` array entries are appended afterwards,
//     again in arrival order. Duplicates against the running
//     set (either from clip_ids or earlier in clips[]) are
//     silently dropped; each successful append increments
//     `derived`.
//
// Empty `clip_id` strings are silently skipped in both phases.
//
// PR 1 (June 2026): the precedence-only logic was inline in
// toEnvelope(). PR 2 supersedes it: the helper now owns both
// the resolution AND the audit-count side channel, so the
// handler can emit `legacy_adapter: derived N clip_ids from
// clips array` without duplicating the iteration over the
// request payload.
//
// Implementation note: dedup is implemented with an inline
// `map[string]struct{}` so arrival order is preserved. The
// stdlib `pkg/sliceutil.UniqueStrings` family does NOT
// preserve order (it iterates the map) and was rejected for
// that reason; consider extracting this helper into
// `pkg/sliceutil.OrderedUnique` if it shows up in a third place.
func (r *LegacyGenerateFromClipsRequest) deriveClipIDs() (out []string, derived int) {
	// Prefer nil over an empty non-nil slice so callers can
	// distinguish "no clip selection" from "empty selection
	// arrived and was fully filtered". The empty_inputs_give_zero
	// table test in handler_legacy_adapters_test.go relies on
	// this asymmetry.
	total := len(r.ClipIDs) + len(r.Clips)
	if total == 0 {
		return nil, 0
	}
	seen := make(map[string]struct{}, total)
	out = make([]string, 0, total)

	// PR 3 (June 2026): whitespace-only IDs are normalised via
	// strings.TrimSpace before the empty-check AND stored in
	// trimmed form, so PR 3's 400 guard rejects
	// `clip_ids:[" "]`, `clips:[{clip_id:"\t"}]`, etc. as
	// effectively empty. Without this, the strict `id == ""`
	// check from PR 2 is a loophole: a payload of all-whitespace
	// IDs would survive the helper, len(clipIDs) would still be
	// > 0, and the PR 3 400 would never fire — silently
	// degrading to whatever source-type toEnvelope chose. The
	// trim happens before the dedup `seen` lookup so callers
	// can no longer use multi-whitespace forms of the same ID
	// to inflate the audit-log `derived` count. Note: the
	// TRIMMED form is what gets stored in `out` \u2014 callers
	// sending `"  X  "` and `"X"` will see one entry, not two.
	for _, id := range r.ClipIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	// Phase 2 — legacy `clips[]`. Each successful append counts
	// toward `derived` so the handler can attribute clip-id
	// provenance in the audit log.
	for _, c := range r.Clips {
		cid := strings.TrimSpace(c.ClipID)
		if cid == "" {
			continue
		}
		if _, dup := seen[cid]; dup {
			continue
		}
		seen[cid] = struct{}{}
		out = append(out, cid)
		derived++
	}

	// PR 3-followup: when both loops produced no surviving
	// entries (all-whitespace input that survived the early
	// `total > 0` guard but produced no real IDs), return nil
	// instead of `[]string{}` so callers can rely on a nil
	// return meaning "no clip selection" regardless of whether
	// the input was literally empty or all-whitespace. The
	// PR 3 handler guard still rejects both via `len == 0` —
	// this post-loop nil return is purely a contract-cleanliness
	// fix for the helper itself.
	if len(out) == 0 {
		return nil, 0
	}
	return out, derived
}

// resolveAliases rewrites PR 4's documented alias fields onto the
// canonical ones in-place AND returns the list of alias names
// recognised in the payload. The handler iterates the returned
// list to emit `legacy_alias_used` warn entries. Both effects
// happen in one pass so the handler doesn't have to re-walk the
// fields.
//
// Alias precedence chain:
//
//  1. `enable_scene_images` is the documented alias for
//     `generate_scene_images`. If both are sent, the canonical
//     `GenerateSceneImages` is left as-is (canonical wins) and
//     the alias name is still reported — operators can see
//     adoption of the alias shape even on correctly-shaped
//     payloads. If only the alias is sent, `GenerateSceneImages`
//     is set to true (the documented alias still drives the
//     behaviour).
//
//  2. `sentences_per_image` has no collision on this legacy
//     request type (no existing SentencesPerImage field) and is
//     a pass-through to ScriptSpec.SentencesPerImage inside
//     toEnvelope; the alias name is reported whenever the
//     field is non-zero, which matches the `omitempty` JSON
//     boundary.
//
//  3. `min_quality_score` has no collision on this legacy
//     request type (the curate flow's `MinScore` is the
//     curate-only field, distinct from this alias) and is a
//     pass-through to SourceSpec.MinQualityScore inside
//     toEnvelope when non-zero. Reported whenever non-zero
//     matches the `omitempty` boundary.
//
// PR 4 is a temporary compatibility layer: the cutover PR (#9,
// when all known clients migrate to /api/script/generate) will
// remove these alias fields, the warn-emitting handler block,
// and the X-Deprecated header path in lock-step.
func (r *LegacyGenerateFromClipsRequest) resolveAliases() []string {
	var aliases []string
	if r.EnableSceneImages {
		aliases = append(aliases, "enable_scene_images")
		if !r.GenerateSceneImages {
			r.GenerateSceneImages = true
		}
	}
	if r.SentencesPerImage != 0 {
		aliases = append(aliases, "sentences_per_image")
	}
	if r.MinQualityScore != 0 {
		aliases = append(aliases, "min_quality_score")
	}
	return aliases
}

// LegacyGenerateWithImagesRequest is the deprecated request for
// POST /api/script/generate-with-images.
type LegacyGenerateWithImagesRequest struct {
	Topic             string   `json:"topic"`
	SourceText        string   `json:"source_text"`
	Title             string   `json:"title"`
	Language          string   `json:"language"`
	Tone              string   `json:"tone"`
	Model             string   `json:"model"`
	Style             string   `json:"style"`
	ClipIDs           []string `json:"clip_ids"`
	NumClips          int      `json:"num_clips"`
	TargetWords       int      `json:"target_words"`
	Duration          int      `json:"duration"`
	SegmentWords      int      `json:"segment_words"`
	SegmentTopics     []string `json:"segment_topics"`
	SaveToDB          bool     `json:"save_to_db"`
	ForceRefresh      bool     `json:"force_refresh"`
	DriveFolderID     string   `json:"drive_folder_id"`
	StyleInstructions string   `json:"style_instructions"`
	VoiceoverGroup    string   `json:"voiceover_group"`
	VoiceoverFolderID string   `json:"voiceover_folder_id"`
	TranscriptPolicy  string   `json:"transcript_policy"`
	PromptVersion     string   `json:"prompt_version"`
}

// toEnvelope translates a legacy generate-with-images request.
// The "with_images" preset forces scene_images + voiceover ON,
// entities + metadata OFF regardless of request fields.
func (r *LegacyGenerateWithImagesRequest) toEnvelope() domainScript.GenerationEnvelopeV2 {
	item := domainScript.GenerationItemV2{
		ID:       r.Title,
		Title:    r.Title,
		Language: r.Language,
		Tone:     r.Tone,
		Model:    r.Model,
		Style:    r.Style,
		Source: domainScript.SourceSpec{
			Type:             domainScript.SourceText,
			Topic:            r.Topic,
			SourceText:       r.SourceText,
			Guidelines:       r.StyleInstructions,
			TranscriptPolicy: r.TranscriptPolicy,
			ForceRefresh:     r.ForceRefresh,
		},
		ScriptParams: domainScript.ScriptSpec{
			TargetWords:   r.TargetWords,
			Duration:      r.Duration,
			PromptVersion: r.PromptVersion,
			ForceRefresh:  r.ForceRefresh,
			SegmentWords:  r.SegmentWords,
			SegmentTopics: append([]string(nil), r.SegmentTopics...),
		},
		Output: domainScript.OutputSpec{
			// PR 8 (June 2026): with_images preset enables ONLY
			// scene_images (set explicitly below). Voiceover,
			// document, entities, and metadata are caller-controlled
			// — the canonical precedence chain caller > preset >
			// config > safety resolves them via ApplyPreset +
			// applyConfigDefaults + applySafetyDefaults when the
			// use case executes.
			//
			// The pre-PR-8 adapter hardcoded these to a fixed
			// "with_images = always voiceover+document, never
			// entities+metadata" recipe. That fight-callers fix
			// made the preset a no-op for any caller that wanted
			// the opposite shape. PR 8 removes the hardcoding.
			SaveToDB:            r.SaveToDB,
			GenerateSceneImages: true, // sole preset responsibility
			VoiceoverGroup:      r.VoiceoverGroup,
			VoiceoverFolderID:   r.VoiceoverFolderID,
			DriveFolderID:       r.DriveFolderID,
		},
	}
	if len(r.ClipIDs) > 0 {
		item.Source.Type = domainScript.SourceClips
		item.Source.ClipIDs = r.ClipIDs
	}
	item.Source.NumClips = r.NumClips
	return domainScript.GenerationEnvelopeV2{
		Version: 2,
		Preset:  domainScript.PresetWithImages,
		Items:   []domainScript.GenerationItemV2{item},
	}
}

// ── Legacy batch types ─────────────────────────────────────────────────

// LegacyBatchItem is a single topic in a deprecated batch request.
type LegacyBatchItem struct {
	Topic      string `json:"topic"`
	SourceText string `json:"source_text"`
}

// LegacyBatchTopic mirrors LegacyBatchItem for the batch_topics field.
type LegacyBatchTopic struct {
	Topic      string `json:"topic"`
	SourceText string `json:"source_text"`
}

// LegacyGenerateBatchRequest is the deprecated request for
// POST /api/script/generate-batch.
type LegacyGenerateBatchRequest struct {
	DocTitle            string             `json:"doc_title"`
	DriveFolderID       string             `json:"drive_folder_id"`
	Language            string             `json:"language"`
	Tone                string             `json:"tone"`
	Duration            int                `json:"duration"`
	Model               string             `json:"model"`
	PromptVersion       string             `json:"prompt_version"`
	EditorPromptVersion string             `json:"editor_prompt_version"`
	QAPromptVersion     string             `json:"qa_prompt_version"`
	SaveToDB            bool               `json:"save_to_db"`
	Style               string             `json:"style"`
	Items               []LegacyBatchItem  `json:"items"`
	BatchTopics         []LegacyBatchTopic `json:"batch_topics"`
	ForceRefresh        bool               `json:"force_refresh"`
}

// toEnvelope translates a legacy batch request into a multi-item
// GenerationEnvelopeV2. Each item/topic becomes a text-source
// generation item.
//
// PR 8 (June 2026): the previous adapter passed `r.Duration` as
// both TargetWords AND Duration, conflating time and word count.
// That was the historical legacy contract: callers sent a duration
// in seconds and the adapter treated it as a target_word count
// (which then leaked into the LLM prompt). PR 8 calls for clean
// separation:
//
//   - TargetWords left at 0 (the normalizer derives it from
//     Duration via the canonical ~150 wpm formula in
//     generation_normalizer.go::applyConfigDefaults)
//   - Duration carries r.Duration as-is
//
// The handler adapter no longer applies defaults (PR 8: non
// applicare default dentro l'handler); the normalizer owns the
// precedence chain caller > preset > config > safety.
func (r *LegacyGenerateBatchRequest) toEnvelope() domainScript.GenerationEnvelopeV2 {
	topics := r.Items
	if len(topics) == 0 {
		for _, bt := range r.BatchTopics {
			topics = append(topics, LegacyBatchItem{Topic: bt.Topic, SourceText: bt.SourceText})
		}
	}
	items := make([]domainScript.GenerationItemV2, 0, len(topics))
	for i, t := range topics {
		itemID := fmt.Sprintf("%d", i+1)
		title := t.Topic
		if title == "" {
			title = t.SourceText
		}
		if title == "" && r.DocTitle != "" {
			title = fmt.Sprintf("%s #%d", r.DocTitle, i+1)
		}
		items = append(items, domainScript.GenerationItemV2{
			ID:       itemID,
			Title:    title,
			Language: r.Language,
			Tone:     r.Tone,
			Model:    r.Model,
			Style:    r.Style,
			Source: domainScript.SourceSpec{
				Type:       domainScript.SourceText,
				Topic:      t.Topic,
				SourceText: t.SourceText,
			},
			ScriptParams: domainScript.ScriptSpec{
				// PR 8: TargetWords is 0; normalizer derives it from
				// Duration via ~150 wpm. The handler does NOT apply
				// defaults — that contract belongs to the normalizer.
				TargetWords:   0,
				Duration:      r.Duration,
				PromptVersion: r.PromptVersion,
				ForceRefresh:  r.ForceRefresh,
			},
			Output: domainScript.OutputSpec{
				SaveToDB:         r.SaveToDB,
				GenerateDocument: true,
				DriveFolderID:    r.DriveFolderID,
			},
		})
	}
	return domainScript.GenerationEnvelopeV2{
		Version: 2,
		Preset:  domainScript.PresetCustom,
		Items:   items,
	}
}

// ── Legacy curate adapter ───────────────────────────────────────────────

// LegacyCurateRequest is the deprecated request for
// POST /api/script/curate.
//
// PR 4 (June 2026): extended with the three SourceCurate fields the
// user spec demanded: Search (opt-in to semantic search leg),
// AllowTextOnly (legacy text-only fallback opt-in), and
// HintClipIDs (caller-seeded clip list). All three map cleanly into
// SourceSpec on the unified SourceCurate path; previously these were
// handled by the legacy MediaCurator's bespoke credentialing.
type LegacyCurateRequest struct {
	Query             string   `json:"query"`
	Title             string   `json:"title"`
	Language          string   `json:"language"`
	Tone              string   `json:"tone"`
	Model             string   `json:"model"`
	MaxClips          int      `json:"max_clips"`
	SelectableClips   int      `json:"selectable_clips"`
	TargetWords       int      `json:"target_words"`
	MaxCharsPerScene  int      `json:"max_chars_per_scene"`
	MinScore          float64  `json:"min_score"`
	Source            string   `json:"source"`
	MediaType         string   `json:"media_type"`
	Type              string   `json:"type"`
	Style             string   `json:"style"`
	StyleInstructions string   `json:"style_instructions"`
	ForceRefresh      bool     `json:"force_refresh"`
	Languages         []string `json:"languages"`
	VoiceoverGroup    string   `json:"voiceover_group"`
	VoiceoverFolderID string   `json:"voiceover_folder_id"`
	GenerateVoiceover bool     `json:"generate_voiceover"`
	DriveFolderID     string   `json:"drive_folder_id"`
	// PR 4 (June 2026): SourceCurate credentials.
	Search        bool     `json:"search"`
	AllowTextOnly bool     `json:"allow_text_only"`
	HintClipIDs   []string `json:"hint_clip_ids"`
}

// toEnvelope translates a legacy curate request into a
// GenerationEnvelopeV2.
//
// PR 4 (June 2026): legacy /curate now routes through SourceCurate
// (unified resolver) instead of SourceCatalog. Mapping:
//   - Query             → src.Query
//   - MaxClips          → src.MaxClips
//   - MinScore          → src.MinQualityScore (typed *float64)
//   - HintClipIDs       → src.ClipIDs (caller-seeded clip list)
//   - Source            → src.SourceFilter
//   - MediaType         → src.MediaTypeFilter
//   - AllowTextOnly     → src.AllowTextOnly
//   - Search=true       → src.Search
//
// ResolutionContext (Language, Tone, Model, Style, TargetWords) is
// derived from item-level fields (Title, Language, Tone, Model,
// Style, ScriptParams.TargetWords); the curate resolver reads from
// SourceResolutionContext, never from SourceSpec.Guidelines.
//
// SourceSpec.Guidelines is intentionally empty here — the previous
// path put StyleInstructions there, which historically caused the
// curate resolver to hijack Guidelines as the language. The fix
// keeps Guidelines preserving editorial style on the *text* path
// (SourceText editorial overrides) while the curate flow reads
// Style from SourceResolutionContext.Style explicitly.
func (r *LegacyCurateRequest) toEnvelope() domainScript.GenerationEnvelopeV2 {
	item := domainScript.GenerationItemV2{
		ID:       r.Title,
		Title:    r.Title,
		Language: r.Language,
		Tone:     r.Tone,
		Model:    r.Model,
		Style:    r.Style,
		Source: domainScript.SourceSpec{
			Type:            domainScript.SourceCurate,
			Query:           r.Query,
			MaxClips:        r.MaxClips,
			SourceFilter:    r.Source,
			MediaTypeFilter: r.MediaType,
			ForceRefresh:    r.ForceRefresh,
			// PR 4 (June 2026): SourceCurate credentials mapped
			// verbatim from the legacy request. Search is the
			// opt-in for the semantic-search leg via
			// ClipSearchPort; AllowTextOnly gates the legacy
			// text-only fallback (ErrCurateNoClips surfaces
			// otherwise); HintClipIDs is caller-seeded clip
			// list merged with search hits.
			Search:        r.Search,
			AllowTextOnly: r.AllowTextOnly,
			ClipIDs:       r.HintClipIDs,
		},
		ScriptParams: domainScript.ScriptSpec{
			TargetWords:  r.TargetWords,
			ForceRefresh: r.ForceRefresh,
		},
		Output: domainScript.OutputSpec{
			SaveToDB:          true,
			GenerateVoiceover: r.GenerateVoiceover,
			GenerateDocument:  true,
			VoiceoverGroup:    r.VoiceoverGroup,
			VoiceoverFolderID: r.VoiceoverFolderID,
			DriveFolderID:     r.DriveFolderID,
			Languages:         r.Languages,
		},
	}
	// MinScore → quality threshold
	if r.MinScore > 0 {
		score := r.MinScore
		item.Source.MinQualityScore = &score
	}
	return domainScript.GenerationEnvelopeV2{
		Version: 2,
		Preset:  domainScript.PresetCustom,
		Items:   []domainScript.GenerationItemV2{item},
	}
}

// ── Adapter handlers ────────────────────────────────────────────────────

// LegacyGenerateFromClips handles POST /api/script/generate-from-clips.
func (h *ScriptFlowHandler) LegacyGenerateFromClips(c *gin.Context) {
	addDeprecationHeader(c)

	var req LegacyGenerateFromClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}

	// PR 3 (June 2026): clip-source guard. The
	// `generate-from-clips` endpoint contract requires at
	// least one clip identifier — explicit `clip_ids` or a
	// `clips[]` entry. PR 1 (precedence) and PR 2 (union +
	// dedup) built the resolution chain; together they
	// previously let a topic-only payload reach toEnvelope()
	// and silently degrade to SourceText via the
	// "neither clip_ids nor clips[]" branch. PR 3 closes that
	// gap: deriveClipIDs must return at least one non-empty
	// ID; otherwise reject with HTTP 400 BEFORE touching
	// toEnvelope, the audit log, or jobs.
	//
	// Note: the X-Deprecated header is added by
	// addDeprecationHeader(c) above (BEFORE BindJSON), so even
	// a payload rejected at PR 3's guard preserves the audit
	// marker — operators can still grep for clients probing
	// the deprecated endpoint with malformed/missing clips.
	//
	// The other legacy endpoints (generate-with-images,
	// generate-batch, curate) keep their SourceText fallback
	// because they are not clip-only — see the PR 1-followup
	// for the parallel extension to generate-with-images.
	clipIDs, derived := req.deriveClipIDs()
	if len(clipIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "generate-from-clips requires at least one clip_id or one clips[] entry",
		})
		return
	}

	// PR 4 (June 2026): documented alias field resolution.
	// resolveAliases mutates r so the canonical
	// `GenerateSceneImages` is set when the documented
	// `enable_scene_images` alias was sent. The returned list
	// drives the per-alias `legacy_alias_used` warn emission
	// below so operators can identify which clients rely on
	// the documented (but deprecated) field shape before the
	// cutover PR (#9) removes these shims. Resolved AFTER the
	// PR 3 guard so rejected payloads do not generate spurious
	// warn noise (the alias shape wasn't the actual problem
	// for those). Note: SentencesPerImage and MinQualityScore
	// are pass-throughs that flow through toEnvelope without
	// mutation; resolveAliases still reports them so the warn
	// fires for adoption tracking.
	aliases := req.resolveAliases()
	for _, name := range aliases {
		h.log.Warn("legacy_alias_used",
			zap.String("alias", name),
			zap.String("endpoint", "generate-from-clips"),
		)
	}

	// PR 2 (June 2026): audit log — when the legacy `clips[]`
	// array actually contributed new IDs to the resolution,
	// emit the canonical
	// `legacy_adapter: derived N clip_ids from clips array`
	// entry so operators can attribute clip-id provenance
	// (clients on the documented `clips[]` shape vs. the legacy
	// `clip_ids: []string` shape) in retrospective audits.
	// deriveClipIDs returns both the resolved slice and the
	// count attributed to the legacy array.
	//
	// Note: deriveClipIDs is invoked twice — once above for
	// the PR 3 guard + audit, once inside toEnvelope(). The
	// work is O(n) and n is bounded by the number of clips
	// per request (~tens), so the second pass is intentional
	// and cheaper than threading the resolved slice through
	// toEnvelope's signature (which would diverge from the
	// parallel toEnvelope methods on the other legacy request
	// types in this file).
	if derived > 0 {
		h.log.Info("legacy_adapter: derived clip_ids from clips array",
			zap.Int("derived", derived),
			zap.Int("total", len(clipIDs)),
		)
	}

	env := req.toEnvelope()
	h.enqueueDeprecated(c, env)
}

// LegacyGenerateWithImages handles POST /api/script/generate-with-images.
func (h *ScriptFlowHandler) LegacyGenerateWithImages(c *gin.Context) {
	addDeprecationHeader(c)

	var req LegacyGenerateWithImagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}
	env := req.toEnvelope()
	h.enqueueDeprecated(c, env)
}

// LegacyGenerateBatch handles POST /api/script/generate-batch.
func (h *ScriptFlowHandler) LegacyGenerateBatch(c *gin.Context) {
	addDeprecationHeader(c)

	var req LegacyGenerateBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}
	env := req.toEnvelope()
	h.enqueueDeprecated(c, env)
}

// LegacyCurate handles POST /api/script/curate (deprecated — use
// POST /api/script/generate with source.type=catalog).
func (h *ScriptFlowHandler) LegacyCurate(c *gin.Context) {
	addDeprecationHeader(c)

	var req LegacyCurateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}

	// Apply defaults matching the old Curate handler.
	if req.Query == "" {
		req.Query = req.Title
	}
	if req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "query is required"})
		return
	}
	if req.Title == "" {
		if req.VoiceoverGroup != "" {
			req.Title = req.VoiceoverGroup
		} else {
			req.Title = req.Query
		}
	}
	if req.Language == "" {
		req.Language = "en"
	}
	if req.Tone == "" {
		req.Tone = "comedy"
	}
	if req.MaxClips <= 0 {
		req.MaxClips = 10
	}
	if req.MaxClips > 30 {
		req.MaxClips = 30
	}
	if req.TargetWords <= 0 {
		req.TargetWords = 2000
	}
	if req.MinScore <= 0 {
		req.MinScore = 0.5
	}

	// Normalize languages (parity with old handler).
	langs := make([]string, 0, len(req.Languages))
	for _, l := range req.Languages {
		if t := strings.TrimSpace(l); t != "" {
			langs = append(langs, t)
		}
	}
	req.Languages = langs

	env := req.toEnvelope()
	h.enqueueDeprecated(c, env)
}

// enqueueDeprecated is the shared enqueue path for all legacy routes.
// Validates the translated envelope, enqueues via EnqueueGenerationJob,
// and returns an async response with the deprecation header already set.
func (h *ScriptFlowHandler) enqueueDeprecated(c *gin.Context, env domainScript.GenerationEnvelopeV2) {
	if err := env.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid envelope: " + err.Error()})
		return
	}

	if h.jobsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "jobs service not initialized"})
		return
	}

	req := usecase.NewGenerateEnqueueRequest(env)
	enqueuedJob, err := usecase.EnqueueGenerationJob(c.Request.Context(), h.jobsSvc, req, h.log)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	resp := GenerateResponse{}
	resp.async(enqueuedJob.ID, string(enqueuedJob.Status), "/api/jobs/"+enqueuedJob.ID+"/full", "")
	c.JSON(http.StatusOK, resp)
}
