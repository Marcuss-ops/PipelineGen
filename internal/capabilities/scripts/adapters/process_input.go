package adapters

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

// ProcessInput is the typed envelope passed to every postprocessor.
type ProcessInput struct {
	Text              string
	WordCount         int
	SpecScene         scriptpkg.SpecSceneOutput
	OriginalText      string
	OriginalSpecScene scriptpkg.SpecSceneOutput
	// TranslatedText and TranslatedSpecScene are the canonical translated
	// surfaces retained across postprocessor write-back. Keeping them
	// alongside the working envelope prevents a later binding processor from
	// accidentally reintroducing the source-language scene text before TTS.
	TranslatedText      string
	TranslatedSpecScene scriptpkg.SpecSceneOutput
	ModelUsed           string
	CacheStatus         string
	SourceTrace         *scriptpkg.ClipEvidence
	PriorArtifacts      map[string]PostProcessResult
	EffectiveLanguage   string
	StockEnabled        scriptpkg.Toggle
	StockBindings       []scriptpkg.StockBindingInput
	ResearchSources     []scriptpkg.SourceReference

	// Entities carries the entity-extraction result, populated by
	// MergePostProcessResult when the entities processor produces
	// output. It is the canonical typed entity surface consumed by the
	// response and persistence writers. Nil until the entities
	// processor runs.
	// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026).
	Entities *scriptpkg.EntityResult

	// VidRushSegments carries the per-segment VidRush output that
	// downstream processors reuse for query fan-out and binding.
	VidRushSegments []scriptpkg.VidRushSegmentResult

	// Metadata carries the video-metadata result, populated by
	// MergePostProcessResult when the metadata processor produces
	// output. It is the canonical typed metadata surface consumed by
	// the response and persistence writers. Nil until the metadata
	// processor runs.
	// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026).
	Metadata []scriptpkg.VideoMetadata

	// Provenance carries the generation provenance block. Populated by
	// GenerateOneUseCase before Run.
	// PR-PROVENANCE (July 2026).
	Provenance *scriptpkg.GenerationProvenance

	// SpecSceneChanged is internal pipeline state used to force a document
	// refresh after reconciliation changed the published scene surface.
	SpecSceneChanged bool `json:"-"`
}
