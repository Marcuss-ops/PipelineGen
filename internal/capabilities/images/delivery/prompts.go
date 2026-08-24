// Package fullimages owns the section→image-prompt composition consumed by
// the /api/images/batch-generate mode=sections flow.
//
// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10) + IMAGES-LEGACY-CLEANUP:
// the former synchronous FullImages service (one image per section via
// GenerateSmartImage, Ken Burns pipeline, Drive publish) was retired when
// POST /api/fullimages/image/generate was merged into the async
// /api/images/batch-generate surface (mode=sections). This package now
// holds ONLY the deterministic section→prompt composition: Section,
// BuildPrimaryPrompt, buildSectionPrompts, and the canonical section-image
// output dimensions. Zero publishing, zero orchestration.
package delivery

import (
	"fmt"
	"strings"
)

// Section describes a single text part for which an image should be generated.
type Section struct {
	Title string `json:"title" binding:"required" example:"Castello Medievale"`
	Text  string `json:"text"  example:"Descrizione della scena..."`
	Style string `json:"style" example:"medievale"`
}

// SectionImageWidth / SectionImageHeight are the canonical output
// dimensions for section-mode batch generation. They preserve the
// retired fullimages sync pipeline's imageGenWidth / imageGenHeight
// contract (1344×768) so mode=sections produces the same aspect ratio.
const (
	SectionImageWidth  = 1344
	SectionImageHeight = 768
)

// BuildPrimaryPrompt returns the primary candidate prompt for a section:
// the first non-empty candidate produced by buildSectionPrompts. It is the
// single prompt the async image.generate.google job consumes in the
// /api/images/batch-generate mode=sections flow (the async job takes one
// prompt, so the sync fallback-prompt list collapses to its primary).
//
// It returns "" only when the section has no title, no text, and the
// topic is empty — callers treat that as a validation error (godlike/07
// fail-closed, never a silent empty prompt).
func BuildPrimaryPrompt(sec Section, topic string) string {
	for _, p := range buildSectionPrompts(sec, topic) {
		if strings.TrimSpace(p) != "" {
			return p
		}
	}
	return ""
}

// buildSectionPrompts creates candidate image prompts from section content.
// The first non-empty prompt is the primary; remaining are fallbacks.
func buildSectionPrompts(sec Section, topic string) []string {
	var prompts []string

	// 1. Use section title as the primary prompt subject
	if sec.Title != "" {
		prompts = append(prompts,
			fmt.Sprintf("cinematic documentary image of %s", sec.Title),
			fmt.Sprintf("professional stock photo of %s", sec.Title),
		)
	}

	// 2. Add topic context if present and different from title
	if topic != "" && !strings.EqualFold(topic, sec.Title) {
		prompts = append(prompts,
			fmt.Sprintf("cinematic documentary image of %s, %s theme", sec.Title, topic),
			fmt.Sprintf("high quality photography of %s related to %s", sec.Title, topic),
		)
	}

	// 3. Use the first ~100 chars of text as a contextual prompt
	if text := strings.TrimSpace(sec.Text); text != "" {
		if len(text) > 100 {
			text = text[:100]
		}
		prompts = append(prompts, text)
	}

	// 4. Pure topic as last resort
	if topic != "" {
		prompts = append(prompts, fmt.Sprintf("documentary image about %s", topic))
	}

	return prompts
}
