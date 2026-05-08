package artlist

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"velox/go-master/pkg/models"
	"velox/go-master/pkg/textutil"
)

// Recommend provides clip recommendations based on segment context
func (s *Service) Recommend(ctx context.Context, req *RecommendRequest) (*RecommendResponse, error) {
	resp := &RecommendResponse{
		OK:          true,
		Topic:       req.Topic,
		SegmentID:   req.SegmentID,
		Recommended: make([]ClipRecommend, 0),
		Rejected:    make([]ClipRejected, 0),
	}

	if len(req.Queries) == 0 && req.Topic == "" && req.SegmentText == "" {
		return resp, nil
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	minScore := req.MinScore
	if minScore <= 0 {
		minScore = 0.5
	}

	// Collect all search terms from queries and topic
	searchTerms := s.collectSearchTerms(req)

	// Build set of used clip IDs for quick lookup
	usedClipIDs := make(map[string]bool)
	for _, id := range req.UsedClipIDs {
		usedClipIDs[id] = true
	}

	// Build set of avoid terms
	avoidTerms := make(map[string]bool)
	for _, term := range req.AvoidTerms {
		avoidTerms[strings.ToLower(strings.TrimSpace(term))] = true
	}

	// Search for clips using all search terms
	clipScores := make(map[string]*ClipWithScore)
	for _, term := range searchTerms {
		clips, err := s.searchClipsForRecommend(ctx, term)
		if err != nil {
			continue
		}

		for _, clip := range clips {
			if clip == nil {
				continue
			}
			if clipScores[clip.ID] == nil {
				clipScores[clip.ID] = &ClipWithScore{
					Clip:        clip,
					Score:       0,
					Breakdown:   &ScoreBreakdown{},
					MatchedTerms: make([]string, 0),
				}
			}

			entry := clipScores[clip.ID]
			s.scoreClip(entry, term, req, avoidTerms, usedClipIDs)
		}
	}

	// Process all scored clips
	for clipID, entry := range clipScores {
		if entry.Score < minScore {
			if req.Explain {
				resp.Rejected = append(resp.Rejected, ClipRejected{
					ClipID:       clipID,
					Title:        entry.Clip.Name,
					Score:        entry.Score,
					RejectReason: fmt.Sprintf("Score %.2f below min_score %.2f", entry.Score, minScore),
				})
			}
			continue
		}

		// Check negative terms
		if entry.RejectReason != "" {
			if req.Explain {
				resp.Rejected = append(resp.Rejected, ClipRejected{
					ClipID:       clipID,
					Title:        entry.Clip.Name,
					Score:        entry.Score,
					RejectReason: entry.RejectReason,
				})
			}
			continue
		}

		rec := ClipRecommend{
			ClipID:        clipID,
			Title:         entry.Clip.Name,
			DriveLink:     entry.Clip.DriveLink,
			LocalPath:     entry.Clip.LocalPath,
			Score:         entry.Score,
			MatchedQuery:  entry.MatchedQuery,
			Category:      entry.Clip.Category,
			MatchedTerms:  entry.MatchedTerms,
			ScoreBreakdown: entry.Breakdown,
			Reason:        s.buildRecommendReason(entry, req),
		}

		if !req.Explain {
			rec.ScoreBreakdown = nil
		}

		resp.Recommended = append(resp.Recommended, rec)
	}

	// Sort by score descending
	s.sortRecommendations(resp.Recommended)

	// Limit results
	if len(resp.Recommended) > limit {
		resp.Recommended = resp.Recommended[:limit]
	}

	// Check if we need to harvest
	if len(resp.Recommended) == 0 {
		resp.NeedsHarvest = true
		resp.HarvestTerms = req.Queries
		if len(resp.HarvestTerms) == 0 && req.Topic != "" {
			resp.HarvestTerms = []string{req.Topic}
		}
	}

	return resp, nil
}

func (s *Service) collectSearchTerms(req *RecommendRequest) []string {
	terms := make([]string, 0)
	seen := make(map[string]bool)

	addTerm := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		lower := strings.ToLower(term)
		if !seen[lower] {
			seen[lower] = true
			terms = append(terms, term)
		}
	}

	// Add queries first
	for _, q := range req.Queries {
		addTerm(q)
	}

	// Add topic
	if req.Topic != "" {
		addTerm(req.Topic)
	}

	// Extract terms from segment text
	if req.SegmentText != "" {
		tokens := textutil.TokenizeWithStopWords(req.SegmentText)
		for _, tok := range tokens {
			tok = strings.TrimSpace(tok)
			if len(tok) >= 4 && len(tok) > 0 && unicode.IsLetter(rune(tok[0])) {
				addTerm(tok)
				if len(terms) >= 10 {
					break
				}
			}
		}
	}

	return terms
}

func (s *Service) searchClipsForRecommend(ctx context.Context, term string) ([]*models.Clip, error) {
	// Search in database using existing SearchClips method
	clips, err := s.artlistRepo.SearchClips(ctx, term)
	if err != nil {
		return nil, err
	}
	return clips, nil
}

func (s *Service) scoreClip(entry *ClipWithScore, matchedQuery string, req *RecommendRequest, avoidTerms map[string]bool, usedClipIDs map[string]bool) {
	clip := entry.Clip
	bd := entry.Breakdown

	// Text score - based on search_text, name, tags
	textScore := s.calculateTextScore(clip, matchedQuery)
	bd.TextScore = textScore
	entry.Score += textScore

	// Update matched query and terms
	if entry.MatchedQuery == "" {
		entry.MatchedQuery = matchedQuery
	}
	entry.MatchedTerms = append(entry.MatchedTerms, matchedQuery)

	// Topic boost
	if req.Topic != "" && s.matchesTopic(clip, req.Topic) {
		boost := 0.15
		bd.TopicBoost = boost
		entry.Score += boost
	}

	// Category boost
	if req.Category != "" && strings.EqualFold(clip.Category, req.Category) {
		boost := 0.10
		bd.CategoryBoost = boost
		entry.Score += boost
	}

	// Quality score from clip metadata
	qualityScore := 0.05 // default
	bd.QualityScore = qualityScore
	entry.Score += qualityScore

	// Negative penalty - check avoid terms
	for term := range avoidTerms {
		if s.clipContainsTerm(clip, term) {
			penalty := 0.5
			bd.NegativePenalty += penalty
			entry.Score -= penalty
			if entry.RejectReason == "" {
				entry.RejectReason = fmt.Sprintf("Contains avoid term: %s", term)
			}
		}
	}

	// Reuse penalty
	if usedClipIDs[clip.ID] {
		penalty := 0.3
		bd.ReusePenalty = penalty
		entry.Score -= penalty
		if entry.RejectReason == "" {
			entry.RejectReason = "Clip already used in timeline"
		}
	}

	// Ensure score is non-negative
	if entry.Score < 0 {
		entry.Score = 0
	}
}

func (s *Service) calculateTextScore(clip *models.Clip, query string) float64 {
	// Simple text scoring based on term matches in name, search_terms, tags
	searchTermsStr := strings.Join(clip.SearchTerms, " ")
	tagsStr := strings.Join(clip.Tags, " ")
	targetText := clip.Name + " " + searchTermsStr + " " + tagsStr

	queryTokens := textutil.Tokenize(query)
	targetTokens := textutil.Tokenize(targetText)

	score := 0.0
	for _, qt := range queryTokens {
		for _, tt := range targetTokens {
			if strings.EqualFold(qt, tt) {
				score += 0.1
			}
		}
	}

	// Cap at reasonable value
	if score > 0.4 {
		score = 0.4
	}
	return score
}

func (s *Service) matchesTopic(clip *models.Clip, topic string) bool {
	// Check if clip is relevant to the topic
	topicLower := strings.ToLower(topic)
	searchTermsStr := strings.Join(clip.SearchTerms, " ")
	tagsStr := strings.Join(clip.Tags, " ")
	searchText := strings.ToLower(searchTermsStr + " " + clip.Name + " " + tagsStr)
	return strings.Contains(searchText, topicLower)
}

func (s *Service) clipContainsTerm(clip *models.Clip, term string) bool {
	termLower := strings.ToLower(term)
	searchTermsStr := strings.Join(clip.SearchTerms, " ")
	tagsStr := strings.Join(clip.Tags, " ")
	searchText := strings.ToLower(searchTermsStr + " " + clip.Name + " " + tagsStr)
	return strings.Contains(searchText, termLower)
}

func (s *Service) sortRecommendations(recs []ClipRecommend) {
	// Simple bubble sort by score descending
	n := len(recs)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if recs[j].Score < recs[j+1].Score {
				recs[j], recs[j+1] = recs[j+1], recs[j]
			}
		}
	}
}

func (s *Service) buildRecommendReason(entry *ClipWithScore, req *RecommendRequest) string {
	reasons := make([]string, 0)

	if entry.Breakdown.TopicBoost > 0 {
		reasons = append(reasons, fmt.Sprintf("Matches topic '%s'", req.Topic))
	}
	if entry.Breakdown.CategoryBoost > 0 {
		reasons = append(reasons, fmt.Sprintf("Category '%s'", req.Category))
	}
	if entry.MatchedQuery != "" {
		reasons = append(reasons, fmt.Sprintf("Matched query '%s'", entry.MatchedQuery))
	}
	if entry.Breakdown.NegativePenalty > 0 {
		reasons = append(reasons, "Has negative terms")
	}
	if entry.Breakdown.ReusePenalty > 0 {
		reasons = append(reasons, "Already used")
	}

	if len(reasons) == 0 {
		return "General match"
	}
	return strings.Join(reasons, "; ")
}
