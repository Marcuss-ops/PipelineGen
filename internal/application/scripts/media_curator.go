// Package scripts — media_curator.go is the canonical clip-curation
// service. PG-033 (June 2026): clipsRepo is now concrete
// *assets.ClipsRepository (no runtime `ok := ... (*assets.ClipsRepository)`
// check). vectorStore remains `interface{}` because it accepts
// multiple backend adapters (qdrant.NewSearchAdapter, etc.) and is
// matched against the narrow `clipSearcher` port at use site.
package scripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	"go.uber.org/zap"
)

// NewMediaCurator constructs a real MediaCurator. All concrete typed args.
//
// vectorStore and clipsRepo may be nil (Curate returns an error when
// they're needed and missing). engine is required for the script
// generation leg; clipBuilder is required for the clip context
// building leg.
func NewMediaCurator(
	vectorStore interface{},
	serverURL string,
	clipsRepo *assets.ClipsRepository,
	clipBuilder *ClipSourceBuilder,
	engine *Engine,
	log *zap.Logger,
) *MediaCurator {
	return &MediaCurator{
		vectorStore: vectorStore,
		serverURL:   serverURL,
		clipsRepo:   clipsRepo,
		clipBuilder: clipBuilder,
		engine:      engine,
		log:         log,
	}
}

// Curate searches for clips matching the query, builds context, and
// generates a script. The flow:
//
//  1. Search for clips via the vector store (Qdrant)
//  2. If the vector store is unavailable, fall back to text-only generation
//  3. Build clip context via ClipSourceBuilder
//  4. Generate script via Engine.WriteScript
//  5. Build CurateResult with timings
//
// PG-033: the previous `clipsRepo.(*assets.ClipsRepository)` runtime
// assertion is gone (field is already concrete). clipsRepo is read
// for the nil-gate only (the clips come from the vector store's
// RealtimeMatchAsset list, not directly from the repo at this layer).
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

	// Phase 1: search for clips.
	searchStart := time.Now()
	var clipIDs []string
	var searchResults []SearchResultInfo

	if m.vectorStore != nil && m.clipsRepo != nil && req.MaxClips > 0 {
		// The vectorStore is a multi-backend adapter (typically
		// qdrant.NewSearchAdapter). Match the narrow `narrowClipSearcher`
		// port declared in types.go (PG-033 hoisted it from inline).
		if searcher, ok := m.vectorStore.(narrowClipSearcher); ok {
			results, searchErr := searcher.SearchClips(ctx, query, req.Source, req.MediaType, req.MaxClips, req.MinScore)
			if searchErr != nil && m.log != nil {
				m.log.Warn("media curator: vector search failed, falling back to text-only",
					zap.String("query", query),
					zap.Error(searchErr))
			}
			for _, r := range results {
				clipIDs = append(clipIDs, r.ID)
				searchResults = append(searchResults, SearchResultInfo{
					ClipID:    r.ID,
					Name:      r.Name,
					Score:     r.Score,
					Source:    r.Source,
					DriveLink: r.DriveLink,
				})
			}
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
			if names, ok := pack["clip_names"].([]string); ok {
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
