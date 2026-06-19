package memory

import (
	"context"
	"sort"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// RetrieveRelevantContext implements the 3-level memory retrieval:
// Level 1: channel style rules
// Level 2: topic-specific memories + similar chunks
// Level 3: recent successful outputs as structural reference
func RetrieveRelevantContext(ctx context.Context, repo *Repository, q MemoryGateRequest) []MemoryHit {
	topicKey := BuildTopicKey(q.Title, q.Prompt)
	tokens := ExtractSearchTokens(q.Title + " " + q.Prompt)
	var hits []MemoryHit

	// Level 1: Channel style rules (always relevant)
	if channelHits, err := repo.FindMemoryByChannel(ctx, q.ChannelID, MemoryTypeChannelStyle, 5); err == nil {
		for _, h := range channelHits {
			hits = append(hits, MemoryHit{Entry: h, Relevance: 0.9, Source: "channel_style"})
		}
	}

	// Level 2: Topic-specific memories (same topic key)
	if topicHits, err := repo.FindMemoryByTopicKey(ctx, q.ChannelID, topicKey, 8); err == nil {
		for _, h := range topicHits {
			score := 0.85
			// Boost score for specific memory types that are more useful
			switch h.MemoryType {
			case MemoryTypeSuccessfulHook, MemoryTypeScriptStructure:
				score = 0.9
			case MemoryTypeTopicResearch, MemoryTypeCharacterProfile:
				score = 0.88
			case MemoryTypeBadPattern:
				score = 0.82 // still useful but less critical
			}
			hits = append(hits, MemoryHit{Entry: h, Relevance: score, Source: "topic_key"})
		}
	}

	// Level 2b: Similar script chunks via LIKE search
	if len(tokens) > 0 {
		if chunks, err := repo.FindSimilarChunksBySearchText(ctx, q.ChannelID, tokens, 5); err == nil {
			for _, ch := range chunks {
				entry := MemoryEntry{
					ID:          ch.ID,
					ChannelID:   ch.ChannelID,
					MemoryType:  "script_chunk",
					TopicKey:    ch.TopicKey,
					Title:       ch.Title,
					Summary:     textutil.Truncate(ch.Text, 200),
					ContentText: ch.Text,
				}
				// Score based on number of matching tokens
				score := computeChunkRelevance(tokens, ch.SearchText)
				hits = append(hits, MemoryHit{Entry: entry, Relevance: score, Source: "search"})
			}
		}
	}

	// Level 3: Recent successful outputs for structural reference (up to 2)
	if recentHits, err := repo.FindMemoryByChannel(ctx, q.ChannelID, MemoryTypeScriptStructure, 2); err == nil {
		for _, h := range recentHits {
			// Only add if not already covered by topic_key hits
			alreadyCovered := false
			for _, existing := range hits {
				if existing.Entry.ID == h.ID {
					alreadyCovered = true
					break
				}
			}
			if !alreadyCovered {
				hits = append(hits, MemoryHit{Entry: h, Relevance: 0.7, Source: "recent"})
			}
		}
	}

	// Sort by relevance descending
	sort.Slice(hits, func(i, j int) bool {
		return hits[i].Relevance > hits[j].Relevance
	})

	// Trim to top 8 most relevant
	if len(hits) > 8 {
		hits = hits[:8]
	}

	return hits
}

// computeChunkRelevance scores a chunk based on how many search tokens it contains.
func computeChunkRelevance(queryTokens []string, searchText string) float64 {
	if len(queryTokens) == 0 || searchText == "" {
		return 0
	}
	var matched int
	for _, tok := range queryTokens {
		if len(tok) >= 3 && containsIgnoreCase(searchText, tok) {
			matched++
		}
	}
	return float64(matched) / float64(len(queryTokens))
}

func containsIgnoreCase(s, substr string) bool {
	return textutil.ContainsCI(s, substr)
}
