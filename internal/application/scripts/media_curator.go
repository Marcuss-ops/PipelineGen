// Package scripts — media_curator.go replaces the MediaCurator stub
// with a real implementation that searches clips semantically via
// Qdrant vector store and generates scripts via the Engine.
//
// AGENT-3 (June 2026): the previous stub returned the query string as
// the script text. The real implementation:
//   1. Searches for clips semantically via the vector store
//   2. Converts results to clip IDs
//   3. Builds clip context via ClipSourceBuilder
//   4. Generates the script via Engine.WriteScript
package scripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	"go.uber.org/zap"
)

// NewMediaCurator constructs a real MediaCurator backed by the
// clips repository, ClipSourceBuilder, and Engine. All concrete typed args.
//
// vectorStore parameter was removed from this constructor and the
// capability was deleted. Semantic clip discovery now flows solely
// through the clips repository + ClipSourceBuilder; the engine leg
// is unchanged.
//   - clipsRepo may be nil (Curate returns an error when missing).
//   - engine is required for the script generation leg.
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
	engine *Engine,
	log *zap.Logger,
) *MediaCurator {
	return &MediaCurator{
		serverURL:   serverURL,
		clipsRepo:   clipsRepo,
		clipBuilder: clipBuilder,
		engine:      engine,
		log:         log,
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
//  3. Generate script via Engine.WriteScript
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
			Category:  "", // CurateRequest does not expose Category; operators
			// raise it via JobPayloadCurate once the typed-payload wiring
			// lands (PJ-CURATE-2 follow-up). For now the search leg carries
			// Source + MediaType filters only. The previous code passed
			// req.Tone as Category which was a silent cross-field leak.
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
					ClipID:    h.AssetID,
					Name:      h.Name,
					Score:     h.Score,
					Source:    h.Source,
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

	// Phase 3: generate script.
	writeStart := time.Now()
	if m.engine == nil {
		return nil, fmt.Errorf("media curator: engine not configured")
	}

	writeReq := WriteScriptRequest{
		Topic:      query,
		Title:      title,
		Language:   req.Language,
		Tone:       req.Tone,
		Model:      req.Model,
		Mode:       "curate",
		SourceText: sourceText,
		MinWords:   req.TargetWords,
		Prompt:     sourceFingerprint,
		UseMemory:  !req.ForceRefresh,
		SaveToDB:   true,
	}
	writeResult, err := m.engine.WriteScript(ctx, writeReq)
	if err != nil {
		return nil, fmt.Errorf("media curator: script generation failed: %w", err)
	}
	writeScriptMs := time.Since(writeStart).Milliseconds()

	totalMs := time.Since(startAll).Milliseconds()

	if m.log != nil {
		m.log.Info("media curator: curation completed",
			zap.String("title", title),
			zap.Int("word_count", writeResult.WordCount),
			zap.Int("clips_found", len(clipIDs)),
			zap.Int64("search_ms", searchMs),
			zap.Int64("build_ctx_ms", buildCtxMs),
			zap.Int64("write_ms", writeScriptMs),
			zap.Int64("total_ms", totalMs))
	}

	return &CurateResult{
		Title:             title,
		ClipScenes:        clipScenes,
		Script:            writeResult.Script,
		WordCount:         writeResult.WordCount,
		CacheStatus:       writeResult.CacheStatus,
		AcceptedClipIDs:   clipIDs,
		NarrativePlan:     narrativePlan,
		SourceText:        sourceText,
		SourceFingerprint: sourceFingerprint,
		SearchResults:     searchResults,
		Timings: CurateTimings{
			SearchMs:      searchMs,
			BuildCtxMs:    buildCtxMs,
			WriteScriptMs: writeScriptMs,
			TotalMs:       totalMs,
		},
	}, nil
}
