// Package scripts — media_curator.go is a pure source resolver
// that searches clips semantically via Qdrant and builds clip
// context. It does NOT generate scripts — PR D.3 removed the
// GenerateOneUseCase dependency.
//
// Flow:
//   1. Searches for clips via ClipSearchPort and/or HintClipIDs
//   2. Builds clip context via ClipSourceBuilder
//   3. Returns resolved CurateResult with clip evidence
//
// Script generation is handled downstream by GenerateOneUseCase
// which consumes the resolved source text + clip evidence.
package scripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	"go.uber.org/zap"
)

// NewMediaCurator constructs a pure source resolver backed by clips
// repository and ClipSourceBuilder. All concrete typed args.
//
//   - clipsRepo may be nil (Curate returns an error when missing).
//   - clipBuilder is required for the clip context building leg.
//   - clipSearch is the optional ClipSearchPort for semantic-search.
//     nil → consumes only req.HintClipIDs.
func NewMediaCurator(
	serverURL string,
	clipsRepo *assets.ClipsRepository,
	clipBuilder *ClipSourceBuilder,
	log *zap.Logger,
) *MediaCurator {
	return &MediaCurator{
		serverURL:   serverURL,
		clipsRepo:   clipsRepo,
		clipBuilder: clipBuilder,
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

// Curate searches for clips matching the query and builds clip context.
// It is a pure source resolver — no script generation occurs here.
//
// Flow:
//  1. Pick up clip IDs from semantic-search and/or HintClipIDs.
//  2. Build clip context via ClipSourceBuilder.
//  3. Return resolved CurateResult with clip evidence.
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
	searchStart := time.Now()
	clipIDs := make([]string, 0)
	searchResults := make([]SearchResultInfo, 0)
	seen := make(map[string]struct{})

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
			Category:  "",
			MediaType: req.MediaType,
			Limit:     req.MaxClips,
			MinScore:  req.MinScore,
		})
		if searchErr != nil {
			if m.log != nil {
				m.log.Warn("clip search port returned error (falling back to hint list)", zap.Error(searchErr))
			}
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


	totalMs := time.Since(startAll).Milliseconds()

	if m.log != nil {
		m.log.Info("media curator: source resolution completed",
			zap.String("title", title),
			zap.Int("clips_found", len(clipIDs)),
			zap.Int64("search_ms", searchMs),
			zap.Int64("build_ctx_ms", buildCtxMs),
			zap.Int64("total_ms", totalMs))
	}

	return &CurateResult{
		Title:             title,
		ClipScenes:        clipScenes,
		AcceptedClipIDs:   clipIDs,
		NarrativePlan:     narrativePlan,
		SourceText:        sourceText,
		SourceFingerprint: sourceFingerprint,
		SearchResults:     searchResults,
		Timings: CurateTimings{
			SearchMs:   searchMs,
			BuildCtxMs: buildCtxMs,
			TotalMs:    totalMs,
		},
	}, nil
}
