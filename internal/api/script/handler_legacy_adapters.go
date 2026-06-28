// Package script — handler_legacy_adapters.go provides thin adapter handlers
// for legacy script-generation routes that have been superseded by
// POST /api/script/generate (unified endpoint, PR6).
//
// Each legacy handler:
//   1. Binds the deprecated JSON request shape
//   2. Translates it to a canonical GenerationEnvelopeV2 via toEnvelope()
//   3. Enqueues the envelope as a script.generate job
//   4. Adds X-Deprecated: true response header with concrete removal date
//   5. Increments the per-route prometheus.CounterVec
//
// Legacy routes registered here:
//   - POST /api/script/generate-from-clips   → unified pipeline  (removal: 2026-12-31)
//   - POST /api/script/generate-with-images  → unified pipeline  (removal: 2026-12-31)
//   - POST /api/script/generate-batch        → unified pipeline  (removal: 2026-09-30)
//   - POST /api/script/curate                → unified pipeline  (removal: 2026-09-30)
//
// PR 11 (June 2026): created as part of the legacy-route deprecation wave.
// P0.7 (June 2026): replaced atomic.Int64 with prometheus.CounterVec;
// eliminated double-default in LegacyCurate; established concrete removal dates.

package script

import (
	"fmt"
	"net/http"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Deprecation metrics ────────────────────────────────────────────────

// legacyRouteInvocationsTotal tracks how many times each deprecated
// route has been invoked since process start. Operators can expose
// this via GET /metrics or admin dashboards to identify clients that
// haven't migrated to the unified endpoint.
// P0.7 (June 2026): replaced atomic.Int64 with prometheus.CounterVec.
var legacyRouteInvocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "legacy_route_invocations_total",
	Help: "Monotonic counter for deprecated script-generation route invocations, by route name.",
}, []string{"route"})

// DeprecationCount returns the cumulative invocation count across all
// 4 legacy routes by reading the legacyRouteInvocationsTotal prometheus
// counter (dto.Metric writeback pattern).
func DeprecationCount() int64 {
	var total int64
	for _, route := range []string{"generate-from-clips", "generate-with-images", "generate-batch", "curate"} {
		counter, err := legacyRouteInvocationsTotal.GetMetricWithLabelValues(route)
		if err != nil {
			continue
		}
		var m dto.Metric
		if err := counter.Write(&m); err != nil {
			continue
		}
		total += int64(m.GetCounter().GetValue())
	}
	return total
}

// addDeprecationHeader injects X-Deprecated: true and
// X-Deprecation-Notice into the response header, then increments
// the per-route prometheus counter. Call from every legacy adapter handler.
// P0.7 (June 2026): accepts route + removalDate; uses per-route CounterVec.
func addDeprecationHeader(c *gin.Context, route string, removalDate string) {
	legacyRouteInvocationsTotal.WithLabelValues(route).Inc()
	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Notice",
		"POST /api/script/generate is the canonical endpoint. "+
			"This route will be removed on "+removalDate+".")
}

// ── Removal dates ─────────────────────────────────────────────────────
// Established P0.7 (June 2026):
//   - generate-from-clips  / generate-with-images: 6-month grace (2026-12-31)
//   - generate-batch       / curate:               3-month grace (2026-09-30)
// Reasoning: batch and curate are lower-usage routes historically;
// from-clips and with-images are the original entry points, still
// used by external API consumers.

const (
	removalDateFromClips  = "2026-12-31"
	removalDateWithImages = "2026-12-31"
	removalDateBatch      = "2026-09-30"
	removalDateCurate     = "2026-09-30"
)

// ── Legacy request types ────────────────────────────────────────────────
//
// These types mirror the old per-endpoint request shapes so existing
// API consumers don't need to change their payloads. Each type has
// only the fields necessary for translation to GenerationEnvelopeV2.

// LegacyClipInput is one entry of the documented `clips` array of
// objects on the legacy /api/script/generate-from-clips request.
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
	EnableSceneImages   bool              `json:"enable_scene_images,omitempty"`
	SentencesPerImage   int               `json:"sentences_per_image,omitempty"`
	MinQualityScore     float64           `json:"min_quality_score,omitempty"`
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
// canonical GenerationEnvelopeV2.
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
	clipIDs, _ := r.deriveClipIDs()
	if len(clipIDs) > 0 {
		item.Source.Type = domainScript.SourceClips
		item.Source.ClipIDs = clipIDs
	}
	item.Source.NumClips = r.NumClips
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

// deriveClipIDs is the precedence + ordered-union + dedup core.
func (r *LegacyGenerateFromClipsRequest) deriveClipIDs() (out []string, derived int) {
	total := len(r.ClipIDs) + len(r.Clips)
	if total == 0 {
		return nil, 0
	}
	seen := make(map[string]struct{}, total)
	out = make([]string, 0, total)
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
	if len(out) == 0 {
		return nil, 0
	}
	return out, derived
}

// resolveAliases rewrites PR 4's documented alias fields.
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
			SaveToDB:            r.SaveToDB,
			GenerateSceneImages: true,
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

// toEnvelope translates a legacy batch request.
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
	Search            bool     `json:"search"`
	AllowTextOnly     bool     `json:"allow_text_only"`
	HintClipIDs       []string `json:"hint_clip_ids"`
}

// toEnvelope translates a legacy curate request.
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
			Search:          r.Search,
			AllowTextOnly:   r.AllowTextOnly,
			ClipIDs:         r.HintClipIDs,
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
// Removal target: 2026-12-31.
func (h *ScriptFlowHandler) LegacyGenerateFromClips(c *gin.Context) {
	addDeprecationHeader(c, "generate-from-clips", removalDateFromClips)

	var req LegacyGenerateFromClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}

	clipIDs, derived := req.deriveClipIDs()
	if len(clipIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "generate-from-clips requires at least one clip_id or one clips[] entry",
		})
		return
	}

	aliases := req.resolveAliases()
	for _, name := range aliases {
		h.log.Warn("legacy_alias_used",
			zap.String("alias", name),
			zap.String("endpoint", "generate-from-clips"),
		)
	}

	if derived > 0 {
		h.log.Info("legacy_adapter: derived clip_ids from clips array",
			zap.Int("derived", derived), zap.Int("total", len(clipIDs)),
		)
	}

	env := req.toEnvelope()
	h.enqueueDeprecated(c, env)
}

// LegacyGenerateWithImages handles POST /api/script/generate-with-images.
// Removal target: 2026-12-31.
func (h *ScriptFlowHandler) LegacyGenerateWithImages(c *gin.Context) {
	addDeprecationHeader(c, "generate-with-images", removalDateWithImages)

	var req LegacyGenerateWithImagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}
	env := req.toEnvelope()
	h.enqueueDeprecated(c, env)
}

// LegacyGenerateBatch handles POST /api/script/generate-batch.
// Removal target: 2026-09-30.
func (h *ScriptFlowHandler) LegacyGenerateBatch(c *gin.Context) {
	addDeprecationHeader(c, "generate-batch", removalDateBatch)

	var req LegacyGenerateBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}
	env := req.toEnvelope()
	h.enqueueDeprecated(c, env)
}

// LegacyCurate handles POST /api/script/curate (deprecated — use
// POST /api/script/generate with source.type=curate).
// Removal target: 2026-09-30.
func (h *ScriptFlowHandler) LegacyCurate(c *gin.Context) {
	addDeprecationHeader(c, "curate", removalDateCurate)

	var req LegacyCurateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}

	if req.Query == "" {
		req.Query = req.Title
	}
	if req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "query is required"})
		return
	}

	env := req.toEnvelope()
	h.enqueueDeprecated(c, env)
}

// enqueueDeprecated is the shared enqueue path for all legacy routes.
func (h *ScriptFlowHandler) enqueueDeprecated(c *gin.Context, env domainScript.GenerationEnvelopeV2) {
	if err := env.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid envelope: " + err.Error()})
		return
	}

	if h.jobsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "jobs service not initialized"})
		return
	}

	req := jobs.NewGenerateEnqueueRequest(env)
	enqueuedJob, err := jobs.EnqueueGenerationJob(c.Request.Context(), h.jobsSvc, req, h.log)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	resp := GenerateResponse{}
	resp.async(enqueuedJob.ID, string(enqueuedJob.Status), "/api/jobs/"+enqueuedJob.ID+"/full", "")
	c.JSON(http.StatusOK, resp)
}
