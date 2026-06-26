// Package scripts — CurationJobServiceImpl extracted from api/script/handler_jobs.go (PR2, June 2026).
package scripts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// CurationJobServiceImpl satisfies the CurationJobService interface
// (defined in api/script/helpers.go) for the script.curate background job.
type CurationJobServiceImpl struct {
	MediaCurator   *MediaCurator
	VOService      *voiceover.Service
	Cfg            *config.Config
	Log            *zap.Logger
	ResolveFolder  func(ctx context.Context, input, defaultRootID string) (string, error)
	GroupsResolver *voiceover.GroupsResolver
	MaybeCreateDoc func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string)
}

// NewCurationJobServiceImpl creates the curation job service.
func NewCurationJobServiceImpl(
	mediaCurator *MediaCurator,
	voService *voiceover.Service,
	cfg *config.Config,
	log *zap.Logger,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	groupsResolver *voiceover.GroupsResolver,
	maybeCreateDoc func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string),
) *CurationJobServiceImpl {
	return &CurationJobServiceImpl{
		MediaCurator:   mediaCurator,
		VOService:      voService,
		Cfg:            cfg,
		Log:            log,
		ResolveFolder:  resolveFolder,
		GroupsResolver: groupsResolver,
		MaybeCreateDoc: maybeCreateDoc,
	}
}

// HandleCurateJob processes a background script.curate job.
func (c *CurationJobServiceImpl) HandleCurateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if c != nil && c.Log != nil {
		c.Log.Info("handling script.curate job", zap.String("job_id", j.ID))
	}

	curator := c.MediaCurator
	if curator == nil {
		return nil, fmt.Errorf("media curator not initialized")
	}

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

	if c != nil && c.Log != nil {
		c.Log.Info("curate job params",
			zap.String("query", payload.Query),
			zap.String("language", lang),
			zap.String("tone", payload.Tone),
			zap.Int("max_clips", payload.MaxClips),
			zap.Int("target_words", payload.TargetWords))
	}

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Searching clips for: %s", payload.Query))
	}

	req := CurateRequest{
		Query:             payload.Query,
		Title:             payload.Title,
		Language:          lang,
		Tone:              payload.Tone,
		Model:             payload.Model,
		MaxClips:          payload.MaxClips,
		SelectableClips:   payload.SelectableClips,
		TargetWords:       payload.TargetWords,
		MaxCharsPerScene:  payload.MaxCharsPerScene,
		MinScore:          payload.MinScore,
		Source:            payload.Source,
		MediaType:         payload.MediaType,
		Type:              payload.Type,
		Style:             payload.Style,
		StyleInstructions: payload.StyleInstructions,
		ForceRefresh:      payload.ForceRefresh,
	}

	if tools.Progress != nil {
		tools.Progress(15, "Semantic search complete, building clip context...")
	}

	result, err := curator.Curate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("curation failed: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(90, "Creating Google Doc...")
	}

	var docLink, docID, docErr string
	docContent := BuildCurateDocContent(result.Title, result.ClipScenes)
	docFolderID := ""
	if c.Cfg != nil {
		docFolderID = c.Cfg.Drive.ScriptsGenFolder()
	}
	if c.MaybeCreateDoc != nil {
		if l, id := c.MaybeCreateDoc(ctx, result.Title, docContent, docFolderID, true); l != "" {
			docLink = l
			docID = id
		}
	}
	if docLink == "" {
		docErr = "google doc creation failed (non-fatal)"
		c.Log.Warn("Google Doc creation failed, continuing without it")
	}

	voiceoverResults := make([]map[string]any, 0)
	if payload.GenerateVoiceover && c.VOService != nil && len(result.ClipScenes) > 0 {
		if tools.Progress != nil {
			tools.Progress(95, "Generating voiceovers for each scene...")
		}

		voRootID := payload.VoiceoverFolderID
		if voRootID == "" && c.Cfg != nil {
			voRootID = c.Cfg.Drive.VoiceoverFolder()
		}
		destReq := BuildVoiceoverDestination(
			ctx, c.ResolveFolder, c.Log, result.Title,
			payload.VoiceoverFolderID, payload.VoiceoverGroup,
			voRootID, c.GroupsResolver,
		)
		if destReq != nil {
			scenes := make([]VoiceoverSceneItem, len(result.ClipScenes))
			for i, sc := range result.ClipScenes {
				scenes[i] = VoiceoverSceneItem{Text: sc.Text, SceneIndex: sc.SceneIndex}
			}
			GenerateSceneVoiceovers(ctx, c.VOService, scenes, lang, destReq, c.Log, tools.Progress, 95, 5)
		}
	}

	if tools.Progress != nil {
		tools.Progress(100, "Curation completed")
	}

	clipScenesJSON := make([]map[string]any, 0, len(result.ClipScenes))
	for _, sc := range result.ClipScenes {
		m := map[string]any{
			"scene_index": sc.SceneIndex,
			"text":        sc.Text,
		}
		if sc.ClipID != "" {
			m["clip_id"] = sc.ClipID
		}
		if sc.DriveLink != "" {
			m["drive_link"] = sc.DriveLink
		}
		clipScenesJSON = append(clipScenesJSON, m)
	}

	searchResultsJSON := make([]map[string]any, 0, len(result.SearchResults))
	for _, sr := range result.SearchResults {
		m := map[string]any{
			"clip_id": sr.ClipID,
			"name":    sr.Name,
			"score":   sr.Score,
		}
		if sr.Source != "" {
			m["source"] = sr.Source
		}
		if sr.DriveLink != "" {
			m["drive_link"] = sr.DriveLink
		}
		searchResultsJSON = append(searchResultsJSON, m)
	}

	response := map[string]any{
		"ok":                 true,
		"title":              result.Title,
		"script":             result.Script,
		"word_count":         result.WordCount,
		"language":           lang,
		"tone":               payload.Tone,
		"cache_status":       result.CacheStatus,
		"accepted_clip_ids":  result.AcceptedClipIDs,
		"clip_scenes":        clipScenesJSON,
		"search_results":     searchResultsJSON,
		"narrative_plan":     result.NarrativePlan,
		"source_text":        result.SourceText,
		"source_fingerprint": result.SourceFingerprint,
		"voiceover_results":  voiceoverResults,
		"timings": map[string]any{
			"search_ms":        result.Timings.SearchMs,
			"build_context_ms": result.Timings.BuildCtxMs,
			"write_script_ms":  result.Timings.WriteScriptMs,
			"total_ms":         result.Timings.TotalMs,
		},
	}
	if scenes, scenesJSON, ok := marshalNormalizedScenes(result.ClipScenes, nil); ok {
		response["scenes"] = scenes
		response["scenes_json"] = scenesJSON
	}

	if docLink != "" {
		response["doc_url"] = docLink
		response["doc_link"] = docLink
		response["doc_id"] = docID
	}
	if docErr != "" {
		response["doc_error"] = docErr
	}

	return response, nil
}
