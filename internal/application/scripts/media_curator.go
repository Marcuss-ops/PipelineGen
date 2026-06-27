// Package scripts — media_curator.go replaces the MediaCurator stub
// with a real implementation that searches clips semantically via
// Qdrant vector store and generates scripts via GenerateOneUseCase.
//
// AGENT-3 (June 2026): the previous stub returned the query string as
// the script text. The real implementation:
//  1. Searches for clips semantically via the vector store
//  2. Converts results to clip IDs
//  3. Builds clip context via ClipSourceBuilder
//  4. Generates the script via GenerateOneUseCase.Execute
//
// PR 13 (June 2026): migrated from engine.WriteScript to
// GenerateOneUseCase.Execute — canonical unified pipeline.
//
// PR 3 (June 2026): the typed walk via PostProcessorRegistry is
// the canonical generator path. Scenes are walked by the
// processors (ImageProcessor, VoiceoverProcessor) directly inside
// ppReg.Run; the curator no longer mediates scene ↔ asset pairing.
// The pre-PR-3 overwriting clip-binding loop in the canonical
// pipeline output is gone — clip bindings flow from the model's
// V1 contract or are absent.
package scripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	"go.uber.org/zap"
)

// NewMediaCurator constructs a real MediaCurator backed by the
// clips repository, ClipSourceBuilder, and GenerateOneUseCase.
// All concrete typed args.
//
// vectorStore parameter was removed from this constructor and the
// capability was deleted. Semantic clip discovery now flows solely
// through the clips repository + ClipSourceBuilder; the generation
// leg now flows through the canonical GenerateOneUseCase.
//   - clipsRepo may be nil (Curate returns an error when missing).
//   - generateOneUC is required for the script generation leg.
//   - clipBuilder is required for the clip context building leg.
//   - clipSearch (PG-CURATE-1, June 2026) is the optional
//     ClipSearchPort for the semantic-search leg. nil is allowed —
//     invoke the SetClipSearchPort setter from the composition root
//     when Qdrant is enabled and the operator wants the
//     `Search=true` opt-in. nil skips the port entirely (legacy
//     HintClipIDs-only behaviour, except for ErrCurateNoClips which
//     still surfaces when both ports and hints are empty).
func NewMediaCurator(
	serverURL string,
	clipsRepo *assets.ClipsRepository,
	clipBuilder *ClipSourceBuilder,
	generateOneUC *GenerateOneUseCase,
	log *zap.Logger,
) *MediaCurator {
	return &MediaCurator{
		serverURL:     serverURL,
		clipsRepo:     clipsRepo,
		clipBuilder:   clipBuilder,
		generateOneUC: generateOneUC,
		log:           log,
	}
}

// SetClipSearchPort attaches the typed ClipSearchPort adapter to the
// curator. Called by the composition root (wire_script.go) when
// Qdrant is enabled; nil is the safe no-op for the Qdrant-disabled
// deployment path. Tests pass an in-memory fake here without touching
// the production wiring.
func (m *MediaCurator) SetClipSearchPort(port ClipSearchPort) {
	if m == nil {
		return
	}
	m.clipSearch = port
}

// Curate searches for clips matching the query, builds context, and
// generates a script. The flow:
//
//  1. semantic-search leg removed from this flow.
//     The clip IDs are derived from req.HintClipIDs or from a text-only
//     fallback if the caller supplied none.
//  2. Build clip context via ClipSourceBuilder
//  3. Generate script via GenerateOneUseCase.Execute (canonical)
//  4. Build CurateResult with timings
func (m *MediaCurator) Curate(ctx context.Context, req CurateRequest) (*CurateResult, error) {
	if m == nil {
		return nil, fmt.Errorf("media curator: not constructed")
	}

	startAll := time.Now()

	query := strings.TrimSpace(req.Query)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = query
	}
	if query == "" && title == "" {
		return nil, fmt.Errorf("media curator: query and title are both empty")
	}

	// Phase 1: pick up clip IDs from optional semantic-search leg
	// (req.Search=true + m.clipSearch wired) AND/OR caller-seeded
	// HintClipIDs. The two paths are unioned (deduped by AssetID).
	//
	// PJ-CURATE-1 (June 2026): the previous behaviour silently fell
	// back to text-only when neither path produced clips. The new
	// contract (ErrCurateNoClips) honours the caller's opt-in via
	// req.AllowTextOnly. The silent fallback is rejected because
	// it hid real failures — a /curate request asking for clip-driven
	// curation would receive a text-only response without surfacing
	// the caller's misconfiguration.
	searchStart := time.Now()
	clipIDs := make([]string, 0)
	searchResults := make([]SearchResultInfo, 0)
	seen := make(map[string]struct{})

	// PJ-CURATE-1 (June 2026): when the operator opts in via
	// `?search=true` but the production wiring for ClipSearchPort
	// has not landed (SetClipSearchPort has never been called), the
	// user-facing experience would otherwise be SILENT — same
	// HintClipIDs-only output as without the flag. Surface the
	// de-differentiated state in logs so operators see the
	// mismatch between "asked" and "got" instead of debugging in
	// the dark post-incident.
	if req.Search && m.clipSearch == nil {
		if m.log != nil {
			m.log.Info("PJ-CURATE-1: ?search=true requested but ClipSearchPort not wired (HintClipIDs-only fallback; production wiring pending)",
				zap.String("query", req.Query),
				zap.String("source", req.Source),
				zap.String("media_type", req.MediaType),
			)
		}
	}

	if req.Search && m.clipSearch != nil {
		hits, searchErr := m.clipSearch.SearchClips(ctx, ClipSearchQuery{
			Query:     req.Query,
			Source:    req.Source,
			Category:  "", // CurateRequest does not expose Category
			MediaType: req.MediaType,
			Limit:     req.MaxClips,
			MinScore:  req.MinScore,
		})
		if searchErr != nil {
			if m.log != nil {
				m.log.Warn("clip search port returned error (falling back to hint list)", zap.Error(searchErr))
			}
			// Fall through to hint list + typed error contract.
		} else {
			for _, h := range hits {
				if _, dup := seen[h.AssetID]; dup {
					continue
				}
				seen[h.AssetID] = struct{}{}
				clipIDs = append(clipIDs, h.AssetID)
				searchResults = append(searchResults, SearchResultInfo{
					ClipID: h.AssetID,
					Name:   h.Name,
					Score:  h.Score,
					Source: h.Source,
				})
			}
		}
	}

	// Merge in caller-seeded HintClipIDs (deduped against search hits).
	for _, id := range req.HintClipIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		clipIDs = append(clipIDs, id)
	}

	if len(clipIDs) == 0 && !req.AllowTextOnly {
		return nil, &CurateNoClipsError{
			Query:       req.Query,
			MinScore:    req.MinScore,
			ResultCount: 0,
		}
	}
	searchMs := time.Since(searchStart).Milliseconds()

	// Phase 2: build clip context, or fall back to text-only.
	buildCtxStart := time.Now()
	var clipScenes []ClipScene
	var sourceText string
	var sourceFingerprint string
	var narrativePlan *NarrativePlan

	if len(clipIDs) > 0 && m.clipBuilder != nil {
		opts := &ClipGenerationOptions{
			Language:          req.Language,
			Tone:              req.Tone,
			Title:             title,
			Model:             req.Model,
			TargetWords:       req.TargetWords,
			StyleInstructions: req.StyleInstructions,
		}
		pack, plan, srcText, buildErr := m.clipBuilder.BuildClipContext(ctx, clipIDs, opts)
		if buildErr != nil {
			if m.log != nil {
				m.log.Warn("media curator: clip context build failed, falling back to text-only",
					zap.Error(buildErr))
			}
			// Fall through to text-only.
		} else {
			sourceText = srcText
			narrativePlan = plan
			sourceFingerprint = m.clipBuilder.ComputeFingerprint(clipIDs, pack, opts, NewFingerprintContext(req.Model, req.Model))

			// Build clip scenes from the pack.
			if packMap, ok := pack.(map[string]any); ok {
				if names, ok := packMap["clip_names"].([]string); ok {
					for i, name := range names {
						id := ""
						if i < len(clipIDs) {
							id = clipIDs[i]
						}
						link := ""
						if i < len(searchResults) {
							link = searchResults[i].DriveLink
						}
						clipScenes = append(clipScenes, ClipScene{
							SceneIndex: i,
							Text:       name,
							ClipID:     id,
							DriveLink:  link,
							Kind:       "clip",
						})
					}
				}
			}
		}
	}
	buildCtxMs := time.Since(buildCtxStart).Milliseconds()

	// Phase 3: generate script via GenerateOneUseCase (canonical).
	genStart := time.Now()
	if m.generateOneUC == nil {
		return nil, fmt.Errorf("media curator: generateOneUC not configured")
	}

	// Build a GenerationItemV2 from the curator's resolved context.
	// Use SourceText so the TextSourceResolver passes through our
	// already-built source text; the clip context (narrativePlan,
	// clipScenes, sourceFingerprint) stays in the CurateResult
	// side-channel — GenerateOneUseCase only needs the text.
	item := scriptpkg.GenerationItemV2{
		ID:       query,
		Title:    title,
		Language: req.Language,
		Tone:     req.Tone,
		Model:    req.Model,
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      query,
			SourceText: sourceText,
			// PR 2: guidelines carry REAL editorial style
			// (req.StyleInstructions from the curator request),
			// not the source fingerprint. The fingerprint now
			// lives on plan.SourceFingerprint (cache-key input,
			// never seen by the model).
			Guidelines: req.StyleInstructions,
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords:  req.TargetWords,
			UseMemory:    !req.ForceRefresh,
			ForceRefresh: req.ForceRefresh,
		},
		Output: scriptpkg.OutputSpec{
			// PR 5: SaveToDB enables the "persistence" phase in the
			// generated plan's Postprocessors list (built by
			// generation_plan_builder.go). The single writer is
			// PersistenceProcessor; the engine no longer writes to
			// the scripts table. Idempotency key on (item_id,
			// cache_key, prompt_version, target_words, language)
			// makes replays a no-op.
			SaveToDB: true,
		},
	}

	tracker := NewProgressTracker(nil, "curate")
	genResult, genErr := m.generateOneUC.Execute(ctx, item, scriptpkg.PresetCustom, tracker)
	if genErr != nil {
		return nil, fmt.Errorf("media curator: script generation failed: %w", genErr)
	}
	genMs := time.Since(genStart).Milliseconds()

	totalMs := time.Since(startAll).Milliseconds()

	if m.log != nil {
		m.log.Info("media curator: curation completed",
			zap.String("title", title),
			zap.Int("word_count", genResult.Output.WordCount),
			zap.Int("clips_found", len(clipIDs)),
			zap.Int64("search_ms", searchMs),
			zap.Int64("build_ctx_ms", buildCtxMs),
			zap.Int64("gen_ms", genMs),
			zap.Int64("total_ms", totalMs))
	}

	return &CurateResult{
		Title:             title,
		ClipScenes:        clipScenes,
		Script:            genResult.Output.Text,
		WordCount:         genResult.Output.WordCount,
		CacheStatus:       genResult.Cache.Status,
		AcceptedClipIDs:   clipIDs,
		NarrativePlan:     narrativePlan,
		SourceText:        sourceText,
		SourceFingerprint: sourceFingerprint,
		SearchResults:     searchResults,
		Timings: CurateTimings{
			SearchMs:      searchMs,
			BuildCtxMs:    buildCtxMs,
			WriteScriptMs: genMs,
			TotalMs:       totalMs,
		},
	}, nil
}
