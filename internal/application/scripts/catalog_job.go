package scripts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// CatalogJobServiceImpl handles background script.generate_from_catalog jobs.
type CatalogJobServiceImpl struct {
	ClipSourceBuilder *ClipSourceBuilder
	Engine            *Engine
	CatalogSearch     appsearch.LocalCatalogPort
	Log               *zap.Logger
}

// NewCatalogJobServiceImpl creates a catalog job service.
func NewCatalogJobServiceImpl(clipSourceBuilder *ClipSourceBuilder, engine *Engine, catalogSearch appsearch.LocalCatalogPort, log *zap.Logger) *CatalogJobServiceImpl {
	return &CatalogJobServiceImpl{
		ClipSourceBuilder: clipSourceBuilder,
		Engine:            engine,
		CatalogSearch:     catalogSearch,
		Log:               log,
	}
}

// HandleCatalogScriptGenerateJob processes a background script.generate_from_catalog job.
func (s *CatalogJobServiceImpl) HandleCatalogScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if s != nil && s.Log != nil {
		s.Log.Info("handling script.generate_from_catalog job", zap.String("job_id", j.ID))
	}

	if s == nil || s.ClipSourceBuilder == nil {
		return nil, fmt.Errorf("clip source builder not initialized")
	}
	if s.Engine == nil {
		return nil, fmt.Errorf("script engine not initialized")
	}

	var payload JobPayloadCatalogScript
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}
	payload.Languages = NormalizeLanguages(payload.Languages)

	topic := strings.TrimSpace(payload.Topic)
	if topic == "" {
		topic = strings.TrimSpace(payload.Title)
	}
	if topic == "" {
		topic = strings.TrimSpace(payload.OutputName)
	}
	if topic == "" {
		topic = strings.Join(payload.ClipIDs, " ")
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		title = topic
	}
	if title == "" {
		title = "catalog script"
	}
	payload.Title = title
	if payload.MaxClips <= 0 {
		payload.MaxClips = 10
	}

	if len(payload.ClipIDs) == 0 {
		selected, err := s.selectCatalogClipIDs(ctx, topic, payload.MaxClips, payload.MinCoverage)
		if err != nil {
			return nil, err
		}
		payload.ClipIDs = selected
	}
	if len(payload.ClipIDs) == 0 {
		return nil, fmt.Errorf("no catalog clips found for topic %q", topic)
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Loading %d clips selected from catalog", len(payload.ClipIDs)))
	}

	lang := strings.TrimSpace(payload.Language)
	if lang == "" && len(payload.Languages) > 0 {
		lang = payload.Languages[0]
	}
	if lang == "" {
		lang = "en"
	}
	opts := &ClipGenerationOptions{
		Language:         lang,
		Tone:             strings.TrimSpace(payload.Tone),
		Title:            title,
		Model:            strings.TrimSpace(payload.Model),
		TargetWords:      payload.TargetWords,
		TranscriptPolicy: strings.TrimSpace(payload.TranscriptPolicy),
		OrderingStrategy: strings.TrimSpace(payload.OrderingStrategy),
	}
	if payload.MinQualityScore != nil {
		opts.MinQualityScore = *payload.MinQualityScore
	}
	if payload.MinTranscriptWords != nil {
		opts.MinTranscriptWords = *payload.MinTranscriptWords
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(15, "Hydrating clips and building evidence cards")
	}

	pack, plan, sourceText, err := s.ClipSourceBuilder.BuildClipContext(ctx, payload.ClipIDs, opts)
	if err != nil {
		return nil, fmt.Errorf("clip context building failed: %w", err)
	}
	if plan == nil {
		plan = &NarrativePlan{Title: title}
	}

	sourceFingerprint := s.ClipSourceBuilder.ComputeFingerprint(payload.ClipIDs, pack, opts, NewFingerprintContext(opts.Model, opts.Model))

	if tools != nil && tools.Progress != nil {
		tools.Progress(50, "Generating script via common engine (MemoryGate, normalization)...")
	}

	writeResult, err := s.Engine.WriteScript(ctx, WriteScriptRequest{
		Plan: &scriptpkg.ScriptGenerationPlan{
			Title:       plan.Title,
			Topic:       plan.Title,
			Language:    opts.Language,
			Tone:        opts.Tone,
			Model:       opts.Model,
			Mode:        gemmamemory.ModeClipToScript,
			SourceText:  sourceText,
			TargetWords: opts.TargetWords,
			UseMemory:   !payload.ForceRefresh,
			SaveToDB:    payload.SaveToDB,
			Prompt:      sourceFingerprint,
		},
		Topic:       plan.Title,
		Title:       plan.Title,
		Language:    opts.Language,
		Tone:        opts.Tone,
		Model:       opts.Model,
		Mode:        gemmamemory.ModeClipToScript,
		SourceText:  sourceText,
		MinWords:    opts.TargetWords,
		Prompt:      sourceFingerprint,
		UseMemory:   !payload.ForceRefresh,
		SaveToDB:    payload.SaveToDB,
		SaveTimeout: 60,
	})
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	wordCount := textutil.CountWords(writeResult.Script)
	acceptedClips, excludedClips, excludedDetails := summarizeClipPack(pack)

	if tools != nil && tools.Progress != nil {
		tools.Progress(100, "Catalog-first generation completed")
	}

	result := map[string]any{
		"ok":           true,
		"script_id":    writeResult.ScriptID,
		"title":        plan.Title,
		"script":       writeResult.Script,
		"word_count":   wordCount,
		"language":     opts.Language,
		"languages":    append([]string(nil), payload.Languages...),
		"mode":         "catalog_first",
		"cache_status": writeResult.CacheStatus,
		"clip_coverage": map[string]any{
			"requested": maxInt(payload.MaxClips, len(payload.ClipIDs)),
			"accepted":  acceptedClips,
			"used":      acceptedClips,
			"excluded":  excludedClips,
		},
		"narrative_plan":     plan,
		"source_fingerprint": sourceFingerprint,
	}

	if len(excludedDetails) > 0 {
		result["excluded_clips"] = excludedDetails
	}
	if plan != nil {
		result["sections_count"] = len(plan.Sections)
	}

	return result, nil
}

func (s *CatalogJobServiceImpl) selectCatalogClipIDs(ctx context.Context, topic string, limit int, minCoverage float64) ([]string, error) {
	if s.CatalogSearch == nil {
		return nil, fmt.Errorf("catalog search service not initialized")
	}
	query := strings.TrimSpace(topic)
	if query == "" {
		return nil, fmt.Errorf("topic is required")
	}
	if limit <= 0 {
		limit = 10
	}
	results, err := s.CatalogSearch.SearchAll(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("catalog search failed: %w", err)
	}
	seen := make(map[string]struct{}, limit)
	clipIDs := make([]string, 0, limit)
	for _, r := range results {
		id := strings.TrimSpace(r.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clipIDs = append(clipIDs, id)
		if len(clipIDs) >= limit {
			break
		}
	}
	if minCoverage > 0 && limit > 0 {
		coverage := float64(len(clipIDs)) / float64(limit)
		if coverage < minCoverage {
			return nil, fmt.Errorf("catalog coverage %.2f below required minimum %.2f for topic %q", coverage, minCoverage, query)
		}
	}
	return clipIDs, nil
}

// summarizeClipPack produces the `accepted` integer for the
// `clip_coverage` telemetry block in the catalog-job response.
// PG-033: pack is now `map[string]any` (the canonical shape returned
// by ClipSourceBuilder.BuildClipContext, which populates a
// `"clip_count"` int key). The previous reflection-based version
// looked up struct fields ("Clips" / "ExcludedClips") that no longer
// exist — silently returning 0/0/nil in production.
//
// Excluded-clip reasons are no longer surfaced by this telemetry
// channel. If/when operator-facing exclusion telemetry is needed,
// extend BuildClipContext to populate an "excluded_clips" key with
// {clip_id, reason} entries and re-introduce the detail extraction
// here.
func summarizeClipPack(pack map[string]any) (accepted int, excluded int, excludedDetails []map[string]any) {
	if pack == nil {
		return 0, 0, nil
	}
	if v, ok := pack["clip_count"].(int); ok {
		accepted = v
	}
	return accepted, 0, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
