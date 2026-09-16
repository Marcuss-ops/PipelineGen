// Package scriptgeneration — vidrush_semantic_ports.go: the port and pure
// value contracts of the Fase 1-5 semantic cutover (SceneIR → VisualNER →
// MediaSampler → Local Stock → MediaCert).
//
// Only interfaces, function types and the VisualEntity wire mirror live here;
// the implementations are in vidrush_semantic_chain.go and the MetalCert
// barrier in vidrush_mediacert_barrier.go. The Rust crates (rust/visualner,
// rust/mediasampler) are reached through these interfaces so production can
// swap in the stdio-JSON FFI adapter without the coordinator knowing about
// process spawning.
//
// Extracted 2026-09-12 from vidrush_semantic_chain.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VisualEntity is the source-grounded entity produced by the VisualNER
// Rust crate. It mirrors rust/visualner::VisualEntity so the FFI adapter
// can decode the crate's JSON output without translation.
type VisualEntity struct {
	Text     string               `json:"text"`
	Type     scriptpkg.EntityType `json:"type"`
	Score    float32              `json:"score"`
	Start    int                  `json:"start"`
	End      int                  `json:"end"`
	Evidence string               `json:"evidence,omitempty"`
}

// VisualNERPort extracts source-grounded visual entities from a scene's
// source text. The deterministic Rust crate (rust/visualner) is the
// production implementation; the rule it enforces is NO EVIDENCE → NO ENTITY.
type VisualNERPort interface {
	Extract(ctx context.Context, sourceText string, entityCount int) ([]VisualEntity, error)
}

// ImportantPhraseExtractor is the NLP semantic phrase surface. It is kept
// separate from VisualNER: the latter owns named entities, while this port
// asks the language model to identify meaningful source-grounded fragments.
type ImportantPhraseExtractor interface {
	ExtractImportantPhrases(ctx context.Context, sourceText string, limit int, language, model string) ([]string, error)
}

// BatchImportantPhraseExtractor is the optional batched variant of
// ImportantPhraseExtractor, satisfied by the language-model adapter.
//
// The per-scene shape costs one model call per (scene, language): a 10-scene
// three-language run paid 30 calls for hints ("key_statement" annotations and
// Artlist phrases) that never gate the run. The batched shape collapses that to
// one call per chunk of scenes per language, which is the same structured NLP
// response the single-scene path already asks for — it is a request-batching
// change, not a semantic one.
//
// Contract: the returned slice has exactly len(sourceTexts) entries, aligned by
// input position; a segment with no extractable phrase yields an empty (nil is
// accepted) entry. Callers MUST fall back to ExtractImportantPhrases when this
// interface is not implemented, and MUST treat an error as "use the per-scene
// path", never as "no phrases".
type BatchImportantPhraseExtractor interface {
	ImportantPhraseExtractor
	ExtractImportantPhrasesBatch(ctx context.Context, sourceTexts []string, limit int, language, model string) ([][]string, error)
}

// SceneNLPExtraction is the language-local NLP surface returned by the model.
// Every value is a candidate copied from that scene's translated text; the
// runner still applies its own source-span grounding before creating an
// annotation.
type SceneNLPExtraction struct {
	ImportantPhrases []string
	ImportantWords   []string
	SpecialNames     []string
	Entities         []VisualEntity
}

// SceneNLPExtractor exposes the complete structured extraction for one scene.
// It is optional so older phrase-only adapters retain their existing contract.
type SceneNLPExtractor interface {
	ExtractSceneNLP(ctx context.Context, sourceText string, limit int, language, model string) (SceneNLPExtraction, error)
}

// BatchSceneNLPExtractor batches the full structured output by language while
// retaining positional scene alignment.
type BatchSceneNLPExtractor interface {
	SceneNLPExtractor
	ExtractSceneNLPBatch(ctx context.Context, sourceTexts []string, limit int, language, model string) ([]SceneNLPExtraction, error)
}

// LocalStockResolverPort is the LOCAL FIRST PROVIDER SECOND resolver. The
// stockintelligence.Service is the production implementation; it consults the
// local Qdrant search + SQLite hydrate first and falls back to the provider
// only when local_candidates < threshold or best_score < minimum_quality.
type LocalStockResolverPort interface {
	Resolve(ctx context.Context, req stockintelligence.ResolveRequest) (stockintelligence.ResolveResult, error)
}

// MediaCertifierPort certifies a completed VidRush run against a spec. The
// mediacert.Certify function is the production implementation. A
// CERTIFIED=false report must fail the job even when JobStatus=SUCCEEDED.
type MediaCertifierPort interface {
	Certify(ctx context.Context, spec mediacert.Spec, result mediacert.MediaResult) (mediacert.Report, error)
}

// MediaCertifierFunc adapts the canonical mediacert.Certify function to the
// pipeline boundary. It deliberately contains no certification rules.
type MediaCertifierFunc func(context.Context, mediacert.Spec, mediacert.MediaResult) (mediacert.Report, error)

func (f MediaCertifierFunc) Certify(ctx context.Context, spec mediacert.Spec, result mediacert.MediaResult) (mediacert.Report, error) {
	return f(ctx, spec, result)
}

// MediaCertSpecResolver creates the run-specific contract from the resolved
// plan instead of using a hard-coded fixture in production.
type MediaCertSpecResolver interface {
	ResolveMediaCertSpec(*scriptpkg.ResolvedGenerationPlan) mediacert.Spec
}

type MediaCertSpecResolverFunc func(*scriptpkg.ResolvedGenerationPlan) mediacert.Spec

func (f MediaCertSpecResolverFunc) ResolveMediaCertSpec(plan *scriptpkg.ResolvedGenerationPlan) mediacert.Spec {
	return f(plan)
}
