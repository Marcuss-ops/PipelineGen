package youtube

import (
	"context"
	"fmt"
	"sort"

	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/tagutil"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
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
