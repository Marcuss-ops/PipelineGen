package youtube

import (
	"sort"
	"strings"

	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/tagutil"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Topic clustering ──────────────────────────────────────────────────

func shouldClusterByTopic(a, b *clipIntelligenceRecord) bool {
	if a == nil || b == nil || a.item == nil || b.item == nil || a.item.ID == b.item.ID {
		return false
	}
	topicScore := tagutil.SliceJaccardScore(a.item.Topics, b.item.Topics)
	speakerScore := tagutil.SliceJaccardScore(a.item.Speakers, b.item.Speakers)
	peopleScore := tagutil.SliceJaccardScore(tagutil.MergeStringSlices(a.item.MentionedPeople, a.item.People), tagutil.MergeStringSlices(b.item.MentionedPeople, b.item.People))
	semanticScore := tagutil.TextJaccardScore(a.semanticText, b.semanticText)
	if topicScore >= 0.35 {
		return true
	}
	if topicScore >= 0.2 && (speakerScore >= 0.2 || peopleScore >= 0.2) {
		return true
	}
	if semanticScore >= 0.65 && topicScore >= 0.15 {
		return true
	}
	return false
}

func buildTopicClusterLabel(records []*clipIntelligenceRecord, members []int) string {
	freq := make(map[string]int)
	for _, idx := range members {
		for _, topic := range records[idx].item.Topics {
			norm := tagutil.NormalizeSemanticText(topic)
			if norm == "" {
				continue
			}
			freq[norm]++
		}
	}
	type kv struct {
		key   string
		count int
	}
	ranked := make([]kv, 0, len(freq))
	for key, count := range freq {
		ranked = append(ranked, kv{key: key, count: count})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].count == ranked[j].count {
			return len(ranked[i].key) > len(ranked[j].key)
		}
		return ranked[i].count > ranked[j].count
	})
	if len(ranked) > 0 {
		return strings.Title(strings.ReplaceAll(ranked[0].key, "-", " "))
	}
	if len(members) > 0 {
		label := records[members[0]].item.CleanTitle
		if label == "" {
			label = records[members[0]].item.Name
		}
		label = strings.TrimSpace(label)
		if len(label) > 48 {
			label = label[:48]
		}
		return label
	}
	return "topic cluster"
}

// ── Best version selection ────────────────────────────────────────────

func chooseBestManifestClip(records []*clipIntelligenceRecord, members []int) int {
	best := members[0]
	for _, idx := range members[1:] {
		if compareManifestClips(records[idx].item, records[best].item) > 0 {
			best = idx
		}
	}
	return best
}

func compareManifestClips(a, b *asset.ClipManifestItem) int {
	if a == nil || b == nil {
		return 0
	}
	scoreA := qualityForManifest(a)
	scoreB := qualityForManifest(b)
	switch {
	case scoreA > scoreB:
		return 1
	case scoreA < scoreB:
		return -1
	}
	if a.DurationSeconds > b.DurationSeconds {
		return 1
	}
	if a.DurationSeconds < b.DurationSeconds {
		return -1
	}
	if a.StartSeconds < b.StartSeconds {
		return 1
	}
	if a.StartSeconds > b.StartSeconds {
		return -1
	}
	return strings.Compare(a.ID, b.ID)
}

func qualityForManifest(item *asset.ClipManifestItem) float64 {
	if item == nil {
		return 0
	}
	score := item.QualityScore
	if item.IsDuplicate && !item.IsBestVersion {
		score -= 0.18
	}
	switch strings.ToLower(strings.TrimSpace(item.SearchVisibility)) {
	case "high":
		score += 0.07
	case "normal":
		score += 0.02
	case "low":
		score -= 0.03
	case "poor":
		score -= 0.08
	}
	if item.ClipSummary != "" {
		score += 0.02
	}
	if item.Hook != "" {
		score += 0.02
	}
	if len(item.Topics) >= 3 {
		score += 0.03
	}
	if len(item.Speakers) > 0 {
		score += 0.01
	}
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
