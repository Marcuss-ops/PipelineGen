package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
)

// Payload is the semantic metadata output from the Python tagger.
// Same structure as images.SemanticMetadataPayload — one format, no duplication.
type Payload struct {
	AssetID             string           `json:"asset_id,omitempty"`
	AssetType           string           `json:"asset_type"`
	SemanticTier        string           `json:"semantic_tier"`
	Source              string           `json:"source"`
	MediaType           string           `json:"media_type"`
	Generator           string           `json:"generator"`
	PromptOriginal      string           `json:"prompt_original"`
	SemanticDescription string           `json:"semantic_description"`
	SearchText          string           `json:"search_text"`
	Subjects            []string         `json:"subjects"`
	SubjectSlugs       []string         `json:"subject_slugs"`
	Tags                []string         `json:"tags"`
	Categories          []string         `json:"categories"`
	Mood                []string         `json:"mood,omitempty"`
	Style               []string         `json:"style"`
	Confidence          float64          `json:"confidence"`
	EmbeddingStatus     string           `json:"embedding_status"`
	CreatedAt           string           `json:"created_at"`
	Assets              []map[string]any `json:"assets,omitempty"`
}

// Tagger calls the Python semantic_tagger.py script and returns a Payload.
// mediaType can be "image", "video", "audio", or "voiceover".
func Tagger(ctx context.Context, scriptsDir, prompt, style, mediaType, generator string) (*Payload, error) {
	scriptPath := filepath.Join(scriptsDir, "semantic_tagger.py")
	args := []string{
		scriptPath,
		"--prompt", prompt,
		"--style", style,
		"--media-type", mediaType,
		"--generator", generator,
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("semantic_tagger failed: %w (output: %s)", err, string(output))
	}

	var payload Payload
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("decode semantic_tagger output: %w", err)
	}

	return &payload, nil
}
