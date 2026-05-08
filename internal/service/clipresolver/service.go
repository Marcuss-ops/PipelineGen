package clipresolver

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"velox/go-master/internal/service/clipcatalog"
	"velox/go-master/pkg/models"
	"velox/go-master/pkg/textutil"
)

// Service provides clip recommendation functionality
type Service struct {
	catalogRepo  *clipcatalog.Repository
	harvestSvc   ArtlistHarvestService
	ontologyPath string
}

// ArtlistHarvestService interface for enqueueing harvest jobs
type ArtlistHarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (jobID string, err error)
}

// NewService creates a new clip resolver service
func NewService(catalogRepo *clipcatalog.Repository, harvestSvc ArtlistHarvestService, ontologyPath string) *Service {
	return &Service{
		catalogRepo:  catalogRepo,
		harvestSvc:   harvestSvc,
		ontologyPath: ontologyPath,
	}
}

// Recommend provides clip recommendations based on segment context
func (s *Service) Recommend(ctx context.Context, req *RecommendRequest) (*RecommendResponse, error) {
	resp := &RecommendResponse{
		OK:          true,
		Topic:       req.Topic,
		SegmentID:   req.SegmentID,
		Recommended: make([]RecommendedClip, 0),
		Rejected:    make([]RejectedClip, 0),
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
	clipScores := make(map[string]*ClipScore)
	for _, term := range searchTerms {
		candidates, err := s.catalogRepo.FindCandidates(ctx, term, limit*2)
		if err != nil {
			continue
		}

		for _, cand := range candidates {
			if clipScores[cand.ID] == nil {
				clipScores[cand.ID] = &ClipScore{
					Clip:        s.candidateToClip(cand),
					Score:       0,
					Breakdown:   &ScoreBreakdown{},
					MatchedTerms: make([]string, 0),
				}
			}

			entry := clipScores[cand.ID]
			s.scoreClip(entry, term, req, avoidTerms, usedClipIDs)
		}
	}

	// Process all scored clips
	for clipID, entry := range clipScores {
		if entry.Score < minScore {
			if req.Explain {
				resp.Rejected = append(resp.Rejected, RejectedClip{
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
				resp.Rejected = append(resp.Rejected, RejectedClip{
					ClipID:       clipID,
					Title:        entry.Clip.Name,
					Score:        entry.Score,
					RejectReason: entry.RejectReason,
				})
			}
			continue
		}

		rec := RecommendedClip{
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

		// Auto-harvest if enabled
		if req.AutoHarvest && s.harvestSvc != nil {
			resp.HarvestJobIDs = s.enqueueHarvestForTerms(ctx, resp.HarvestTerms)
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

func (s *Service) scoreClip(entry *ClipScore, matchedQuery string, req *RecommendRequest, avoidTerms map[string]bool, usedClipIDs map[string]bool) {
	c := entry.Clip
	bd := entry.Breakdown

	// Text score - based on search_text, name, tags
	textScore := s.calculateTextScore(c, matchedQuery)
	bd.TextScore = textScore
	entry.Score += textScore

	// Update matched query and terms
	if entry.MatchedQuery == "" {
		entry.MatchedQuery = matchedQuery
	}
	entry.MatchedTerms = append(entry.MatchedTerms, matchedQuery)

	// Topic boost (weight: 0.20)
	if req.Topic != "" && s.matchesTopic(c, req.Topic) {
		boost := 0.20
		bd.TopicBoost = boost
		entry.Score += boost
	}

	// Category boost (weight: 0.10)
	if req.Category != "" && strings.EqualFold(c.Category, req.Category) {
		boost := 0.10
		bd.CategoryBoost = boost
		entry.Score += boost
	}

	// UsableFor boost (weight: 0.15)
	if len(c.UsableFor) > 0 {
		for _, term := range req.Queries {
			if s.clipUsableFor(c, term) {
				boost := 0.15
				bd.UsableForBoost += boost
				entry.Score += boost
				break
			}
		}
	}

	// Quality score (weight: 0.15)
	qualityScore := c.QualityScore * 0.15
	if qualityScore > 0.15 {
		qualityScore = 0.15
	}
	bd.QualityScore = qualityScore
	entry.Score += qualityScore

	// Negative penalty (weight: 0.40)
	for term := range avoidTerms {
		if s.clipContainsTerm(c, term) {
			penalty := 0.40
			bd.NegativePenalty += penalty
			entry.Score -= penalty
			if entry.RejectReason == "" {
				entry.RejectReason = fmt.Sprintf("Contains avoid term: %s", term)
			}
		}
	}

	// Reuse penalty (weight: 0.10)
	if usedClipIDs[c.ID] {
		penalty := 0.10
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
	// Weight: 0.45
	baseWeight := 0.45

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

	// Apply weight
	score = score * (baseWeight / 0.4) // Normalize since max raw score is ~0.4

	// Cap at baseWeight
	if score > baseWeight {
		score = baseWeight
	}
	return score
}

func (s *Service) matchesTopic(clip *models.Clip, topic string) bool {
	topicTokens := textutil.Tokenize(topic)
	if len(topicTokens) == 0 {
		return false
	}

	searchTermsStr := strings.Join(clip.SearchTerms, " ")
	tagsStr := strings.Join(clip.Tags, " ")
	searchText := strings.ToLower(searchTermsStr + " " + clip.Name + " " + tagsStr)

	// Count matching tokens (only tokens with len >= 4)
	matched := 0
	for _, tok := range topicTokens {
		if len(tok) < 4 {
			continue
		}
		if strings.Contains(searchText, strings.ToLower(tok)) {
			matched++
		}
	}

	// Return true if at least 1 meaningful token matches
	return matched > 0
}

func (s *Service) clipContainsTerm(clip *models.Clip, term string) bool {
	termLower := strings.ToLower(term)

	// Check in search terms, name, tags
	searchTermsStr := strings.Join(clip.SearchTerms, " ")
	tagsStr := strings.Join(clip.Tags, " ")
	searchText := strings.ToLower(searchTermsStr + " " + clip.Name + " " + tagsStr)
	if strings.Contains(searchText, termLower) {
		return true
	}

	// Check in avoid_for list
	for _, avoid := range clip.AvoidFor {
		if strings.EqualFold(avoid, term) {
			return true
		}
	}

	return false
}

func (s *Service) clipUsableFor(clip *models.Clip, term string) bool {
	if len(clip.UsableFor) == 0 {
		return false
	}

	termLower := strings.ToLower(term)
	for _, usable := range clip.UsableFor {
		if strings.EqualFold(usable, term) || strings.Contains(strings.ToLower(usable), termLower) {
			return true
		}
	}
	return false
}

func (s *Service) sortRecommendations(recs []RecommendedClip) {
	n := len(recs)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if recs[j].Score < recs[j+1].Score {
				recs[j], recs[j+1] = recs[j+1], recs[j]
			}
		}
	}
}

func (s *Service) buildRecommendReason(entry *ClipScore, req *RecommendRequest) string {
	reasons := make([]string, 0)

	if entry.Breakdown.TopicBoost > 0 {
		reasons = append(reasons, fmt.Sprintf("Matches topic '%s'", req.Topic))
	}
	if entry.Breakdown.CategoryBoost > 0 {
		reasons = append(reasons, fmt.Sprintf("Category '%s'", entry.Clip.Category))
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

func (s *Service) candidateToClip(cand clipcatalog.ClipCandidate) *models.Clip {
	return &models.Clip{
		ID:           cand.ID,
		Name:         cand.Name,
		DriveLink:    cand.DriveLink,
		LocalPath:     cand.LocalPath,
		Category:      cand.Category,
		SearchTerms:  []string{cand.SearchText},
		Tags:         cand.Tags,
		SearchText:   cand.SearchText,
		SceneType:    cand.SceneType,
		QualityScore: cand.QualityScore,
		ReuseCount:   cand.ReuseCount,
		UsableFor:    cand.UsableFor,
		AvoidFor:     cand.AvoidFor,
	}
}

func (s *Service) enqueueHarvestForTerms(ctx context.Context, terms []string) []string {
	if s.harvestSvc == nil {
		return nil
	}

	jobIDs := make([]string, 0)
	for _, term := range terms {
		jobID, err := s.harvestSvc.EnqueueHarvest(ctx, term, 3, "youtube_1080p_7s")
		if err != nil {
			// Log error but continue with other terms
			continue
		}
		if jobID != "" {
			jobIDs = append(jobIDs, jobID)
		}
	}
	return jobIDs
}
