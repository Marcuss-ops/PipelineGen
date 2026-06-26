// Package scripts — job helpers extracted from api/script/handler_jobs.go (PR2, June 2026).
package scripts

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Pipeline stage logger
func StageLog(log *zap.Logger, jobID, stage string) func(extra ...zap.Field) {
	t := time.Now()
	log.Info("pipeline_stage_started",
		zap.String("job_id", jobID),
		zap.String("stage", stage))
	return func(extra ...zap.Field) {
		fields := append([]zap.Field{
			zap.String("job_id", jobID),
			zap.String("stage", stage),
			zap.Int64("duration_ms", time.Since(t).Milliseconds()),
		}, extra...)
		log.Info("pipeline_stage_completed", fields...)
	}
}

// ── buildVoiceoverDestination ────────────────────────────────────────────────

// BuildVoiceoverDestination builds a *voiceover.DestinationRequest from the
// provided parameters.
func BuildVoiceoverDestination(
	ctx context.Context,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	log *zap.Logger,
	title, voiceoverFolderID, voiceoverGroup, voRootID string,
	groupsResolver *voiceover.GroupsResolver,
) *voiceover.DestinationRequest {
	subfolderName := textutil.SlugifyWithMax(title, 40)

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

// ── Voiceover scene item ────────────────────────────────────────────────────

type VoiceoverSceneItem struct {
	Text       string
	SceneIndex int
}

// ── generateSceneVoiceovers ──────────────────────────────────────────────────

// GenerateSceneVoiceovers generates voiceovers for each scene item.
func GenerateSceneVoiceovers(
	ctx context.Context,
	voService *voiceover.Service,
	scenes []VoiceoverSceneItem,
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
		sceneSlug := textutil.SlugifyWithMax(sceneText, 30)
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

// ── buildCurateDocContent ────────────────────────────────────────────────────

// BuildCurateDocContent builds HTML content for a Google Doc from curate output.
func BuildCurateDocContent(title string, clipScenes []ClipScene) string {
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
				if IsLikelyOutro(sc, clipScenes) {
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

// ── Text helpers ─────────────────────────────────────────────────────────────

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
	// Strip narration/clip markers
	text = textutil.StripNarrationMarkerRe.ReplaceAllString(text, "")
	text = textutil.StripClipMarkerRe.ReplaceAllString(text, "")
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

// IsLikelyOutro checks if a scene is likely an outro.
func IsLikelyOutro(sc ClipScene, all []ClipScene) bool {
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
