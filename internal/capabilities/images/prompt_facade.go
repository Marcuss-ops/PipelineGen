package images

import imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"

type ResolvedGenerationRequest = imggeneration.ResolvedGenerationRequest
type GenerateCommand = imggeneration.GenerateCommand
type PromptComposer = imggeneration.PromptComposer
type ComposeResult = imggeneration.ComposeResult
type Section = imggeneration.Section

const (
	SectionImageWidth = imggeneration.SectionImageWidth
	SectionImageHeight = imggeneration.SectionImageHeight
)

func NewPromptComposer() PromptComposer { return imggeneration.NewPromptComposer() }
func ComposePrompt(prompt, style, negativePrompt string) ComposeResult { return imggeneration.ComposePrompt(prompt, style, negativePrompt) }
func BuildPrimaryPrompt(sec Section, topic string) string { return imggeneration.BuildPrimaryPrompt(sec, topic) }
func buildSectionPrompts(sec Section, topic string) []string { return imggeneration.BuildSectionPrompts(sec, topic) }
