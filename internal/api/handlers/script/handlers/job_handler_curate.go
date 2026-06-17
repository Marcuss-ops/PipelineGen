package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"

	"go.uber.org/zap"

	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/service/mediacurator"
	"velox/go-master/internal/service/scriptcore"
)

// HandleCurateJob processes a background script.curate job.
//
// Pipeline:
//  1. Parse the natural language query from the job payload
//  2. Call MediaCurator.Curate() which internally:
//     a. Searches Qdrant for matching clips
//     b. Hydrates clips and builds evidence cards
//     c. Plans narrative (LLM step 1)
//     d. Generates script (common engine with intro/outro)
//  3. Return the complete result with clip scenes, search results, timings
func (h *ScriptFlowHandler) HandleCurateJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	h.log.Info("handling script.curate job", zap.String("job_id", job.ID))

	curator := h.mediaCurator
	if curator == nil {
		return nil, fmt.Errorf("media curator not initialized")
	}

	var payload jobPayloadCurate
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}

	h.log.Info("curate job params",
		zap.String("query", payload.Query),
		zap.String("language", payload.Language),
		zap.String("tone", payload.Tone),
		zap.Int("max_clips", payload.MaxClips),
		zap.Int("target_words", payload.TargetWords))

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Searching clips for: %s", payload.Query))
	}

	// ── Step 1: Run curation ───────────────────────────────────────────────
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

	// ── Step 2: Create Google Doc (error-resilient) ────────────────────────
	var docLink, docID, docErr string
	docContent := buildCurateDocContent(result.Title, result.ClipScenes)
	if l, id := h.maybeCreateGoogleDoc(ctx, result.Title, docContent, "", true); l != "" {
		docLink = l
		docID = id
	} else {
		docErr = "google doc creation failed (non-fatal)"
		h.log.Warn("Google Doc creation failed, continuing without it")
	}

	// ── Step 3 (optional): Generate voiceovers per scene ────────────────────
	voiceoverResults := make([]map[string]any, 0)
	if payload.GenerateVoiceover && h.voService != nil && len(result.ClipScenes) > 0 {
		if tools.Progress != nil {
			tools.Progress(95, "Generating voiceovers for each scene...")
		}

		voRootID := payload.VoiceoverFolderID
		if voRootID == "" && h.cfg != nil {
			voRootID = h.cfg.Drive.VoiceoverFolder()
		}
		destReq := buildVoiceoverDestination(ctx, h.resolveDriveFolderID, h.log, result.Title, payload.VoiceoverFolderID, payload.VoiceoverGroup, voRootID, h.groupsResolver)
		if destReq != nil {
			scenes := make([]voiceoverSceneItem, len(result.ClipScenes))
			for i, sc := range result.ClipScenes {
				scenes[i] = voiceoverSceneItem{Text: sc.Text, SceneIndex: sc.SceneIndex}
			}
			generateSceneVoiceovers(ctx, h.voService, scenes, payload.Language, destReq, h.log, tools.Progress, 95, 5)
		}
	}

	if tools.Progress != nil {
		tools.Progress(100, "Curation completed")
	}

	// ── Step 3: Build result ───────────────────────────────────────────────
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

// buildCurateDocContent builds HTML content for a Google Doc from curate output.
// Shows each scene as readable paragraphs, not raw JSON with escaped \n.
//
// Per-scene template version: 2026-06-17. Each scene gets:
//   - A label (clip id OR intro/outro marker)
//   - A "description" line: what happens next (first sentence preview) + word count + read-time
//   - A clean drive link (no emoji decoration)
//   - The narration prose itself
//
// The description line gives a reader (or LLM consumer) a fast per-scene
// snapshot — useful for compilations where the viewer wants to skim the
// lineup before reading the prose.
func buildCurateDocContent(title string, clipScenes []scriptcore.ClipScene) string {
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

		// ── Label: clip scene vs narration scene ──────────────────────────
		if sc.ClipID != "" {
			b.WriteString(fmt.Sprintf(
				"<p class=\"scene-label\">🎬 Scene %d — Clip: %s</p>",
				sc.SceneIndex, html.EscapeString(sc.ClipID)))
		} else {
			label := "Intro"
			if sc.SceneIndex > 1 {
				// Crude but reliable: opening scene = 1, the last narration-only
				// scene in a list is "Outro". With clip scenes mixed in, anything
				// in the middle without a clip id is "Transition".
				if isLikelyOutro(sc, clipScenes) {
					label = "Outro"
				} else {
					label = "Transition"
				}
			}
			fmt.Fprintf(&b, "<p class=\"scene-label\">📝 Scene %d — %s</p>", sc.SceneIndex, label)
		}

		// ── Description: word count + read-time + "what happens" preview ───
		b.WriteString(fmt.Sprintf(
			"<p class=\"scene-meta\">~%d words · ~%ds read</p>",
			words, duration))

		if preview := firstSentencePreview(sc.Text, 140); preview != "" {
			b.WriteString("<p class=\"scene-preview\">")
			b.WriteString(html.EscapeString(preview))
			b.WriteString("</p>")
		}

		// ── Drive link (no emoji decoration) ───────────────────────────────
		if sc.ClipID != "" && sc.DriveLink != "" {
			b.WriteString(fmt.Sprintf(
				"<p class=\"drive-link\"><a href=\"%s\">Drive link</a></p>",
				html.EscapeString(sc.DriveLink)))
		}

		// ── Narration text: split by newlines into paragraphs ──────────────
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

// countWords returns the number of whitespace-separated tokens in text.
// Empty / whitespace-only input returns 0.
func countWords(text string) int {
	return len(strings.Fields(text))
}

// approxReadingSeconds estimates voiceover read-time at the standard 150wpm
// narration pace. Returns 0 for empty input (avoid showing "0s").
func approxReadingSeconds(words int) int {
	if words <= 0 {
		return 0
	}
	secs := (words * 60) / 150
	if secs < 1 {
		secs = 1
	}
	return secs
}

// firstSentencePreview returns up to maxChars from the prose's first sentence
// so a reader gets a one-line "what happens next" hint. Falls back to the first
// maxChars of prose when no sentence boundary is reachable. Trims and
// ellipsizes cleanly. Returns "" for empty input.
func firstSentencePreview(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// Strip scene markers — they're handles, not prose. [Narration: ...] AND
	// [Clip: <id>] both leak into Text when the LLM keeps the source structure;
	// the preview must lead with the narrative sentence, not a handle.
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
				cutAt = i + len(sep) // include the separator character
			}
		}
	}
	preview := text
	if cutAt > 0 {
		preview = text[:cutAt]
	}
	preview = strings.TrimRight(preview, " \t\n")
	preview = strings.TrimSuffix(preview, ".") // we'll re-add a single trailing dot + ellipsis

	if len(preview) > maxChars {
		// Cut on the last space before maxChars so we don't split mid-word.
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

// isLikelyOutro returns true if `sc` is the LAST scene in the list AND has no
// clip_id, OR it's the second scene with no clip_id when there are 2+
// narration-only scenes. Used to label narration-only scenes as Intro/Outro/
// Transition for the doc reader.
func isLikelyOutro(sc scriptcore.ClipScene, all []scriptcore.ClipScene) bool {
	if sc.ClipID != "" {
		return false
	}
	if sc.SceneIndex == len(all) {
		return true
	}
	// Count how many narration-only scenes appear AFTER this one. If it's the
	// last narration-only scene and there are >=1 before it, it's Outro.
	narrationAfter := 0
	for _, c := range all {
		if c.SceneIndex > sc.SceneIndex && c.ClipID == "" {
			narrationAfter++
		}
	}
	return narrationAfter == 0
}

// narrationMarkerRe strips scene markers like [Narration: opening] so the
// preview doesn't lead with the marker name. Compilation choice.
var narrationMarkerRe = regexp.MustCompile(`(?m)^\s*\[Narration:\s*[a-z_]+\s*\]\s*`)

// clipMarkerRe strips [Clip: <id>] handles that survive in the prose so the
// preview leads with the narrative sentence, not the handle.
var clipMarkerRe = regexp.MustCompile(`(?m)^\s*\[Clip:\s*[^\]]+\s*\]\s*`)
