package script

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// LegacyClipInput is one entry of the documented `clips` array of objects on
// the legacy /api/script/generate-from-clips request.
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

// LegacyGenerateFromClips handles POST /api/script/generate-from-clips.
// Removal target: 2026-12-31.
func (h *ScriptFlowHandler) LegacyGenerateFromClips(c *gin.Context) {
	addGenerateFromClipsDeprecationHeader(c, removalDateFromClips)

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

	for _, name := range req.resolveAliases() {
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

	h.warnIgnoredLegacyFields(&req)
	h.enqueueEnvelope(c, req.toEnvelope())
}
