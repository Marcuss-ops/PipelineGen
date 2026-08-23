package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"go.uber.org/zap"
)

func (s *Service) GenerateClipMetadata(ctx context.Context, title, transcript, description string) *dto.CanonicalClipMetadata {
	if s.ollama == nil {
		return nil
	}
	model := s.metadataModel()

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
		zap.String("model", model), zap.Int("transcript_chars", len(transcript)))

	response, err := s.ollama.SimpleGenerate(ctx, model, prompt, 60*time.Second, nil)
	if err != nil {
		s.log.Warn("Ollama call failed for clip metadata", zap.Error(err))
		return nil
	}

	response = strings.TrimSpace(response)
	if response == "" {
		return nil
	}

	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start == -1 || end == -1 || end <= start {
		s.log.Warn("invalid JSON in ollama response for clip metadata")
		return nil
	}
	jsonStr := response[start : end+1]

	var result dto.CanonicalClipMetadata
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		s.log.Warn("failed to parse ollama JSON response for clip metadata", zap.Error(err))
		return tagutil.FallbackClipMetadata(title, transcript, description)
	}

	return tagutil.NormalizeClipMetadata(&result, title, transcript, description)
}

func (s *Service) metadataModel() string {
	model := strings.TrimSpace(s.cfg.OllamaMetadataModel)
	if model == "" {
		model = strings.TrimSpace(s.cfg.OllamaModel)
	}
	if model == "" {
		model = "gemma4:e2b"
	}
	return model
}
