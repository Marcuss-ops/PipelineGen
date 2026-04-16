package clip

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// SimilarClipsRequest represents a request for similar clips
type SimilarClipsRequest struct {
	ClipID     string `json:"clip_id"`
	MaxResults int    `json:"max_results"`
	Group      string `json:"group,omitempty"`      // Filter by group
	MinScore   int    `json:"min_score,omitempty"`  // Minimum similarity score (0-100)
}

// SimilarClipResult represents a similar clip with similarity score
type SimilarClipResult struct {
	Clip         IndexedClip `json:"clip"`
	Score        int         `json:"score"`         // 0-100 similarity
	MatchType    string      `json:"match_type"`    // "tags", "group", "name", "mixed"
	MatchDetails string      `json:"match_details"` // Human-readable explanation
}

// FindSimilarClips finds clips similar to a given clip by ID
func (idx *Indexer) FindSimilarClips(req SimilarClipsRequest) ([]SimilarClipResult, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return nil, nil
	}

	// Find the source clip
	var sourceClip *IndexedClip
	for i := range idx.index.Clips {
		if idx.index.Clips[i].ID == req.ClipID {
			sourceClip = &idx.index.Clips[i]
			break
		}
	}

	if sourceClip == nil {
		return nil, nil // Clip not found
	}

	maxResults := req.MaxResults
	if maxResults == 0 {
		maxResults = 10
	}

	minScore := req.MinScore
	if minScore == 0 {
		minScore = 20
	}

	var results []SimilarClipResult

	for i := range idx.index.Clips {
		target := &idx.index.Clips[i]

		// Skip the source clip itself
		if target.ID == sourceClip.ID {
			continue
		}

		// Filter by group if specified
		if req.Group != "" && target.Group != req.Group {
			continue
		}

		score, matchType, details := idx.computeSimilarity(sourceClip, target)

		if score >= minScore {
			results = append(results, SimilarClipResult{
				Clip:         *target,
				Score:        score,
				MatchType:    matchType,
				MatchDetails: details,
			})
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Limit results
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	return results, nil
}

// computeSimilarity calculates similarity score between two clips
func (idx *Indexer) computeSimilarity(source, target *IndexedClip) (int, string, string) {
	score := 0
	var matchTypes []string
	var details []string

	// 1. Tag overlap (highest weight: up to 50 points)
	commonTags := idx.tagOverlap(source.Tags, target.Tags)
	if len(commonTags) > 0 {
		tagScore := int(math.Min(50, float64(len(commonTags))*10))
		score += tagScore
		matchTypes = append(matchTypes, "tags")
		details = append(details, "tags:"+strings.Join(commonTags, ","))
	}

	// 2. Same group (30 points)
	if source.Group == target.Group && source.Group != "" && source.Group != "general" {
		score += 30
		matchTypes = append(matchTypes, "group")
		details = append(details, "same_group:"+source.Group)
	}

	// 3. Same folder/parent (10 points)
	if source.FolderID == target.FolderID {
		score += 10
		matchTypes = append(matchTypes, "folder")
		details = append(details, "same_folder")
	} else if source.FolderPath != "" && target.FolderPath != "" {
		// Check if they share a common parent path
		sourceParts := strings.Split(source.FolderPath, "/")
		targetParts := strings.Split(target.FolderPath, "/")

		commonParts := 0
		for i := 0; i < len(sourceParts) && i < len(targetParts); i++ {
			if strings.EqualFold(sourceParts[i], targetParts[i]) {
				commonParts++
			} else {
				break
			}
		}

		if commonParts >= 2 {
			score += 5
			matchTypes = append(matchTypes, "path")
			details = append(details, "common_path:"+strings.Join(sourceParts[:commonParts], "/"))
		}
	}

	// 4. Name similarity (10 points)
	nameSimilarity := idx.nameSimilarity(source.Name, target.Name)
	if nameSimilarity > 0.3 {
		nameScore := int(nameSimilarity * 10)
		score += nameScore
		matchTypes = append(matchTypes, "name")
		details = append(details, fmt.Sprintf("name_sim:%.2f", nameSimilarity))
	}

	// Determine primary match type
	matchType := "mixed"
	if len(matchTypes) == 1 {
		matchType = matchTypes[0]
	}

	return score, matchType, strings.Join(details, "; ")
}

// tagOverlap returns common tags between two clips
func (idx *Indexer) tagOverlap(tags1, tags2 []string) []string {
	set1 := make(map[string]bool)
	for _, t := range tags1 {
		set1[strings.ToLower(t)] = true
	}

	var common []string
	for _, t := range tags2 {
		tLower := strings.ToLower(t)
		if set1[tLower] {
			common = append(common, t)
		}
	}

	return common
}

// nameSimilarity returns a 0.0-1.0 similarity score between two names
func (idx *Indexer) nameSimilarity(name1, name2 string) float64 {
	n1 := strings.ToLower(name1)
	n2 := strings.ToLower(name2)

	if n1 == n2 {
		return 1.0
	}

	// Simple word overlap
	words1 := strings.Fields(n1)
	words2 := strings.Fields(n2)

	if len(words1) == 0 || len(words2) == 0 {
		return 0
	}

	set2 := make(map[string]bool)
	for _, w := range words2 {
		set2[w] = true
	}

	overlap := 0
	for _, w := range words1 {
		if set2[w] {
			overlap++
		}
	}

	// Jaccard-like similarity
	return float64(overlap) / float64(len(words1)+len(words2)-overlap)
}
