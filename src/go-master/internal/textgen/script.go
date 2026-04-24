package textgen

import (
	"context"
	"fmt"
	"strings"
)

// ScriptRequest contains the parameters for script generation.
type ScriptRequest struct {
	Topic       string   `json:"topic" binding:"required"`
	Duration    int      `json:"duration"`
	Language    string   `json:"language"`
	Tone        string   `json:"tone"`
	Keywords    []string `json:"keywords"`
	Structure   []string `json:"structure"`
	UseGPU      bool     `json:"use_gpu"`
	TargetWords int      `json:"target_words"`
	Model       string   `json:"model"`
}

// ScriptResult is the JSON response returned by script generation endpoints.
type ScriptResult struct {
	Ok          bool   `json:"ok"`
	Script      string `json:"script"`
	WordCount   int    `json:"word_count"`
	EstDuration int    `json:"est_duration"`
	Model       string `json:"model"`
}

// GenerateScript builds a prompt, generates a script, and returns the formatted response.
func (g *Generator) GenerateScript(ctx context.Context, req *ScriptRequest) (*ScriptResult, error) {
	if req == nil {
		return nil, fmt.Errorf("script request is required")
	}

	normalized := normalizeScriptRequest(req)
	prompt := buildScriptPrompt(normalized)

	result, err := g.GenerateText(ctx, &GenerationRequest{
		Provider:    ProviderOllama,
		Model:       normalized.Model,
		Prompt:      prompt,
		Temperature: 0.7,
		MaxTokens:   normalized.TargetWords * 2,
		UseGPU:      normalized.UseGPU,
	})
	if err != nil {
		return nil, err
	}

	wordCount := len(strings.Fields(result.Text))
	return &ScriptResult{
		Ok:          true,
		Script:      result.Text,
		WordCount:   wordCount,
		EstDuration: int(float64(wordCount) * 60 / 140),
		Model:       result.Model,
	}, nil
}

func normalizeScriptRequest(req *ScriptRequest) *ScriptRequest {
	out := *req
	out.Topic = strings.TrimSpace(out.Topic)
	if out.Duration <= 0 {
		out.Duration = 60
	}
	if strings.TrimSpace(out.Language) == "" {
		out.Language = "english"
	}
	if strings.TrimSpace(out.Tone) == "" {
		out.Tone = "professional"
	}
	if out.TargetWords <= 0 {
		out.TargetWords = (out.Duration * 140) / 60
	}
	if strings.TrimSpace(out.Model) == "" {
		out.Model = "gemma3:4b"
	}
	return &out
}

func buildScriptPrompt(req *ScriptRequest) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("You are an expert script writer. Create a %s script about: %s.\n\n", req.Tone, req.Topic))
	b.WriteString(fmt.Sprintf("Language: %s\n", req.Language))
	b.WriteString(fmt.Sprintf("Target length: approximately %d words\n", req.TargetWords))
	if len(req.Keywords) > 0 {
		b.WriteString(fmt.Sprintf("Keywords to include naturally: %s\n", strings.Join(req.Keywords, ", ")))
	}
	if len(req.Structure) > 0 {
		b.WriteString(fmt.Sprintf("Structure: %s\n", strings.Join(req.Structure, ", ")))
	}
	b.WriteString("\nWrite only the script text. Keep it engaging, natural, and suitable for narration.")
	return b.String()
}
