package scriptdocs

import (
	"sort"
	"strings"
)

func chapterBoundaries(text string) (startPhrase, endPhrase string) {
	sentences := ExtractSentences(text)
	if len(sentences) == 0 {
		cleaned := compactSnippet(text, 72)
		return cleaned, cleaned
	}
	startPhrase = compactSnippet(sentences[0], 72)
	endPhrase = compactSnippet(sentences[len(sentences)-1], 72)
	if endPhrase == "" {
		endPhrase = startPhrase
	}
	return startPhrase, endPhrase
}

func compactSnippet(text string, maxLen int) string {
	cleaned := strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if cleaned == "" {
		return ""
	}
	if len(cleaned) <= maxLen {
		return cleaned
	}
	cut := maxLen
	if cut > len(cleaned) {
		cut = len(cleaned)
	}
	snippet := cleaned[:cut]
	if idx := strings.LastIndexAny(snippet, " ,;:-"); idx > 40 {
		snippet = snippet[:idx]
	}
	return strings.TrimSpace(snippet) + "..."
}

func langNames(results []LanguageResult) string {
	var names []string
	for _, r := range results {
		if info, ok := LanguageInfo[r.Language]; ok {
			names = append(names, info.Name)
		} else {
			names = append(names, r.Language)
		}
	}
	return strings.Join(names, ", ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

type imageWindowGroup struct {
	StartTime int
	EndTime   int
	Phrase    string
	Images    []ImageAssociation
}

func groupImageAssociationsByWindow(images []ImageAssociation) []imageWindowGroup {
	if len(images) == 0 {
		return nil
	}
	ordered := append([]ImageAssociation(nil), images...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartTime == ordered[j].StartTime {
			if ordered[i].EndTime == ordered[j].EndTime {
				return ordered[i].Score > ordered[j].Score
			}
			return ordered[i].EndTime < ordered[j].EndTime
		}
		return ordered[i].StartTime < ordered[j].StartTime
	})

	groups := make([]imageWindowGroup, 0, len(ordered))
	for _, img := range ordered {
		if len(groups) == 0 {
			groups = append(groups, imageWindowGroup{
				StartTime: img.StartTime,
				EndTime:   img.EndTime,
				Phrase:    img.Phrase,
				Images:    []ImageAssociation{img},
			})
			continue
		}
		last := &groups[len(groups)-1]
		if last.StartTime == img.StartTime && last.EndTime == img.EndTime {
			last.Images = append(last.Images, img)
			if last.Phrase == "" {
				last.Phrase = img.Phrase
			}
			continue
		}
		groups = append(groups, imageWindowGroup{
			StartTime: img.StartTime,
			EndTime:   img.EndTime,
			Phrase:    img.Phrase,
			Images:    []ImageAssociation{img},
		})
	}
	return groups
}

func buildClipDescriptionFromTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}

	seen := make(map[string]bool)
	parts := make([]string, 0, 4)
	for _, raw := range tags {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, tag)
		if len(parts) == 4 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func associationLabel(assoc ClipAssociation) string {
	if phrase := strings.TrimSpace(assoc.Phrase); phrase != "" {
		return phrase
	}
	if kw := strings.TrimSpace(assoc.MatchedKeyword); kw != "" {
		return kw
	}
	return assoc.Phrase
}
