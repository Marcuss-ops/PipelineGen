package youtube

import (
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/tagutil"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	similarity "github.com/Marcuss-ops/PipelineGen/pkg/similarity"
)

// ── Semantic text builder ──────────────────────────────────────────────

func buildManifestSemanticText(item asset.ClipManifestItem) string {
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

// ── Duplicate detection ────────────────────────────────────────────────

func shouldMarkDuplicate(a, b *asset.ClipManifestItem, ra, rb *clipIntelligenceRecord) bool {
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

func duplicateSimilarityScore(a, b *asset.ClipManifestItem) float64 {
	if a == nil || b == nil {
		return 0
	}
	if a.FileHash != "" && a.FileHash == b.FileHash {
		return 1
	}
	semanticA := buildManifestSemanticText(*a)
	semanticB := buildManifestSemanticText(*b)
	tokenScore := tagutil.TextJaccardScore(semanticA, semanticB)
	topicScore := tagutil.SliceJaccardScore(a.Topics, b.Topics)
	speakerScore := tagutil.SliceJaccardScore(a.Speakers, b.Speakers)
	peopleScore := tagutil.SliceJaccardScore(tagutil.MergeStringSlices(a.MentionedPeople, a.People), tagutil.MergeStringSlices(b.MentionedPeople, b.People))
	score := tokenScore*0.55 + topicScore*0.25 + speakerScore*0.1 + peopleScore*0.1
	if a.CleanTitle != "" && tagutil.NormalizeSemanticText(a.CleanTitle) == tagutil.NormalizeSemanticText(b.CleanTitle) {
		score += 0.08
	}
	if a.ClipSummary != "" && b.ClipSummary != "" && tagutil.NormalizeSemanticText(a.ClipSummary) == tagutil.NormalizeSemanticText(b.ClipSummary) {
		score += 0.08
	}
	if score > 1 {
		score = 1
	}
	return score
}
