package jobs

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func newManifestPlanner(kind, processor string, manifest job.CanonicalManifest) PreparationPlanner {
	return ManifestPreparationPlanner{Kind: kind, ProcessorVersion: processor, Manifest: manifest}
}

func NewLLMPreparationPlanner() PreparationPlanner {
	return newManifestPlanner("llm.generate", "llm-v1", job.LLMManifest("", "", "", "", "", "", 0, 0, 0, ""))
}
func NewResearchPreparationPlanner() PreparationPlanner {
	return newManifestPlanner("research.resolve", "research-v1", job.ResearchManifest("", nil, "", "", "", "research-v1", "", "", "", ""))
}
func NewTTSPreparationPlanner() PreparationPlanner {
	return newManifestPlanner("tts.synthesize", "tts-v1", job.TTSManifest("", "", "", "", "", "", 0, 0, 0, 0, "", "", ""))
}
func NewTranslationPreparationPlanner() PreparationPlanner {
	return newManifestPlanner("translation.generate", "translation-v1", job.TranslationManifest("", "", "", "", "", "", "", ""))
}

// Ensure the operators remain usable as regular PreparationPlanner instances.
var _ PreparationPlanner = ManifestPreparationPlanner{}
