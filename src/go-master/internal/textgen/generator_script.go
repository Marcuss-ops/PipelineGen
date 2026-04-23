package textgen

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// GenerateScript generates a structured script for video creation
func (g *Generator) GenerateScript(ctx context.Context, req *ScriptRequest) (*ScriptResult, error) {
	if req.TargetLength == 0 {
		req.TargetLength = 500 // Default 500 words
	}
	if req.Language == "" {
		req.Language = "en"
	}
	if req.Tone == "" {
		req.Tone = "professional"
	}

	startTime := time.Now()

	// Build system prompt based on script type
	systemPrompt := g.buildScriptSystemPrompt(req)

	// Build user prompt
	userPrompt := g.buildScriptUserPrompt(req)

	// Generate the script
	genReq := &GenerationRequest{
		Provider:     g.config.DefaultProvider,
		Model:        g.config.DefaultModel,
		Prompt:       userPrompt,
		SystemPrompt: systemPrompt,
		Temperature:  0.7,
		MaxTokens:    req.TargetLength * 2, // Approximate tokens needed
		UseGPU:       req.UseGPU,
	}

	result, err := g.GenerateText(ctx, genReq)
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	// Parse sections (simple parsing by paragraphs)
	sections := g.parseScriptSections(result.Text)

	wordCount := len(strings.Fields(result.Text))

	scriptResult := &ScriptResult{
		Script:    result.Text,
		WordCount: wordCount,
		Type:      req.Type,
		Sections:  sections,
		GPUUsed:   result.GPUUsed,
		Duration:  time.Since(startTime),
	}

	logger.Info("Script generation completed",
		zap.String("type", string(req.Type)),
		zap.String("topic", req.Topic),
		zap.Int("word_count", wordCount),
		zap.Bool("gpu_used", result.GPUUsed),
		zap.Duration("duration", scriptResult.Duration),
	)

	return scriptResult, nil
}

// buildScriptSystemPrompt creates the system prompt for script generation
func (g *Generator) buildScriptSystemPrompt(req *ScriptRequest) string {
	prompt := fmt.Sprintf(`You are an expert script writer for %s videos. 

Guidelines:
- Write engaging, professional content
- Target length: approximately %d words
- Language: %s
- Tone: %s
- Structure the script with clear sections`,
		req.Type,
		req.TargetLength,
		req.Language,
		req.Tone,
	)

	if len(req.Keywords) > 0 {
		prompt += fmt.Sprintf("\n- Include these keywords naturally: %s", strings.Join(req.Keywords, ", "))
	}

	prompt += "\n\nFormat your response with clear section breaks using ===SECTION: [section name]=== markers."

	return prompt
}

// buildScriptUserPrompt creates the user prompt for script generation
func (g *Generator) buildScriptUserPrompt(req *ScriptRequest) string {
	prompt := fmt.Sprintf("Create a %s script about: %s\n\n", req.Type, req.Topic)

	if len(req.Structure) > 0 {
		prompt += fmt.Sprintf("Please structure it with these sections: %s\n\n", strings.Join(req.Structure, ", "))
	}

	prompt += "Make it engaging and professional."

	return prompt
}

// parseScriptSections parses the generated script into sections
func (g *Generator) parseScriptSections(script string) []ScriptSection {
	var sections []ScriptSection

	// Split by section markers
	parts := strings.Split(script, "===SECTION:")
	if len(parts) == 1 {
		// No section markers found, treat entire text as one section
		return []ScriptSection{
			{Name: "Main", Content: script},
		}
	}

	for _, part := range parts[1:] { // Skip first empty part
		lines := strings.SplitN(part, "\n", 2)
		if len(lines) < 2 {
			continue
		}

		sectionName := strings.TrimSpace(strings.Split(lines[0], "===")[0])
		content := strings.TrimSpace(lines[1])

		sections = append(sections, ScriptSection{
			Name:    sectionName,
			Content: content,
		})
	}

	if len(sections) == 0 {
		sections = append(sections, ScriptSection{
			Name:    "Main",
			Content: script,
		})
	}

	return sections
}
