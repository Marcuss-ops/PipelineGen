package script

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"velox/go-master/internal/ml/ollama"
)

type clipDriveIndex struct {
	Clips []clipDriveRecord `json:"clips"`
}

type clipDriveRecord struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Filename     string   `json:"filename"`
	FolderID     string   `json:"folder_id"`
	FolderPath   string   `json:"folder_path"`
	Group        string   `json:"group"`
	MediaType    string   `json:"media_type"`
	DriveLink    string   `json:"drive_link"`
	DownloadLink string   `json:"download_link"`
	Tags         []string `json:"tags"`
}

type clipDriveCandidate struct {
	Record   clipDriveRecord
	Score    float64
	Reason   string
	Text     string
	SideText string
}

type clipDriveLLMSelection struct {
	ClipID string  `json:"clip_id"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

type clipDrivePhraseMatch struct {
	Sentence string
	ClipID   string
	Title    string
	Link     string
	Score    float64
	Reason   string
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
	for _, phrase := range phrases {
		candidates := rankClipDriveCandidates(phrase, clips, sidecarTexts, 6)
		if len(candidates) == 0 {
			continue
		}

		selected := clipDriveCandidate{
			Record: candidates[0].Record,
			Score:  candidates[0].Score,
			Reason: candidates[0].Reason,
			Text:   candidates[0].Text,
		}

		if client != nil {
			if llmSel, ok := refineClipDriveMatchWithLLM(ctx, client, req.Topic, phrase, candidates); ok {
				for _, cand := range candidates {
					if cand.Record.ID == llmSel.ClipID {
						selected = clipDriveCandidate{
							Record: cand.Record,
							Score:  llmSel.Score,
							Reason: llmSel.Reason,
							Text:   cand.Text,
						}
						break
					}
				}
			}
		}

		if selected.Score < 70 {
			continue
		}

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
		Title: "Clip Drive Matching",
		Body:  renderClipDriveMatches(matches),
	}
}

func loadClipDriveCatalog(dataDir string) ([]clipDriveRecord, error) {
	path := filepath.Join(dataDir, "clip_index.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var index clipDriveIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return index.Clips, nil
}

func loadClipSidecarTexts(textDir string) map[string]string {
	textDir = strings.TrimSpace(textDir)
	if textDir == "" {
		return nil
	}

	texts := make(map[string]string)
	_ = filepath.WalkDir(textDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".txt") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			return nil
		}
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		texts[normalizeClipKey(base)] = content
		texts[normalizeClipKey(strings.TrimSpace(path))] = content
		return nil
	})
	return texts
}

func collectClipDrivePhrases(narrative string, analysis *ollama.FullEntityAnalysis) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)

	add := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		key := normalizeClipKey(text)
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, text)
	}

	if analysis != nil {
		for _, seg := range analysis.SegmentEntities {
			for _, phrase := range seg.FrasiImportanti {
				add(phrase)
			}
		}
	}

	if len(out) == 0 {
		for _, sentence := range extractNarrativeSentences(narrative) {
			add(sentence)
		}
	}

	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func extractNarrativeSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	splitter := func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	}

	parts := strings.FieldsFunc(text, splitter)
	sentences := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len([]rune(part)) < 20 {
			continue
		}
		sentences = append(sentences, part)
	}
	return sentences
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

func clipDriveSidecarText(clip clipDriveRecord, sidecarTexts map[string]string) string {
	if len(sidecarTexts) == 0 {
		return ""
	}

	keys := []string{
		normalizeClipKey(clip.ID),
		normalizeClipKey(strings.TrimSuffix(clip.Filename, filepath.Ext(clip.Filename))),
		normalizeClipKey(clip.Name),
		normalizeClipKey(filepath.Join(clip.FolderPath, clip.Name)),
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		if text, ok := sidecarTexts[key]; ok {
			return text
		}
	}
	return ""
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

func normalizeClipKey(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func clipDriveTokens(text string) []string {
	text = normalizeClipKey(text)
	if text == "" {
		return nil
	}
	parts := strings.Fields(text)
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if len(part) < 3 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func renderClipDriveMatches(matches []clipDrivePhraseMatch) string {
	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("- ")
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
