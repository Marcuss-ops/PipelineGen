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
	StyleInstructions   string            `json:"style_instructions"`
	Guidelines          string            `json:"guidelines"`
	CustomPrompt        string            `json:"custom_prompt"`
	SystemPrompt        string            `json:"system_prompt"`
	VoiceoverGroup      string            `json:"voiceover_group"`
	VoiceoverFolderID   string            `json:"voiceover_folder_id"`
	TranscriptPolicy    string            `json:"transcript_policy"`
	PromptVersion       string            `json:"prompt_version"`
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
	// PR 1 (June 2026): clip-source precedence chain.
	//
	//   1. Explicit `clip_ids []string` wins if non-empty.
	//   2. Otherwise, IDs are extracted from the documented
	//      `clips: [{clip_id,...}]` array, in arrival order,
	//      skipping entries whose ClipID is empty.
	//   3. Otherwise, no clip selection → SourceText fallback
	//      (PR 3 turns this into HTTP 400 on the
	//      `generate-from-clips` semantic).
	//
	// Mixed / merged / deduplicated behaviour is intentionally
	// out of scope for PR 1 — see PR 2 for the union logic.
	clipIDs := r.ClipIDs
	if len(clipIDs) == 0 {
		clipIDs = make([]string, 0, len(r.Clips))
		for _, c := range r.Clips {
			if c.ClipID != "" {
				clipIDs = append(clipIDs, c.ClipID)
			}
		}
	}
	if len(clipIDs) > 0 {
		item.Source.Type = domainScript.SourceClips
		item.Source.ClipIDs = clipIDs
	}
	item.Source.NumClips = r.NumClips
	return domainScript.GenerationEnvelopeV2{
		Version: 2,
		Preset:  domainScript.PresetCustom,
		Items:   []domainScript.GenerationItemV2{item},
	}
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
