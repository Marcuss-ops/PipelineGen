package script

import (
	"strings"
)

func filterFoldersByTerms(folders []timelineFolderCandidate, terms []string, limit int) []timelineFolderCandidate {
	if len(terms) == 0 {
		return nil
	}

	type scoredFolder struct {
		folder timelineFolderCandidate
		score  int
	}

	var scored []scoredFolder
	for _, f := range folders {
		score := 0
		text := strings.ToLower(f.Name + " " + f.Path)

		lowerName := strings.ToLower(f.Name)
		if lowerName == "stock cartella" || lowerName == "stock" || lowerName == "artlist" {
			score -= 100
		}

		for _, term := range terms {
			term = strings.ToLower(term)
			if term == "" || len(term) < 3 {
				continue
			}
			if strings.Contains(text, term) {
				score += 10
				if strings.Contains(lowerName, term) {
					score += 50
				}
			}
		}

		if score > 0 {
			scored = append(scored, scoredFolder{f, score})
		}
	}

	if len(scored) == 0 {
		return nil
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return len(scored[i].folder.Path) > len(scored[j].folder.Path)
	})

	var result []timelineFolderCandidate
	for i := 0; i < len(scored) && i < limit; i++ {
		result = append(result, scored[i].folder)
	}
	return result
}

func strongTimelineFolderDecision(stockFolders, artlistFolders []timelineFolderCandidate, terms []string) (timelineAssetDecision, bool) {
	if len(terms) == 0 {
		return timelineAssetDecision{}, false
	}
	for _, candidate := range stockFolders {
		if isStrongTimelineFolderCandidate(candidate, terms) {
			return timelineAssetDecision{
				Source: string(timelineAssetSourceStockDrive),
				Folder: candidate.Name,
				Reason: "strong stock folder match",
			}, true
		}
	}
	for _, candidate := range artlistFolders {
		if isStrongTimelineFolderCandidate(candidate, terms) {
			return timelineAssetDecision{
				Source: string(timelineAssetSourceArtlistFolder),
				Folder: candidate.Name,
				Reason: "strong artlist folder match",
			}, true
		}
	}
	return timelineAssetDecision{}, false
}

func isStrongTimelineFolderCandidate(candidate timelineFolderCandidate, terms []string) bool {
	name := normalizeFolderChoice(candidate.Name)
	path := normalizeFolderChoice(candidate.Path)
	if name == "" && path == "" {
		return false
	}
	if name == "artlist" || name == "stock" || name == "stock cartella" {
		return false
	}

	candidateTokens := make(map[string]struct{})
	for _, token := range tokenize(name + " " + path) {
		if len(token) >= 3 && !isStopWord(token) {
			candidateTokens[token] = struct{}{}
		}
	}

	if len(candidateTokens) == 0 {
		return false
	}

	for _, term := range terms {
		normalizedTerm := normalizeFolderChoice(term)
		if normalizedTerm == "" {
			continue
		}

		if normalizedTerm == name || normalizedTerm == path || strings.Contains(name, normalizedTerm) || strings.Contains(path, normalizedTerm) {
			return true
		}

		for _, token := range tokenize(normalizedTerm) {
			if _, ok := candidateTokens[token]; ok {
				return true
			}
		}
	}

	return false
}

func normalizeFolderChoice(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.ReplaceAll(text, "_", " ")
	text = strings.ReplaceAll(text, "-", " ")
	text = strings.Join(strings.Fields(text), " ")
	return text
}

func fallbackTimelineAssetDecision(stockFolders, artlistFolders []timelineFolderCandidate, terms []string) timelineAssetDecision {
	if decision, ok := strongTimelineFolderDecision(stockFolders, artlistFolders, terms); ok {
		return decision
	}
	return timelineAssetDecision{Source: "none", Reason: "no strong asset match"}
}

func resolveTimelineFolderCandidate(candidates []timelineFolderCandidate, choice string) (timelineFolderCandidate, bool) {
	choice = normalizeFolderChoice(choice)
	if choice == "" {
		return timelineFolderCandidate{}, false
	}
	for _, candidate := range candidates {
		if normalizeFolderChoice(candidate.Name) == choice || normalizeFolderChoice(candidate.Path) == choice {
			return candidate, true
		}
	}
	for _, candidate := range candidates {
		if strings.Contains(normalizeFolderChoice(candidate.Name), choice) || strings.Contains(choice, normalizeFolderChoice(candidate.Name)) {
			return candidate, true
		}
		if strings.Contains(normalizeFolderChoice(candidate.Path), choice) || strings.Contains(choice, normalizeFolderChoice(candidate.Path)) {
			return candidate, true
		}
	}
	return timelineFolderCandidate{}, false
}

func resolveStockFolderCandidate(folders []timelineFolderCandidate, choice string) (timelineFolderCandidate, bool) {
	return resolveTimelineFolderCandidate(folders, choice)
}

func resolveArtlistFolderCandidate(folders []timelineFolderCandidate, choice string) (timelineFolderCandidate, bool) {
	return resolveTimelineFolderCandidate(folders, choice)
}

func formatTimelineFolderCandidates(candidates []timelineFolderCandidate) string {
	if len(candidates) == 0 {
		return "None"
	}
	var b strings.Builder
	for i, candidate := range candidates {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("%d. name=%s | path=%s | link=%s", i+1, candidate.Name, candidate.Path, candidate.Link))
	}
	return b.String()
}

func prioritizeFoldersByTopic(folders []timelineFolderCandidate, topic string, maxCount int) []timelineFolderCandidate {
	if len(folders) == 0 {
		return folders
	}

	topicTerms := tokenize(topic)

	matching := make([]timelineFolderCandidate, 0)
	nonMatching := make([]timelineFolderCandidate, 0)

	for _, folder := range folders {
		folderText := " " + strings.ToLower(folder.Name+" "+folder.Path) + " "
		isMatch := false
		for _, term := range topicTerms {
			if len(term) < 3 {
				continue
			}
			if strings.Contains(folderText, " "+term+" ") || strings.Contains(folderText, "/"+term) || strings.Contains(folderText, "_"+term) {
				isMatch = true
				break
			}
		}
		if isMatch {
			matching = append(matching, folder)
		} else {
			nonMatching = append(nonMatching, folder)
		}
	}

	if len(matching) > 0 {
		if len(matching) > maxCount {
			return matching[:maxCount]
		}
		return matching
	}

	return []timelineFolderCandidate{}
}
