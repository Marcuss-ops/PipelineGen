package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// clipRichMetadata is the structured result from Ollama metadata generation.
type clipRichMetadata struct {
	ClipSummary      string   `json:"clip_summary"`
	Topics           []string `json:"topics"`
	Speakers         []string `json:"speakers"`
	MentionedPeople  []string `json:"mentioned_people"`
	SourceTags       []string `json:"source_tags"`
	ClipTags         []string `json:"clip_tags"`
	SearchKeywords   []string `json:"search_keywords"`
	People           []string `json:"people"`
	Hook             string   `json:"hook"`
	CleanTitle       string   `json:"clean_title"`
	ShortTitle       string   `json:"short_title"`
	CleanTranscript  string   `json:"clean_transcript"`
	EmbeddingText    string   `json:"embedding_text"`
	Tags             []string `json:"tags"`
	QualityScore     float64  `json:"quality_score"`
	SearchVisibility string   `json:"search_visibility"`
}

// generateClipMetadata generates rich metadata for a clip using Ollama.
// Returns clip_summary, topics, speakers, mentioned_people, source_tags,
// clip_tags, search_keywords, hook, clean_title, short_title.
func (s *Service) generateClipMetadata(ctx context.Context, title, transcript, description string) *clipRichMetadata {
	if s.ollama == nil {
		return nil
	}
	model := s.metadataMetadataModel()

	// Truncate inputs for the prompt
	if len(transcript) > 3000 {
		transcript = transcript[:3000]
	}

	prompt := fmt.Sprintf(`You are an assistant that generates rich metadata for a YouTube clip.
Analyze only the clip transcript below. Do not invent events from the description.
Use the title only as lightweight context for names/entities.

Title: %s
Transcript: %s

Return only JSON with these fields:
{
  "clip_summary": "2-3 sentence summary of the actual clip",
  "topics": ["concept 1", "concept 2"],
  "speakers": ["primary speaker", "host"],
  "mentioned_people": ["person mentioned", "another person"],
  "source_tags": ["show/channel tags tied to source"],
  "clip_tags": ["clip-specific concepts"],
  "search_keywords": ["short keyword phrases from the clip"],
  "hook": "the strongest spoken line from the clip",
  "clean_title": "specific clip title, not the whole video",
  "short_title": "short searchable title",
  "quality_score": 0.0
}

Rules:
- clip_summary must be faithful to the transcript only
- topics must be concepts or themes, not filler words
- speakers are the people actually speaking in the clip when inferable; clearly distinguish the main host/presenter from any guests or interviewees
- mentioned_people are people named in the clip, distinct from speakers
- source_tags should describe the show/channel/source, not the clip moment
- clip_tags should describe the specific moment or topic of the clip
- search_keywords should be short phrases actually useful for search
- hook should be the strongest line actually spoken in the clip
- clean_title should describe the clip-specific moment, not the whole video
- short_title should be concise and searchable
- quality_score must reflect narrative value, clarity, hook strength, completeness, and usefulness for search
- use a score from 0.0 to 1.0; strong clips should be 0.7+ and weak or incomplete clips should be below 0.5
- if the clip is short, incomplete, or low-signal, reduce specificity and quality
- Return ONLY the JSON object, no explanation`, title, transcript)

	s.log.Info("calling Ollama for clip metadata generation",
		zap.String("model", model),
		zap.Int("transcript_chars", len(transcript)))

	response, err := s.ollama.SimpleGenerate(ctx, model, prompt, 60*time.Second, nil)
	if err != nil {
		s.log.Warn("Ollama call failed for clip metadata", zap.Error(err))
		return nil
	}

	s.log.Info("Ollama returned metadata response",
		zap.Int("response_chars", len(response)))

	// Parse JSON response
	response = strings.TrimSpace(response)
	if response == "" {
		return nil
	}

	// Try to extract JSON from response (might be wrapped in markdown)
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start == -1 || end == -1 || end <= start {
		s.log.Warn("invalid JSON in ollama response for clip metadata")
		return nil
	}
	jsonStr := response[start : end+1]

	var result clipRichMetadata
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		s.log.Warn("failed to parse ollama JSON response for clip metadata", zap.Error(err))
		return fallbackClipRichMetadata(title, transcript, description)
	}

	normalized := normalizeClipRichMetadata(&result, title, transcript, description)
	return normalized
}

// metadataMetadataModel returns the model to use for metadata generation (tags, clip metadata).
func (s *Service) metadataMetadataModel() string {
	if s == nil || s.cfg == nil {
		return "gemma4:e2b"
	}
	model := strings.TrimSpace(s.cfg.External.OllamaMetadataModel)
	if model == "" {
		model = strings.TrimSpace(s.cfg.External.OllamaModel)
	}
	if model == "" {
		model = "gemma4:e2b"
	}
	return model
}
