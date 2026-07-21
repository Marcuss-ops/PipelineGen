package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	similarity "github.com/Marcuss-ops/PipelineGen/pkg/similarity"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

type clipIntelligenceRecord struct {
	item         *asset.ClipManifestItem
	semanticText string
	tokenSet     map[string]struct{}
	topicSet     map[string]struct{}
	speakersSet  map[string]struct{}
	mentionedSet map[string]struct{}
	searchKeySet map[string]struct{}
}

type unionFind struct {
	parent []int
	rank   []int
}

func newUnionFind(n int) *unionFind {
	uf := &unionFind{
		parent: make([]int, n),
		rank:   make([]int, n),
	}
	for i := range uf.parent {
		uf.parent[i] = i
	}
	return uf
}

func (u *unionFind) find(x int) int {
	if u.parent[x] != x {
		u.parent[x] = u.find(u.parent[x])
	}
	return u.parent[x]
}

func (u *unionFind) union(a, b int) {
	ra := u.find(a)
	rb := u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		u.parent[ra] = rb
		return
	}
	if u.rank[ra] > u.rank[rb] {
		u.parent[rb] = ra
		return
	}
	u.parent[rb] = ra
	u.rank[ra]++
}

// ── Main enrichment entry point ──────────────────────────────────────────

func (s *Service) enrichManifestIntelligence(ctx context.Context, clipFolder *asset.ClipFolder, manifest *asset.ClipManifest) {
	if s == nil || s.clips == nil || clipFolder == nil || manifest == nil || len(manifest.Clips) == 0 {
		return
	}

	records := make([]*clipIntelligenceRecord, 0, len(manifest.Clips))
	for i := range manifest.Clips {
		item := &manifest.Clips[i]
		records = append(records, &clipIntelligenceRecord{
			item:         item,
			semanticText: buildManifestSemanticText(*item),
			tokenSet:     tagutil.TokenSetForText(buildManifestSemanticText(*item)),
			topicSet:     tagutil.TokenSetFromStrings(item.Topics, item.ClipTags, item.SearchKeywords),
			speakersSet:  tagutil.TokenSetFromStrings(item.Speakers),
			mentionedSet: tagutil.TokenSetFromStrings(item.MentionedPeople, item.People),
			searchKeySet: tagutil.TokenSetFromStrings(item.SearchKeywords, item.ClipTags, item.SourceTags),
		})
	}

	if len(records) == 0 {
		return
	}

	// ── Duplicate detection ─────────────────────────────────────────
	duplicateUF := newUnionFind(len(records))
	for i := 0; i < len(records); i++ {
		for j := i + 1; j < len(records); j++ {
			if shouldMarkDuplicate(records[i].item, records[j].item, records[i], records[j]) {
				duplicateUF.union(i, j)
			}
		}
	}

	duplicateGroups := make(map[int][]int)
	for i := range records {
		root := duplicateUF.find(i)
		duplicateGroups[root] = append(duplicateGroups[root], i)
	}

	duplicateGroupSeq := 0
	for _, members := range duplicateGroups {
		if len(members) <= 1 {
			continue
		}
		duplicateGroupSeq++
		groupID := fmt.Sprintf("dup_%s_%02d", textutil.SlugifyWithMax(manifest.VideoID, 24), duplicateGroupSeq)
		bestIdx := chooseBestManifestClip(records, members)
		for _, idx := range members {
			rec := records[idx]
			item := rec.item
			item.DuplicateGroupID = groupID
			item.DuplicateScore = duplicateSimilarityScore(records[bestIdx].item, item)
			item.IsDuplicate = idx != bestIdx
			item.IsBestVersion = idx == bestIdx
			if idx != bestIdx {
				item.DuplicateOf = records[bestIdx].item.ID
				if item.DuplicateReason == "" {
					if item.FileHash != "" && item.FileHash == records[bestIdx].item.FileHash {
						item.DuplicateReason = "same_file_hash"
					} else {
						item.DuplicateReason = "near_duplicate"
					}
				}
				if item.SearchVisibility == "" || item.SearchVisibility == "high" {
					item.SearchVisibility = "low"
				}
				if item.QualityScore > 0.7 {
					item.QualityScore *= 0.92
				}
			} else {
				item.DuplicateOf = ""
				if item.DuplicateReason == "" {
					item.DuplicateReason = "best_version"
				}
			}
		}
	}

	// ── Topic clustering ────────────────────────────────────────────
	topicUF := newUnionFind(len(records))
	for i := 0; i < len(records); i++ {
		for j := i + 1; j < len(records); j++ {
			if shouldClusterByTopic(records[i], records[j]) {
				topicUF.union(i, j)
			}
		}
	}

	topicGroups := make(map[int][]int)
	for i := range records {
		root := topicUF.find(i)
		topicGroups[root] = append(topicGroups[root], i)
	}

	topicClusterSeq := 0
	for _, members := range topicGroups {
		if len(members) == 0 {
			continue
		}
		topicClusterSeq++
		clusterID := fmt.Sprintf("topic_%s_%02d", textutil.SlugifyWithMax(manifest.VideoID, 24), topicClusterSeq)
		label := buildTopicClusterLabel(records, members)
		rankOrder := make([]int, len(members))
		copy(rankOrder, members)
		sort.SliceStable(rankOrder, func(i, j int) bool {
			return qualityForManifest(records[rankOrder[i]].item) > qualityForManifest(records[rankOrder[j]].item)
		})
		for rank, idx := range rankOrder {
			rec := records[idx]
			rec.item.TopicClusterID = clusterID
			rec.item.TopicClusterLabel = label
			rec.item.TopicClusterSize = len(members)
			rec.item.TopicClusterRank = rank + 1
		}
	}

	// ── Persist intelligence ────────────────────────────────────────
	for _, rec := range records {
		s.syncManifestClipIntelligence(ctx, clipFolder, rec.item)
	}
}

// ── Sync to clip store ───────────────────────────────────────────────────

func (s *Service) syncManifestClipIntelligence(ctx context.Context, clipFolder *asset.ClipFolder, item *asset.ClipManifestItem) {
	if s == nil || s.clips == nil || clipFolder == nil || item == nil || item.ID == "" {
		return
	}

	if item.SearchVisibility == "" {
		item.SearchVisibility = tagutil.DeriveSearchVisibility(item.QualityScore)
	}

	if clipFolder.LocalFolderPath != "" {
		metaPath := filepath.Join(clipFolder.LocalFolderPath, "metadata_"+item.ID+".json")
		if metaBytes, err := os.ReadFile(metaPath); err == nil {
			var meta youtubetypes.ClipMetadataFile
			if err := json.Unmarshal(metaBytes, &meta); err == nil {
				meta.DuplicateGroupID = item.DuplicateGroupID
				meta.DuplicateOf = item.DuplicateOf
				meta.IsDuplicate = item.IsDuplicate
				meta.IsBestVersion = item.IsBestVersion
				meta.DuplicateReason = item.DuplicateReason
				meta.DuplicateScore = item.DuplicateScore
				meta.TopicClusterID = item.TopicClusterID
				meta.TopicClusterLabel = item.TopicClusterLabel
				meta.TopicClusterSize = item.TopicClusterSize
				meta.TopicClusterRank = item.TopicClusterRank
				meta.QualityScore = item.QualityScore
				meta.SearchVisibility = item.SearchVisibility
				if data, err := json.MarshalIndent(meta, "", "  "); err == nil {
					_ = os.WriteFile(metaPath, data, 0644)
				}
			}
		}
	}

	clip, err := s.clips.Get(ctx, item.ID)
	if err != nil || clip == nil {
		return
	}

	if clip.Metadata == nil {
		clip.Metadata = make(map[string]any)
	}
	clip.SearchText = item.EmbeddingText
	clip.TranscriptTags = tagutil.MergeTagLists(item.Tags, item.SourceTags, item.ClipTags, item.Topics, item.Speakers, item.MentionedPeople, item.People)
	clip.RebuildTags()
	clip.SetMetadataString("clean_title", item.CleanTitle)
	clip.SetMetadataString("short_title", item.ShortTitle)
	clip.SetMetadataString("clip_summary", item.ClipSummary)
	clip.SetMetadataString("hook", item.Hook)
	clip.Metadata["topics"] = append([]string(nil), item.Topics...)
	clip.Metadata["speakers"] = append([]string(nil), item.Speakers...)
	clip.Metadata["mentioned_people"] = append([]string(nil), item.MentionedPeople...)
	clip.Metadata["people"] = append([]string(nil), item.People...)
	clip.Metadata["source_tags"] = append([]string(nil), item.SourceTags...)
	clip.Metadata["clip_tags"] = append([]string(nil), item.ClipTags...)
	clip.Metadata["search_keywords"] = append([]string(nil), item.SearchKeywords...)
	clip.Metadata["clean_transcript"] = item.CleanTranscript
	clip.Metadata["embedding_text"] = item.EmbeddingText
	clip.Metadata["quality_score"] = item.QualityScore
	clip.Metadata["search_visibility"] = item.SearchVisibility
	clip.Metadata["duplicate_group_id"] = item.DuplicateGroupID
	clip.Metadata["duplicate_of"] = item.DuplicateOf
	clip.Metadata["is_duplicate"] = item.IsDuplicate
	clip.Metadata["is_best_version"] = item.IsBestVersion
	clip.Metadata["duplicate_reason"] = item.DuplicateReason
	clip.Metadata["duplicate_score"] = item.DuplicateScore
	clip.Metadata["topic_cluster_id"] = item.TopicClusterID
	clip.Metadata["topic_cluster_label"] = item.TopicClusterLabel
	clip.Metadata["topic_cluster_size"] = item.TopicClusterSize
	clip.Metadata["topic_cluster_rank"] = item.TopicClusterRank

	if err := s.clips.Upsert(ctx, clip); err != nil {
		s.log.Debug("failed to persist clip intelligence", zap.String("clip_id", item.ID), zap.Error(err))
		return
	}
}

// ── Semantic text ────────────────────────────────────────────────────────

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

// ── Duplicate detection ──────────────────────────────────────────────────

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

// ── Topic clustering ─────────────────────────────────────────────────────

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

// ── Best version selection ───────────────────────────────────────────────

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
