package script

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"velox/go-master/internal/ml/ollama"
)

func clipAlreadyUsed(used map[string]struct{}, clipID string) bool {
	if clipID == "" {
		return false
	}
	_, ok := used[clipID]
	return ok
}

func pickUnusedClipCandidate(candidates []clipDriveCandidate, used map[string]struct{}) (clipDriveCandidate, bool) {
	for _, cand := range candidates {
		if cand.Score < 70 {
			continue
		}
		if clipAlreadyUsed(used, cand.Record.ID) {
			continue
		}
		return cand, true
	}
	return clipDriveCandidate{}, false
}

func findClipCandidateByID(candidates []clipDriveCandidate, clipID string) (clipDriveCandidate, bool) {
	for _, cand := range candidates {
		if cand.Record.ID == clipID {
			return cand, true
		}
	}
	return clipDriveCandidate{}, false
}

func rankClipDriveCandidates(phrase string, clips []clipDriveRecord, sidecarTexts map[string]string, limit int) []clipDriveCandidate {
	phraseNorm := normalizeClipKey(phrase)
	if phraseNorm == "" {
		return nil
	}

	phraseTokens := clipDriveTokens(phraseNorm)
	candidates := make([]clipDriveCandidate, 0, len(clips))
	for _, clip := range clips {
		if strings.TrimSpace(clip.MediaType) != "" && !strings.EqualFold(strings.TrimSpace(clip.MediaType), "clip") {
			continue
		}

		sideText := clipDriveSidecarText(clip, sidecarTexts)
		candidateText := strings.Join([]string{
			clip.ID,
			clip.Name,
			clip.Filename,
			clip.FolderPath,
			clip.Group,
			strings.Join(clip.Tags, " "),
			sideText,
		}, " ")
		candidateNorm := normalizeClipKey(candidateText)
		if candidateNorm == "" {
			continue
		}

		score, reason := scoreClipDriveCandidate(phraseTokens, phraseNorm, candidateNorm, clip, sideText)
		if score <= 0 {
			continue
		}

		candidates = append(candidates, clipDriveCandidate{
			Record:   clip,
			Score:    score,
			Reason:   reason,
			Text:     candidateText,
			SideText: sideText,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return clipDriveTitle(candidates[i].Record) < clipDriveTitle(candidates[j].Record)
		}
		return candidates[i].Score > candidates[j].Score
	})

	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func scoreClipDriveCandidate(phraseTokens []string, phraseNorm string, candidateNorm string, clip clipDriveRecord, sideText string) (float64, string) {
	if len(phraseTokens) == 0 || candidateNorm == "" {
		return 0, ""
	}

	candidateTokens := clipDriveTokens(candidateNorm)
	if len(candidateTokens) == 0 {
		return 0, ""
	}

	candidateSet := make(map[string]struct{}, len(candidateTokens))
	for _, tok := range candidateTokens {
		candidateSet[tok] = struct{}{}
	}

	hits := 0
	for _, tok := range phraseTokens {
		if _, ok := candidateSet[tok]; ok {
			hits++
		}
	}

	base := float64(hits) / float64(len(phraseTokens)) * 100
	if base <= 0 {
		return 0, ""
	}

	name := normalizeClipKey(clip.Name)
	file := normalizeClipKey(strings.TrimSuffix(clip.Filename, filepath.Ext(clip.Filename)))
	folder := normalizeClipKey(clip.FolderPath)
	topic := normalizeClipKey(clip.Group)

	boost := 0.0
	switch {
	case name != "" && strings.Contains(phraseNorm, name):
		boost += 20
	case file != "" && strings.Contains(phraseNorm, file):
		boost += 18
	case folder != "" && strings.Contains(candidateNorm, folder):
		boost += 10
	case topic != "" && strings.Contains(candidateNorm, topic):
		boost += 5
	}

	if strings.TrimSpace(sideText) != "" {
		boost += 5
	}

	score := base + boost
	if score > 100 {
		score = 100
	}

	reason := fmt.Sprintf("token_overlap=%.0f", base)
	if boost > 0 {
		reason += fmt.Sprintf(" boost=%.0f", boost)
	}
	return score, reason
}

func refineClipDriveMatchWithLLM(ctx context.Context, client *ollama.Client, topic, phrase string, candidates []clipDriveCandidate) (*clipDriveLLMSelection, bool) {
	if client == nil || len(candidates) == 0 {
		return nil, false
	}

	var b strings.Builder
	b.WriteString("Sei un matcher conservativo tra frase e clip già esistenti.\n")
	b.WriteString("Usa SOLO le clip candidate fornite. Non inventare ID, non aggiungere clip esterne, non forzare il match.\n")
	b.WriteString("Se nessuna clip è davvero adatta, restituisci clip_id vuoto e score 0.\n")
	b.WriteString("Rispondi solo con JSON puro nel formato {\"clip_id\":\"...\",\"score\":0-100,\"reason\":\"...\"}.\n\n")
	b.WriteString("TOPIC: ")
	b.WriteString(topic)
	b.WriteString("\nFRASE: ")
	b.WriteString(phrase)
	b.WriteString("\n\nCANDIDATE:\n")
	for i, cand := range candidates {
		b.WriteString(fmt.Sprintf("%d) id=%s | name=%s | folder=%s | tags=%s | score=%.1f\n", i+1, cand.Record.ID, cand.Record.Name, cand.Record.FolderPath, strings.Join(cand.Record.Tags, ", "), cand.Score))
		if strings.TrimSpace(cand.SideText) != "" {
			side := cand.SideText
			if len([]rune(side)) > 180 {
				side = string([]rune(side)[:180]) + "..."
			}
			b.WriteString("   text=")
			b.WriteString(side)
			b.WriteString("\n")
		}
	}

	raw, err := client.GenerateWithOptions(ctx, "gemma3:4b", b.String(), map[string]interface{}{
		"temperature": 0.1,
		"num_predict": 256,
	})
	if err != nil {
		return nil, false
	}

	selection, err := parseClipDriveLLMSelection(raw)
	if err != nil {
		return nil, false
	}

	if selection.ClipID == "" || selection.Score < 70 {
		return nil, false
	}
	for _, cand := range candidates {
		if cand.Record.ID == selection.ClipID {
			return selection, true
		}
	}
	return nil, false
}

func parseClipDriveLLMSelection(raw string) (*clipDriveLLMSelection, error) {
	cleaned := stripCodeFence(raw)
	jsonPayload := extractJSONObject(cleaned)
	if jsonPayload == "" {
		return nil, fmt.Errorf("clip drive selection response did not contain JSON")
	}

	var selection clipDriveLLMSelection
	if err := json.Unmarshal([]byte(jsonPayload), &selection); err != nil {
		return nil, err
	}
	return &selection, nil
}

func clipDriveTitle(clip clipDriveRecord) string {
	if strings.TrimSpace(clip.Name) != "" {
		return clip.Name
	}
	if strings.TrimSpace(clip.Filename) != "" {
		return clip.Filename
	}
	return clip.ID
}

func clipDriveLink(clip clipDriveRecord) string {
	if strings.TrimSpace(clip.DriveLink) != "" {
		return clip.DriveLink
	}
	return clip.DownloadLink
}

func renderClipDriveMatches(matches []clipDrivePhraseMatch) string {
	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("✨ ")
		b.WriteString(match.Sentence)
		b.WriteString("\n")
		b.WriteString("  clip_id: ")
		b.WriteString(match.ClipID)
		b.WriteString("\n")
		b.WriteString("  title: ")
		b.WriteString(match.Title)
		b.WriteString("\n")
		if strings.TrimSpace(match.Link) != "" {
			b.WriteString("  link: ")
			b.WriteString(match.Link)
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("  score: %.1f\n", match.Score))
		if strings.TrimSpace(match.Reason) != "" {
			b.WriteString("  reason: ")
			b.WriteString(match.Reason)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func buildClipDriveMatchingSection(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, narrative string, analysis *ollama.FullEntityAnalysis, dataDir, clipTextDir string) ScriptSection {
	clips, err := loadClipDriveCatalog(dataDir)
	if err != nil || len(clips) == 0 {
		return ScriptSection{
			Title: "Clip Drive Matching",
			Body:  "Clip drive catalog unavailable.",
		}
	}

	phrases := collectClipDrivePhrases(narrative, analysis)
	if len(phrases) == 0 {
		return ScriptSection{
			Title: "Clip Drive Matching",
			Body:  "None",
		}
	}

	sidecarTexts := loadClipSidecarTexts(clipTextDir)
	client := (*ollama.Client)(nil)
	if gen != nil {
		client = gen.GetClient()
	}

	matches := make([]clipDrivePhraseMatch, 0, len(phrases))
	usedClipIDs := make(map[string]struct{})
	for _, phrase := range phrases {
		candidates := rankClipDriveCandidates(phrase, clips, sidecarTexts, 6)
		if len(candidates) == 0 {
			continue
		}

		selected, ok := pickUnusedClipCandidate(candidates, usedClipIDs)
		if !ok {
			continue
		}

		if client != nil {
			if llmSel, ok := refineClipDriveMatchWithLLM(ctx, client, req.Topic, phrase, candidates); ok {
				if cand, found := findClipCandidateByID(candidates, llmSel.ClipID); found && !clipAlreadyUsed(usedClipIDs, cand.Record.ID) {
					selected = clipDriveCandidate{
						Record: cand.Record,
						Score:  llmSel.Score,
						Reason: llmSel.Reason,
						Text:   cand.Text,
					}
				}
			}
		}

		if selected.Score < 70 {
			continue
		}
		if clipAlreadyUsed(usedClipIDs, selected.Record.ID) {
			continue
		}
		usedClipIDs[selected.Record.ID] = struct{}{}

		matches = append(matches, clipDrivePhraseMatch{
			Sentence: phrase,
			ClipID:   selected.Record.ID,
			Title:    clipDriveTitle(selected.Record),
			Link:     clipDriveLink(selected.Record),
			Score:    selected.Score,
			Reason:   selected.Reason,
		})
	}

	if len(matches) == 0 {
		return ScriptSection{
			Title: "Clip Drive Matching",
			Body:  "None",
		}
	}

	return ScriptSection{
		Title: "🎞️ Clip Drive Matching",
		Body:  renderClipDriveMatches(matches),
	}
}