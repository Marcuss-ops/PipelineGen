package script

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// LegacyGenerateWithImagesRequest is the deprecated request for
// POST /api/script/generate-with-images.
type LegacyGenerateWithImagesRequest struct {
	Topic         string   `json:"topic"`
	SourceText    string   `json:"source_text"`
	Title         string   `json:"title"`
	Language      string   `json:"language"`
	Tone          string   `json:"tone"`
	Model         string   `json:"model"`
	Style         string   `json:"style"`
	ClipIDs       []string `json:"clip_ids"`
	NumClips      int      `json:"num_clips"`
	TargetWords   int      `json:"target_words"`
	Duration      int      `json:"duration"`
	SegmentWords  int      `json:"segment_words"`
	SegmentTopics []string `json:"segment_topics"`
	SaveToDB      bool     `json:"save_to_db"`
	ForceRefresh  bool     `json:"force_refresh"`
	DriveFolderID string   `json:"drive_folder_id"`
	// Defaults to true for this legacy route so the Google Doc artifact is
	// visible alongside the generated scene images.
	GenerateDocument  *bool  `json:"generate_document,omitempty"`
	StyleInstructions string `json:"style_instructions"`
	VoiceoverGroup    string `json:"voiceover_group"`
	VoiceoverFolderID string `json:"voiceover_folder_id"`
	TranscriptPolicy  string `json:"transcript_policy"`
	PromptVersion     string `json:"prompt_version"`
}

func (r *LegacyGenerateWithImagesRequest) toEnvelope() domainScript.GenerationEnvelopeV2 {
	generateDocument := true
	if r.GenerateDocument != nil {
		generateDocument = *r.GenerateDocument
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
			GenerateDocument:    generateDocument,
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

// LegacyGenerateWithImages handles POST /api/script/generate-with-images.
// Removal target: 2026-12-31.
func (h *ScriptFlowHandler) LegacyGenerateWithImages(c *gin.Context) {
	addDeprecationHeader(c, "generate-with-images", removalDateWithImages)

	var req LegacyGenerateWithImagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload: " + err.Error()})
		return
	}

	if h.log != nil && strings.TrimSpace(req.TranscriptPolicy) != "" {
		h.log.Warn("legacy_field_best_effort",
			zap.String("field", "transcript_policy"),
			zap.String("endpoint", "generate-with-images"),
			zap.String("value", req.TranscriptPolicy),
			zap.String("note", "not fully enforced; 500-char transcript excerpt always used"))
	}

	h.enqueueEnvelope(c, req.toEnvelope())
}
