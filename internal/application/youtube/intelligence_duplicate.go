package youtube

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	similarity "github.com/Marcuss-ops/PipelineGen/pkg/similarity"
)

// ── Semantic text builder ──────────────────────────────────────────────

func buildManifestSemanticText(item media.ClipManifestItem) string {
	parts := []string{
		item.CleanTitle,
		item.ClipSummary,
		item.Hook,
		strings.Join(item.Topics, " "),
		strings.Join(item.Speakers, " "),
		strings.Join(item.MentionedPeople, " "),
		strings.Join(item.People, " "),
		strings.Join(item.ClipTags, " "),
		strings.Join(item.SearchKeywords, " "),
		item.CleanTranscript,
		item.EmbeddingText,
	}
	out := strings.Builder{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(part)
	}
	return out.String()
}

// ── Token set operations ───────────────────────────────────────────────

func tokenSetForText(text string) map[string]struct{} {
	text = strings.ToLower(text)
	text = cleanYouTubeDescription(text)
	text = cleanClipTranscript(text)
	replacer := strings.NewReplacer(
		",", " ", ".", " ", "!", " ", "?", " ", ";", " ", ":", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "-", " ", "_", " ",
		"\"", " ", "'", " ", "/", " ", "\\", " ",
		"&", " ", "|", " ", "#", " ",
	)
	text = replacer.Replace(text)
	set := make(map[string]struct{})
	for _, word := range strings.Fields(text) {
		word = strings.TrimSpace(word)
		if len(word) < 3 {
			continue
		}
		if isGenericToken(word) {
			continue
		}
		set[word] = struct{}{}
	}
	return set
}

func tokenSetFromStrings(values ...[]string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, list := range values {
		for _, item := range list {
			for tok := range tokenSetForText(item) {
				set[tok] = struct{}{}
			}
		}
	}
	return set
}

// ── Duplicate detection ────────────────────────────────────────────────

func shouldMarkDuplicate(a, b *media.ClipManifestItem, ra, rb *clipIntelligenceRecord) bool {
	if a == nil || b == nil || a.ID == "" || b.ID == "" || a.ID == b.ID {
		return false
	}
	if a.FileHash != "" && a.FileHash == b.FileHash {
		return true
	}
	if a.Status != "processed" || b.Status != "processed" {
		return false
	}
	if similarity.OverlapRatio(a.StartSeconds, a.EndSeconds, b.StartSeconds, b.EndSeconds) >= 0.65 {
		return duplicateSimilarityScore(a, b) >= 0.72
	}
	if duplicateSimilarityScore(a, b) >= 0.83 {
		return true
	}
	if similarity.Jaccard(ra.tokenSet, rb.tokenSet) >= 0.88 {
		return true
	}
	return false
}

func duplicateSimilarityScore(a, b *media.ClipManifestItem) float64 {
	if a == nil || b == nil {
		return 0
	}
	if a.FileHash != "" && a.FileHash == b.FileHash {
		return 1
	}
	semanticA := buildManifestSemanticText(*a)
	semanticB := buildManifestSemanticText(*b)
	tokenScore := textJaccardScore(semanticA, semanticB)
	topicScore := sliceJaccardScore(a.Topics, b.Topics)
	speakerScore := sliceJaccardScore(a.Speakers, b.Speakers)
	peopleScore := sliceJaccardScore(mergeStringSlices(a.MentionedPeople, a.People), mergeStringSlices(b.MentionedPeople, b.People))
	score := tokenScore*0.55 + topicScore*0.25 + speakerScore*0.1 + peopleScore*0.1
	if a.CleanTitle != "" && normalizeSemanticText(a.CleanTitle) == normalizeSemanticText(b.CleanTitle) {
		score += 0.08
	}
	if a.ClipSummary != "" && b.ClipSummary != "" && normalizeSemanticText(a.ClipSummary) == normalizeSemanticText(b.ClipSummary) {
		score += 0.08
	}
	if score > 1 {
		score = 1
	}
	return score
}
