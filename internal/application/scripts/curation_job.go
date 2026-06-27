// Package scripts — CurationJobServiceImpl extracted from api/script/handler_jobs.go (PR2, June 2026).
//
// PR 11 (June 2026): migrated to the unified pipeline. HandleCurateJob
// now translates the legacy JobPayloadCurate to a GenerationEnvelopeV2
// and delegates to GenerateOneUseCase.Execute. The old MediaCurator.Curate
// code path is no longer used for curation jobs.
package scripts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// CurationJobServiceImpl satisfies the CurationJobService interface
// (defined in api/script/helpers.go) for the script.curate background job.
type CurationJobServiceImpl struct {
	One           *GenerateOneUseCase
	Cfg           *config.Config
	Log           *zap.Logger
	ResolveFolder func(ctx context.Context, input, defaultRootID string) (string, error)
	MaybeCreateDoc func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string)
}

// NewCurationJobServiceImpl creates the curation job service.
func NewCurationJobServiceImpl(
	one *GenerateOneUseCase,
	cfg *config.Config,
	log *zap.Logger,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	maybeCreateDoc func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string),
) *CurationJobServiceImpl {
	return &CurationJobServiceImpl{
		One:           one,
		Cfg:           cfg,
		Log:           log,
		ResolveFolder: resolveFolder,
		MaybeCreateDoc: maybeCreateDoc,
	}
}

// HandleCurateJob processes a background media.curate job.
// PR 11 (June 2026): migrated to the unified pipeline. Translates
// the legacy JobPayloadCurate to a GenerationEnvelopeV2 and delegates
// to GenerateOneUseCase.Execute. The old MediaCurator.Curate path is
// superseded.
func (c *CurationJobServiceImpl) HandleCurateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if c != nil && c.Log != nil {
		c.Log.Info("handling media.curate job (unified pipeline)", zap.String("job_id", j.ID))
	}

	if c.One == nil {
		return nil, fmt.Errorf("curation job handler: GenerateOneUseCase not initialized")
	}

	// Decode legacy payload.
	var payload JobPayloadCurate
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}

	payload.Languages = NormalizeLanguages(payload.Languages)
	lang := strings.TrimSpace(payload.Language)
	if lang == "" && len(payload.Languages) > 0 {
		lang = payload.Languages[0]
	}
	if lang == "" {
		lang = "en"
	}

	// Translate to GenerationEnvelopeV2 item.
	item := translateCuratePayloadToItem(payload, lang)

	if tools != nil && tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Curating: %s", payload.Query))
	}

	// Run through the unified pipeline.
	tracker := NewProgressTracker(func(pct int, msg string) {
		if tools != nil && tools.Progress != nil {
			tools.Progress(pct, msg)
		}
	}, "curate")

	result, err := c.One.Execute(ctx, item, domainScript.PresetCustom, tracker)
	if err != nil {
		return nil, fmt.Errorf("curation via unified pipeline failed: %w", err)
	}

	return buildCurateResponse(payload, result, lang), nil
}

// translateCuratePayloadToItem converts a legacy JobPayloadCurate
// to a GenerationItemV2. Curation queries map to catalog-source
// generation items.
func translateCuratePayloadToItem(payload JobPayloadCurate, lang string) domainScript.GenerationItemV2 {
	item := domainScript.GenerationItemV2{
		Title:    payload.Title,
		Language: lang,
		Tone:     payload.Tone,
		Model:    payload.Model,
		Style:    payload.Style,
		Source: domainScript.SourceSpec{
			Type:         domainScript.SourceCatalog,
			Query:        payload.Query,
			MaxClips:     payload.MaxClips,
			ForceRefresh: payload.ForceRefresh,
		},
		ScriptParams: domainScript.ScriptSpec{
			TargetWords:  payload.TargetWords,
			Guidelines:   payload.StyleInstructions,
			ForceRefresh: payload.ForceRefresh,
		},
		Output: domainScript.OutputSpec{
			SaveToDB:          true,
			GenerateVoiceover: payload.GenerateVoiceover,
			GenerateDocument:  true,
			VoiceoverGroup:    payload.VoiceoverGroup,
			VoiceoverFolderID: payload.VoiceoverFolderID,
			Languages:         payload.Languages,
		},
	}
	if payload.MinScore > 0 {
		score := payload.MinScore
		item.Source.MinQualityScore = &score
	}
	if payload.Title == "" {
		item.Title = payload.Query
	}
	return item
}

// buildCurateResponse converts a GenerationResult to the legacy
// curate response shape. This preserves backward compatibility for
// consumers reading job results.
// PR 11 (June 2026): simplified — SourceText/Fingerprint are not
// carried on GenerationResult; the old CurateResult fields are
// dropped in favor of the canonical nested shape.
func buildCurateResponse(payload JobPayloadCurate, result *domainScript.GenerationResult, lang string) map[string]any {
	response := map[string]any{
		"ok":             true,
		"title":          result.Title,
		"script":         result.Output.Text,
		"word_count":     result.Output.WordCount,
		"language":       lang,
		"tone":           payload.Tone,
		"cache_status":   result.Cache.Status,
		"cache_hit":      result.Cache.Hit,
		"voiceover_results": make([]map[string]any, 0),
		"timings": map[string]any{
			"total_ms": result.Timings.TotalMs,
		},
	}
	if result.Artifacts.Document != nil {
		response["doc_url"] = result.Artifacts.Document.DocLink
		response["doc_link"] = result.Artifacts.Document.DocLink
		response["doc_id"] = result.Artifacts.Document.DocID
	}
	return response
}
