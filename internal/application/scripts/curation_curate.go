package scripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Curate runs the full media curation pipeline:
//  1. Search Qdrant for clips matching the query
//  2. Hydrate clips and build evidence cards
//  3. Plan narrative (LLM)
//  4. Generate script (common engine with intro/outro)
//  5. Return complete result
func (s *MediaCurator) Curate(ctx context.Context, req CurateRequest) (*CurateResult, error) {
	req.defaults()
	startAll := time.Now()

	s.log.Info("MediaCurator: starting curation",
		zap.String("query", req.Query),
		zap.String("language", req.Language),
		zap.String("tone", req.Tone),
		zap.String("style", req.Style),
		zap.String("type", req.Type),
		zap.Bool("has_style_instructions", req.StyleInstructions != ""),
		zap.Int("max_clips", req.MaxClips),
		zap.Int("selectable_clips", req.SelectableClips),
		zap.Int("target_words", req.TargetWords))

	if req.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if s.clipBuilder == nil {
		return nil, fmt.Errorf("clip source builder not initialized")
	}
	if s.engine == nil {
		return nil, fmt.Errorf("script engine not initialized")
	}

	// ── Step 1: Search for clips via Qdrant ────────────────────────────────
	searchStart := time.Now()

	searchLimit := req.MaxClips * 3
	if req.SelectableClips > 0 {
		searchLimit = req.SelectableClips
	}
	if searchLimit < 20 {
		searchLimit = 20
	}
	if searchLimit > 60 {
		searchLimit = 60
	}

	searchInfos, err := s.searchClips(ctx, req.Query, req.Source, req.MediaType, searchLimit, req.MinScore)
	if err != nil {
		return nil, fmt.Errorf("semantic search failed: %w", err)
	}

	if len(searchInfos) == 0 {
		s.log.Warn("MediaCurator: no clips found for query",
			zap.String("query", req.Query))
		return nil, fmt.Errorf("no clips found matching query: %q", req.Query)
	}

	s.log.Info("MediaCurator: search results",
		zap.Int("found", len(searchInfos)),
		zap.Duration("took", time.Since(searchStart)))

	clipIDs := make([]string, 0, searchLimit)
	for _, info := range searchInfos {
		clipIDs = append(clipIDs, info.ClipID)
		if len(clipIDs) >= searchLimit {
			break
		}
	}
	searchDurMs := time.Since(searchStart).Milliseconds()

	// ── Step 2: Build clip context (hydrate + evidence + narrative plan) ────
	buildCtxStart := time.Now()

	title := strings.TrimSpace(req.Title)
	if title == "" && len(searchInfos) > 0 {
		if searchInfos[0].Name != "" {
			title = searchInfos[0].Name
		} else {
			title = searchInfos[0].ClipID
		}
	}

	styleInstr := req.resolveStyleInstructions(s.presets)

	opts := &ClipGenerationOptions{
		Language:          req.Language,
		Tone:              req.Tone,
		Title:             title,
		Model:             req.Model,
		TargetWords:       req.TargetWords,
		MaxCharsPerScene:  req.MaxCharsPerScene,
		TranscriptPolicy:  "summary_only",
		OrderingStrategy:  "thematic",
		AllowNoTranscript: true,
		StyleInstructions: styleInstr,
		Type:              req.Type,
	}

	pack, plan, sourceText, err := s.clipBuilder.BuildClipContext(ctx, clipIDs, opts)
	if err != nil {
		return nil, fmt.Errorf("clip context building failed: %w", err)
	}
	if len(pack.Clips) == 0 {
		return nil, fmt.Errorf("no valid clips after validation from %d candidates", len(clipIDs))
	}

	poolSize := req.MaxClips
	if req.SelectableClips > 0 {
		poolSize = req.SelectableClips
	}

	if len(pack.Clips) > poolSize {
		topIDs := make([]string, 0, poolSize)
		for _, c := range pack.Clips[:poolSize] {
			topIDs = append(topIDs, c.ClipID)
		}
		pack, plan, sourceText, err = s.clipBuilder.BuildClipContext(ctx, topIDs, opts)
		if err != nil {
			return nil, fmt.Errorf("clip context rebuild failed: %w", err)
		}
	}

	if plan != nil && len(plan.OrderedClips) > req.MaxClips {
		plan.OrderedClips = plan.OrderedClips[:req.MaxClips]
		pack.Clips = filterClipsByIDs(pack.Clips, plan.OrderedClips)
		sourceText = s.clipBuilder.BuildSourceText(pack, plan, opts)
	}
	buildCtxDurMs := time.Since(buildCtxStart).Milliseconds()

	// ── Step 3: Generate script ────────────────────────────────────────────
	writeStart := time.Now()

	sourceFingerprint := s.clipBuilder.ComputeFingerprint(clipIDs, pack, opts, NewFingerprintContext(opts.Model, opts.Model))

	writeResult, err := s.engine.WriteScript(ctx, WriteScriptRequest{
		Plan: &script.ScriptGenerationPlan{
			Title:       plan.Title,
			Topic:       plan.Title,
			Language:    opts.Language,
			Tone:        opts.Tone,
			Model:       opts.Model,
			Mode:        gemmamemory.ModeClipToScript,
			SourceText:  sourceText,
			TargetWords: opts.TargetWords,
			UseMemory:   !req.ForceRefresh,
			SaveToDB:    false,
			Prompt:      sourceFingerprint,
		},
		Topic:            plan.Title,
		Title:            plan.Title,
		Language:         opts.Language,
		Tone:             opts.Tone,
		Model:            opts.Model,
		Mode:             gemmamemory.ModeClipToScript,
		SourceText:       sourceText,
		MinWords:         opts.TargetWords,
		Prompt:           sourceFingerprint,
		UseMemory:        !req.ForceRefresh,
		SaveToDB:         false,
		SaveTimeout:      60,
		Type:             req.Type,
		ClipPack:         pack,
		MaxCharsPerScene: req.MaxCharsPerScene,
	})
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	wordCount := textutil.CountWords(writeResult.Script)
	writeDurMs := time.Since(writeStart).Milliseconds()

	// ── Step 4: Build clip scenes ──────────────────────────────────────────
	clipScenes := BuildClipScenes(writeResult.Script, pack)

	acceptedIDs := make([]string, 0, len(pack.Clips))
	seen := make(map[string]bool)
	if plan != nil {
		for _, oc := range plan.OrderedClips {
			if !seen[oc.ClipID] {
				seen[oc.ClipID] = true
				acceptedIDs = append(acceptedIDs, oc.ClipID)
			}
		}
	}
	if len(acceptedIDs) == 0 {
		for _, c := range pack.Clips {
			acceptedIDs = append(acceptedIDs, c.ClipID)
		}
	}

	totalDurMs := time.Since(startAll).Milliseconds()

	s.log.Info("MediaCurator: curation complete",
		zap.String("title", plan.Title),
		zap.Int("words", wordCount),
		zap.Int("scenes", len(clipScenes)),
		zap.Int("accepted_clips", len(acceptedIDs)),
		zap.Int64("total_ms", totalDurMs))

	return &CurateResult{
		Title:             plan.Title,
		Script:            writeResult.Script,
		WordCount:         wordCount,
		ClipScenes:        clipScenes,
		AcceptedClipIDs:   acceptedIDs,
		NarrativePlan:     plan,
		SourceText:        sourceText,
		SourceFingerprint: sourceFingerprint,
		SearchResults:     searchInfos[:min(len(searchInfos), len(acceptedIDs))],
		CacheStatus:       writeResult.CacheStatus,
		Timings: CurateTimings{
			SearchMs:      searchDurMs,
			BuildCtxMs:    buildCtxDurMs,
			WriteScriptMs: writeDurMs,
			TotalMs:       totalDurMs,
		},
	}, nil
}
