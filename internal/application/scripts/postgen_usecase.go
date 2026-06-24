// Package scripts — postgen_usecase.go is the post-generation phase
// for the unified clip-source script generation job (entities
// extraction + insights + multi-lingual video metadata).
//
// Wave 14 problem #4 fixup (June 2026): the previous commit landed
// 5 NEW use cases but left post-gen as a STUB closure in
// registry.go::wireScriptFlow — meaning when spec.ExtractEntities=true
// or spec.GenerateMetadata=true the entities block + metadata block
// in the response was silently empty. This use case restores the
// original ScriptFlowHandler.handlePostGeneration logic into a
// typed use case consumable by the existing *Pipeline via a 5-line
// closure adapter registered in wireScriptFlow.
//
// Why this lives in its own file (D1 in the fixup design):
// "extract by concept" — the post-gen phase is logically distinct
// from path dispatching (phases 1-3) and from pipeline post-process
// (scene + doc creation). Generating entities + insights + metadata
// belongs alongside the other bounded-context use cases
// (SectionRegenerator, GenerateBatchUseCase, CacheEvictionUseCase).
//
// Why the api/script helpers are function-typed fields (D3 in the
// fixup design): api/script (the package) already imports
// application/scripts, so the reverse import path (= application
// importing api/script) would create a cycle. Injecting the helpers
// as opaque function values lets the registry close the cycle by
// supplying `script.BuildMetadataLanguages` + `script.GenerateVideoMetadata`
// directly when it builds the use case — but postgen_usecase.go
// itself never imports api/script.
//
// Why the postGen closure in scripts.NewPipeline is preserved (D2
// in the fixup design): Pipeline.Run is tightly coupled in 8+ test
// locations and in the batch-persistence / doc-creation tests.
// Keeping the callback interface intact and routing it through a
// thin closure adapter (`postGenAdapter := func(ctx, spec, scr) {…,
// postGenUC.Run(...)...}`) minimises regression risk while
// definitively moving the BUSINESS LOGIC out of the registry.
//
// The use case owns:
//   - the parallel entities + insights phase (ExtractEntities flag)
//   - the parallel multi-language video metadata phase
//     (GenerateMetadata flag)
//   - nil-safe short-circuit when both flags are false OR the
//     script is empty (mirrors the original nil-pathResult early-exit)
//
// The use case does NOT own:
//   - the spec decode (caller payload decode responsibility)
//   - the path result / writeResult (caller orchestration
//     responsibility — only the script string crosses the boundary)
//   - HTTP transport shape (handler responsibility)
//   - the documents/scenes pipeline phases (they belong to the
//     Pipeline + DocumentsUseCase / SceneBuilderUseCase respectively)
package scripts

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// ── Result + function-port types ────────────────────────────────────────────

// PostGenResult encapsulates the outputs of the post-generation
// phase. Exactly one of the three fields may be empty depending
// on the spec flags (payload.ExtractEntities / payload.GenerateMetadata):
//
//	- EntitiesJSON  is non-empty iff payload.ExtractEntities=true
//	  AND the extractor returned a non-empty entity analysis.
//	- Insights      is non-zero iff ExtractEntities=true AND
//	  the insight builder ran (always populated when extractor
//	  returned even an empty analysis, mirroring the original
//	  ScriptInsightBuilder.Build contract — it never returns a
//	  zero-valued ScriptInsights).
//	- VideoMetadata is non-empty iff payload.GenerateMetadata=true
//	  AND the generator was wired.
type PostGenResult struct {
	EntitiesJSON  string
	Insights      ScriptInsights
	VideoMetadata []VideoMetadata
}

// LanguageBuilderFunc is the injection point for the helper that
// turns (baseLanguage + additionalLanguages) into the canonical
// ordered []string used by the metadata phase. In production the
// registry supplies `script.BuildMetadataLanguages` from
// api/script/helpers.go; tests can supply a fixed []string-list.
type LanguageBuilderFunc func(baseLanguage string, additionalLanguages []string) []string

// MetadataGenFunc is the injection point for the helper that
// produces the multi-language VideoMetadata slice. In production
// the registry supplies `script.GenerateVideoMetadata` from
// api/script/helpers.go; tests can supply a deterministic fake.
//
// The function signature mirrors the api/script helper exactly:
//   (ctx, generator, title, languages, model) → []VideoMetadata
// so the registry wires it via a direct function value
// (`script.GenerateVideoMetadata`) with no additional adapter.
type MetadataGenFunc func(ctx context.Context, generator *ollama.Generator, title string, languages []string, model string) []VideoMetadata

// InsightBuilder is the narrow port the use case consumes to convert
// entity-analysis JSON into the rich ScriptInsights struct. The
// production implementation is *ScriptInsightBuilder declared in
// insight_builder.go (same package) which satisfies this interface
// via its Build method (structural typing). Tests can supply a
// fake implementation to exercise the post-gen flow end-to-end
// without instantiating images.Service / realtime.Service / etc.
type InsightBuilder interface {
	Build(ctx context.Context, title, script, entitiesJSON string) ScriptInsights
}

// ── Use case ────────────────────────────────────────────────────────────────

// PostGenUseCase orchestrates the post-generation extraction +
// translation phases. It is ctor-injected into the registry which
// builds the parameterised closure for scripts.NewPipeline.
//
// All ctor-injected deps are nil-safe so the use case can be unit
// tested without the full production fleet (tests pass nil generator +
// nil extractor + nil insightBuilder + nil languageBuilder + nil
// metadataGen and observe the matching degenerate behaviour).
type PostGenUseCase struct {
	extractor       EntityScriptExtractor
	insightBuilder  InsightBuilder
	generator       *ollama.Generator
	metadataModel   string
	languageBuilder LanguageBuilderFunc
	metadataGen     MetadataGenFunc
	log             *zap.Logger
}

// NewPostGenUseCase constructs the use case. All deps are nil-safe:
//
//   - extractor nil       → entities extraction skipped; EntitiesJSON empty.
//   - insightBuilder nil  → insights building skipped; Insights zero-valued.
//   - generator nil       → video-metadata phase skipped silently.
//   - languageBuilder nil → never called (because metadataGen nil
//     short-circuits the metadata phase unconditionally).
//   - metadataGen nil     → video metadata phase skipped silently.
//
// Production always supplies all six (the registry wires them from
// canonical sources — see wireScriptFlow in app/registry.go).
func NewPostGenUseCase(
	extractor EntityScriptExtractor,
	insightBuilder InsightBuilder,
	generator *ollama.Generator,
	metadataModel string,
	languageBuilder LanguageBuilderFunc,
	metadataGen MetadataGenFunc,
	log *zap.Logger,
) *PostGenUseCase {
	return &PostGenUseCase{
		extractor:       extractor,
		insightBuilder:  insightBuilder,
		generator:       generator,
		metadataModel:   metadataModel,
		languageBuilder: languageBuilder,
		metadataGen:     metadataGen,
		log:             log,
	}
}

// Run executes the parallel extraction + translation phases.
// Returns empty PostGenResult (no error) when any of:
//
//   - the use case was not constructed (nil receiver — early-exit)
//   - the script is empty (mirrors the original nil-pathResult early-exit)
//   - payload is nil OR neither flag (ExtractEntities nor
//     GenerateMetadata) is true
//
// Otherwise both phases run concurrently via concurrent.WithContext
// — first-error-wins + panic recovery are visible to the operator via
// the structured logger the use case was built with. Phase-level
// failures are logged WARN and DO NOT propagate (preserves the
// original handler's "best-effort, continue on failure" semantics).
//
// Errors from the extractor are NOT returned; they are logged + the
// entities block is left empty. Errors from the metadata generator
// are similarly handled inside GenerateVideoMetadata (a nil generator
// is gracefully tolerated — translations return empty strings, the
// function returns nil). Matches the original handlePostGeneration
// call site which had zero nil-checks around the generator pointer.
//
// The only error this function returns is the typed
// concurrent.WithContext panic-recovery error if a phase panicked;
// it is logged + ignored upstream by the original handler's caller
// pattern. For correctness, Run returns nil error unless a panic
// occurred, mirroring the original behaviour.
func (u *PostGenUseCase) Run(ctx context.Context, payload *scriptpkg.GenerationSpec, script string) (PostGenResult, error) {
	var res PostGenResult
	if u == nil || script == "" {
		return res, nil
	}
	if payload != nil && !payload.ExtractEntities && !payload.GenerateMetadata {
		return res, nil
	}

	group, groupCtx := concurrent.WithContext(ctx)

	if payload != nil && payload.ExtractEntities {
		group.Go("entities-and-insights", func() error {
			ents, err := ExtractScriptEntities(groupCtx, u.extractor, script, u.metadataModel)
			if err != nil {
				if u.log != nil {
					u.log.Warn("failed to extract entities", zap.Error(err))
				}
			}
			res.EntitiesJSON = ents
			if u.insightBuilder != nil {
				res.Insights = u.insightBuilder.Build(groupCtx, payload.Title, script, ents)
			}
			return nil
		})
	}

	if u.metadataGen != nil && u.languageBuilder != nil && payload != nil && payload.GenerateMetadata {
		group.Go("video-metadata", func() error {
			languages := u.languageBuilder(payload.Language, payload.Languages)
			res.VideoMetadata = u.metadataGen(groupCtx, u.generator, payload.Title, languages, u.metadataModel)
			return nil
		})
	}

	if waitErr := group.Wait(); waitErr != nil {
		if u.log != nil {
			u.log.Warn("post-generation phase returned an error (continuing)", zap.Error(waitErr))
		}
	}

	return res, nil
}
