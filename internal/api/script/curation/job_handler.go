package curation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/curation"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/content/mediacurator"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
)

// Service handles background script.curate jobs.
type Service struct {
	mediaCurator    *mediacurator.Service
	voService       *voiceover.Service
	cfg             *config.Config
	log             *zap.Logger
	resolveFolder   func(ctx context.Context, input, defaultRootID string) (string, error)
	groupsResolver  *voiceover.GroupsResolver
	maybeCreateDoc  func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string)
}

// NewService creates a new curation job service.
func NewService(
	mediaCurator *mediacurator.Service,
	voService *voiceover.Service,
	cfg *config.Config,
	log *zap.Logger,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	groupsResolver *voiceover.GroupsResolver,
	maybeCreateDoc func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string),
) *Service {
	return &Service{
		mediaCurator:   mediaCurator,
		voService:      voService,
		cfg:            cfg,
		log:            log,
		resolveFolder:  resolveFolder,
		groupsResolver: groupsResolver,
		maybeCreateDoc: maybeCreateDoc,
	}
}

// HandleCurateJob processes a background script.curate job.
func (s *Service) HandleCurateJob(ctx context.Context, job *jobservice.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("handling script.curate job", zap.String("job_id", job.ID))

	curator := s.mediaCurator
	if curator == nil {
		return nil, fmt.Errorf("media curator not initialized")
	}

	var payload curation.JobPayloadCurate
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}

	s.log.Info("curate job params",
		zap.String("query", payload.Query),
		zap.String("language", payload.Language),
		zap.String("tone", payload.Tone),
		zap.Int("max_clips", payload.MaxClips),
		zap.Int("target_words", payload.TargetWords))

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Searching clips for: %s", payload.Query))
	}

	req := mediacurator.CurateRequest{
		Query:             payload.Query,
		Title:             payload.Title,
		Language:          payload.Language,
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
	docContent := buildCurateDocContent(result.Title, result.ClipScenes)
	if s.maybeCreateDoc != nil {
		if l, id := s.maybeCreateDoc(ctx, result.Title, docContent, "", true); l != "" {
			docLink = l
			docID = id
		}
	}
	if docLink == "" {
		docErr = "google doc creation failed (non-fatal)"
		s.log.Warn("Google Doc creation failed, continuing without it")
	}

	voiceoverResults := make([]map[string]any, 0)
	if payload.GenerateVoiceover && s.voService != nil && len(result.ClipScenes) > 0 {
		if tools.Progress != nil {
			tools.Progress(95, "Generating voiceovers for each scene...")
		}

		voRootID := payload.VoiceoverFolderID
		if voRootID == "" && s.cfg != nil {
			voRootID = s.cfg.Drive.VoiceoverFolder()
		}
		destReq := buildVoiceoverDestination(
			ctx, s.resolveFolder, s.log, result.Title,
			payload.VoiceoverFolderID, payload.VoiceoverGroup,
			voRootID, s.groupsResolver,
		)
		if destReq != nil {
			scenes := make([]voiceoverSceneItem, len(result.ClipScenes))
			for i, sc := range result.ClipScenes {
				scenes[i] = voiceoverSceneItem{Text: sc.Text, SceneIndex: sc.SceneIndex}
			}
			generateSceneVoiceovers(ctx, s.voService, scenes, payload.Language, destReq, s.log, tools.Progress, 95, 5)
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
		"language":           payload.Language,
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

	if docLink != "" {
		response["doc_link"] = docLink
		response["doc_id"] = docID
	}
	if docErr != "" {
		response["doc_error"] = docErr
	}

	return response, nil
}

// ── Helpers (moved from script package) ────────────────────────────────────

// voiceoverSceneItem describes a scene for voiceover generation.
type voiceoverSceneItem struct {
	Text       string
	SceneIndex int
}

// buildVoiceoverDestination builds a *voiceover.DestinationRequest from the
// provided parameters.
func buildVoiceoverDestination(
	ctx context.Context,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	log *zap.Logger,
	title, voiceoverFolderID, voiceoverGroup, voRootID string,
	groupsResolver *voiceover.GroupsResolver,
) *voiceover.DestinationRequest {
	subfolderName := slugifyWithMax(title, 40)

	if folderID := strings.TrimSpace(voiceoverFolderID); folderID != "" {
		return &voiceover.DestinationRequest{
			FolderID:        folderID,
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

	if groupsResolver != nil && strings.TrimSpace(voiceoverGroup) != "" {
		entry, err := groupsResolver.ResolveByName(ctx, voRootID, voiceoverGroup)
		switch {
		case err == nil && entry.FolderID != "":
			if log != nil {
				log.Info("routed voiceover via DB groups_resolver",
					zap.String("voiceover_group", voiceoverGroup),
					zap.String("folder_id", entry.FolderID),
					zap.String("parent_id", voRootID))
			}
			return &voiceover.DestinationRequest{
				FolderID:        entry.FolderID,
				SubfolderName:   subfolderName,
				CreateSubfolder: true,
			}
		case err != nil && !errors.Is(err, voiceover.ErrGroupNotFound):
			if log != nil {
				log.Warn("groups_resolver lookup failed unexpectedly, falling back to Drive deep-search",
					zap.String("voiceover_group", voiceoverGroup),
					zap.Error(err))
			}
		}
	}

	targetFolderOrGroup := voiceoverGroup
	if targetFolderOrGroup != "" && resolveFolder != nil {
		resolvedVOFolder, err := resolveFolder(ctx, targetFolderOrGroup, voRootID)
		if err != nil {
			if log != nil {
				log.Warn("failed to resolve custom voiceover folder name/path, using default root", zap.Error(err))
			}
			resolvedVOFolder = voRootID
		}
		if resolvedVOFolder != "" {
			return &voiceover.DestinationRequest{
				FolderID:        resolvedVOFolder,
				SubfolderName:   subfolderName,
				CreateSubfolder: true,
			}
		}
	}

	if voRootID != "" {
		return &voiceover.DestinationRequest{
			FolderID:        voRootID,
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

	grp := voiceoverGroup
	if grp == "" {
		grp = "curation"
	}
	return &voiceover.DestinationRequest{
		Group:           grp,
		SubfolderName:   subfolderName,
		CreateSubfolder: true,
	}
}

// generateSceneVoiceovers generates voiceovers for each scene item.
func generateSceneVoiceovers(
	ctx context.Context,
	voService *voiceover.Service,
	scenes []voiceoverSceneItem,
	language string,
	destReq *voiceover.DestinationRequest,
	log *zap.Logger,
	onProgress func(pct int, msg string),
	basePct, pctRange int,
) int {
	if voService == nil || destReq == nil || len(scenes) == 0 {
		return 0
	}
	voCtx := context.WithoutCancel(ctx)
	successCount := 0
	for i, sc := range scenes {
		sceneText := strings.TrimSpace(sc.Text)
		if sceneText == "" {
			continue
		}
		sceneSlug := slugifyWithMax(sceneText, 30)
		filename := sceneSlug

		if onProgress != nil && len(scenes) > 0 {
			onProgress(basePct+(i*pctRange/len(scenes)), "")
		}

		voRes, voErr := voService.GenerateWithDestination(voCtx, sceneText, language, filename, destReq)
		if voErr != nil {
			if log != nil {
				log.Warn("voiceover generation failed for scene",
					zap.Int("scene_index", sc.SceneIndex),
					zap.Error(voErr))
			}
			continue
		}
		if voRes != nil {
			successCount++
		}
	}
	return successCount
}

func slugifyWithMax(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' || r == '_' {
			b.WriteRune('-')
		}
	}
	result := b.String()
	result = strings.Trim(result, "-")
	if len(result) > max {
		result = result[:max]
		result = strings.TrimRight(result, "-")
	}
	return result
}

// buildCurateDocContent builds HTML content for a Google Doc from curate output.
func buildCurateDocContent(title string, clipScenes []scripts.ClipScene) string {
	var b strings.Builder
	b.WriteString("<html><head><style>")
	b.WriteString("body { font-family: Arial, Helvetica, sans-serif; font-size: 11pt; line-height: 1.4; margin: 20px; }")
	b.WriteString("h1 { font-family: Arial, sans-serif; font-size: 16pt; font-weight: bold; }")
	b.WriteString("h2 { font-family: Arial, sans-serif; font-size: 13pt; font-weight: bold; margin-top: 20px; }")
	b.WriteString("p { font-family: Arial, Helvetica, sans-serif; font-size: 11pt; line-height: 1.6; margin: 10px 0; }")
	b.WriteString(".scene-label { font-family: Arial, sans-serif; font-size: 10pt; color: #666; margin-top: 18px; margin-bottom: 2px; }")
	b.WriteString(".scene-meta { font-family: Arial, sans-serif; font-size: 9pt; color: #444; font-style: italic; margin: 2px 0 4px 4px; }")
	b.WriteString(".scene-preview { font-family: Arial, sans-serif; font-size: 9pt; color: #555; background: #f7f7f7; padding: 6px 10px; border-left: 3px solid #ccc; margin: 4px 0 6px 4px; }")
	b.WriteString(".drive-link { font-family: Arial, sans-serif; font-size: 9pt; color: #1a73e8; margin: 4px 0 6px 4px; }")
	b.WriteString("</style></head><body>")
	b.WriteString("<h1>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</h1>")

	for _, sc := range clipScenes {
		words := countWords(sc.Text)
		duration := approxReadingSeconds(words)

		if sc.ClipID != "" {
			b.WriteString(fmt.Sprintf(
				"<p class=\"scene-label\">🎬 Scene %d — Clip: %s</p>",
				sc.SceneIndex, html.EscapeString(sc.ClipID)))
		} else {
			label := "Intro"
			if sc.SceneIndex > 1 {
				if isLikelyOutro(sc, clipScenes) {
					label = "Outro"
				} else {
					label = "Transition"
				}
			}
			fmt.Fprintf(&b, "<p class=\"scene-label\">📝 Scene %d — %s</p>", sc.SceneIndex, label)
		}

		b.WriteString(fmt.Sprintf(
			"<p class=\"scene-meta\">~%d words · ~%ds read</p>",
			words, duration))

		if preview := firstSentencePreview(sc.Text, 140); preview != "" {
			b.WriteString("<p class=\"scene-preview\">")
			b.WriteString(html.EscapeString(preview))
			b.WriteString("</p>")
		}

		if sc.ClipID != "" && sc.DriveLink != "" {
			b.WriteString(fmt.Sprintf(
				"<p class=\"drive-link\"><a href=\"%s\">Drive link</a></p>",
				html.EscapeString(sc.DriveLink)))
		}

		for _, para := range strings.Split(sc.Text, "\n") {
			para = strings.TrimSpace(para)
			if para != "" {
				b.WriteString("<p>")
				b.WriteString(html.EscapeString(para))
				b.WriteString("</p>")
			}
		}
	}

	b.WriteString("</body></html>")
	return b.String()
}

func countWords(text string) int {
	return len(strings.Fields(text))
}

func approxReadingSeconds(words int) int {
	if words <= 0 {
		return 0
	}
	return max(1, (words*60)/150)
}

func firstSentencePreview(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = narrationMarkerRe.ReplaceAllString(text, "")
	text = clipMarkerRe.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	cutAt := -1
	for _, sep := range []string{". ", "!\n", "?\n", ".\n"} {
		if i := strings.Index(text, sep); i > 0 {
			if cutAt < 0 || i < cutAt {
				cutAt = i + len(sep)
			}
		}
	}
	preview := text
	if cutAt > 0 {
		preview = text[:cutAt]
	}
	preview = strings.TrimRight(preview, " \t\n")
	preview = strings.TrimSuffix(preview, ".")

	if len(preview) > maxChars {
		truncated := preview[:maxChars]
		if i := strings.LastIndex(truncated, " "); i > maxChars/2 {
			truncated = truncated[:i]
		}
		preview = strings.TrimRight(truncated, " ,;:") + "..."
	} else {
		preview += "."
	}
	return preview
}

func isLikelyOutro(sc scripts.ClipScene, all []scripts.ClipScene) bool {
	if sc.ClipID != "" {
		return false
	}
	if sc.SceneIndex == len(all) {
		return true
	}
	narrationAfter := 0
	for _, c := range all {
		if c.SceneIndex > sc.SceneIndex && c.ClipID == "" {
			narrationAfter++
		}
	}
	return narrationAfter == 0
}

var narrationMarkerRe = regexp.MustCompile(`(?m)^\s*\[Narration:\s*[a-z_]+\s*\]\s*`)
var clipMarkerRe = regexp.MustCompile(`(?m)^\s*\[Clip:\s*[^\]]+\s*\]\s*`)
