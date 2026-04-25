package script

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"velox/go-master/internal/ml/ollama/types"
)

// loadClipSidecarTexts loads sidecar text files for clips
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

// clipDriveSidecarText retrieves sidecar text for a clip
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

// normalizeClipKey normalizes text for clip matching
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

// clipDriveTokens tokenizes text for clip matching
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

// collectClipDrivePhrases collects phrases for clip matching
func collectClipDrivePhrases(narrative string, analysis *types.FullEntityAnalysis) []string {
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

// extractNarrativeSentences extracts sentences from narrative text
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