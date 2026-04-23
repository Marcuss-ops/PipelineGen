package scriptdocs

import (
	"sort"
	"strings"
)

type imageCandidate struct {
	entity string
	query  string
	score  float64
}

func (s *ScriptDocService) imageCandidatesForTopic(topic string, entityImages map[string]string) []imageCandidate {
	var candidates []imageCandidate
	if strings.TrimSpace(topic) != "" {
		candidates = append(candidates, imageCandidate{
			entity: topic,
			query:  anchorImageQuery(topic, topic),
			score:  scoreImageCandidate(topic, topic, ScriptChapter{}, 1.05),
		})
	}
	for _, entity := range extractImageEntities(topic, entityImages, nil) {
		candidates = append(candidates, imageCandidate{
			entity: entity,
			query:  anchorImageQuery(topic, entity),
			score:  scoreImageCandidate(entity, topic, ScriptChapter{}, 0.55),
		})
	}
	return sortImageCandidates(dedupeImageCandidates(candidates))
}

func (s *ScriptDocService) imageCandidatesForChapter(topic string, chapter ScriptChapter, entityImages map[string]string) []imageCandidate {
	text := strings.ToLower(chapter.SourceText)
	title := strings.ToLower(chapter.Title)
	seen := make(map[string]bool)
	var candidates []imageCandidate

	addTopic := strings.TrimSpace(topic)
	if addTopic != "" {
		key := normalizeKeyword(addTopic)
		if key != "" {
			seen[key] = true
			candidates = append(candidates, imageCandidate{
				entity: addTopic,
				query:  anchorImageQuery(topic, addTopic),
				score:  scoreImageCandidate(addTopic, topic, chapter, 1.05),
			})
		}
	}

	add := func(entity string, query string, base float64) {
		entity = strings.TrimSpace(entity)
		if entity == "" || isWeakImageEntity(entity) {
			return
		}
		key := normalizeKeyword(entity)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, imageCandidate{
			entity: entity,
			query:  anchorImageQuery(topic, query),
			score:  scoreImageCandidate(entity, topic, chapter, base),
		})
	}

	for _, entity := range chapter.DominantEntities {
		add(entity, entity, 1.0)
	}

	keys := make([]string, 0, len(entityImages))
	for entity := range entityImages {
		keys = append(keys, entity)
	}
	sort.Strings(keys)
	for _, entity := range keys {
		if normalizeKeyword(entity) == "" {
			continue
		}
		lower := strings.ToLower(entity)
		if strings.Contains(text, lower) {
			add(entity, entity, 0.9)
		}
		if strings.Contains(title, lower) {
			add(entity, entity, 0.85)
		}
	}

	for _, entity := range extractImageEntities(chapter.SourceText, entityImages, chapter.DominantEntities) {
		add(entity, entity, 0.75)
	}

	for _, entity := range extractImageEntities(topic, entityImages, chapter.DominantEntities) {
		add(entity, topic, 0.55)
	}

	return sortImageCandidates(dedupeImageCandidates(candidates))
}

func extractImageEntities(topic string, entityImages map[string]string, exclude []string) []string {
	seen := make(map[string]bool)
	for _, item := range exclude {
		seen[normalizeKeyword(item)] = true
	}

	allowAll := len(entityImages) == 0
	allowed := make(map[string]bool)
	for entity := range entityImages {
		allowed[normalizeKeyword(entity)] = true
	}

	var out []string
	add := func(entity string) {
		entity = strings.TrimSpace(entity)
		key := normalizeKeyword(entity)
		if key == "" || seen[key] {
			return
		}
		if isWeakImageEntity(entity) {
			return
		}
		if !allowAll && !allowed[key] {
			return
		}
		seen[key] = true
		out = append(out, entity)
	}

	for _, entity := range ExtractProperNouns([]string{topic}) {
		add(entity)
	}
	for _, entity := range ExtractMultiWordEntities([]string{topic}) {
		add(entity)
	}
	for _, entity := range ExtractKeywords(topic) {
		add(entity)
	}
	return out
}

func dedupeImageCandidates(items []imageCandidate) []imageCandidate {
	best := make(map[string]imageCandidate)
	for _, item := range items {
		key := normalizeKeyword(item.entity)
		if key == "" {
			continue
		}
		if current, ok := best[key]; !ok || item.score > current.score {
			best[key] = item
		}
	}
	out := make([]imageCandidate, 0, len(best))
	for _, item := range best {
		out = append(out, item)
	}
	return out
}

func sortImageCandidates(items []imageCandidate) []imageCandidate {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			if len(items[i].entity) == len(items[j].entity) {
				return items[i].entity < items[j].entity
			}
			return len(items[i].entity) > len(items[j].entity)
		}
		return items[i].score > items[j].score
	})
	return items
}

func scoreImageCandidate(entity, topic string, chapter ScriptChapter, base float64) float64 {
	ne := normalizeKeyword(entity)
	nt := normalizeKeyword(topic)
	score := base
	if isTitleLikeEntity(entity) {
		score += 0.12
	}
	if entity == strings.ToLower(entity) {
		score -= 0.04
	}
	if ne != "" && ne == nt {
		score += 0.55
	}
	if nt != "" && strings.Contains(strings.ToLower(entity), nt) {
		score += 0.25
	}
	if nt != "" {
		for _, tok := range significantTokens(topic) {
			if ne != "" && ne == tok && ne != nt {
				score -= 0.10
			}
		}
	}
	if strings.Contains(strings.ToLower(chapter.Title), strings.ToLower(entity)) {
		score += 0.15
	}
	if strings.Contains(strings.ToLower(chapter.SourceText), strings.ToLower(entity)) {
		score += 0.2
	}
	if nt != "" && strings.Contains(strings.ToLower(chapter.SourceText), nt) && strings.Contains(strings.ToLower(chapter.SourceText), strings.ToLower(entity)) {
		score += 0.12
	}
	if chapter.Confidence > 0 {
		score += chapter.Confidence / 10
	}
	if wc := len(strings.Fields(entity)); wc > 1 {
		score += 0.05 * float64(wc)
	}
	return score
}
