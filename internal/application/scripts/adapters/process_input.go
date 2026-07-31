package adapters

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

// ProcessInput is the typed envelope passed to every postprocessor.
type ProcessInput struct {
	Text              string
	WordCount         int
	SpecScene         scriptpkg.SpecSceneOutput
	OriginalText      string
	OriginalSpecScene scriptpkg.SpecSceneOutput
	ModelUsed         string
	CacheStatus       string
	SourceTrace       *scriptpkg.ClipEvidence
	PriorArtifacts    map[string]PostProcessResult
	EffectiveLanguage string
	StockEnabled      scriptpkg.Toggle
	StockBindings     []scriptpkg.StockBindingInput

	// Entities carries the entity-extraction result, populated by
	// mergePostProcessResult when the entities processor produces
	// output. Threaded through to the canonical document renderer so
	// the Google Doc renders the <h2>Entities</h2> section when
	// non-empty. Nil until the entities processor runs.
	// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026).
	Entities *scriptpkg.EntityResult

	// VidRushSegments carries the per-segment VidRush output that
	// downstream processors reuse for query fan-out and binding.
	VidRushSegments []scriptpkg.VidRushSegmentResult

	// Metadata carries the video-metadata result, populated by
	// mergePostProcessResult when the metadata processor produces
	// output. Threaded through to the canonical document renderer so
	// the Google Doc renders the <h2>Video Metadata</h2> section
	// when non-empty. Nil until the metadata processor runs.
	// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026).
	Metadata []scriptpkg.VideoMetadata

	// Provenance carries the provisional generation provenance block.
	// The document processor fills DocID/DocLink after creating or
	// updating the Google Doc and embeds the complete block into the
	// document HTML. Populated by GenerateOneUseCase before Run.
	// PR-PROVENANCE (July 2026).
	Provenance *scriptpkg.GenerationProvenance
}
