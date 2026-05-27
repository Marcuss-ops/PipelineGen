package clipresolver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"velox/go-master/internal/media/clipcatalog"
	"velox/go-master/internal/pkg/matchingconfig"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/textutil"
)

// Service provides clip recommendation functionality
type Service struct {
	repos          map[string]*clipcatalog.Repository
	harvestSvc     ArtlistHarvestService
	embedProvider  EmbeddingProvider
	ontologyScorer OntologyScorer
	matchingConfig *matchingconfig.MatchingConfig
	vectorStore    VectorStoreSearcher
}

// ArtlistHarvestService interface for enqueueing harvest jobs
type ArtlistHarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (jobID string, err error)
}

// NewService creates a new clip resolver service
func NewService(
	repos map[string]*clipcatalog.Repository,
	harvestSvc ArtlistHarvestService,
	embedProvider EmbeddingProvider,
	ontologyScorer OntologyScorer,
	matchingConfig *matchingconfig.MatchingConfig,
	vectorStore VectorStoreSearcher,
) *Service {
	return &Service{
		repos:          repos,
		harvestSvc:     harvestSvc,
		embedProvider:  embedProvider,
		ontologyScorer: ontologyScorer,
		matchingConfig: matchingConfig,
		vectorStore:    vectorStore,
	}
}

// SetVectorStore sets the vector store searcher for primary ANN search.
// This is the main entry point for Qdrant integration in the resolver.
func (s *Service) SetVectorStore(vs VectorStoreSearcher) {
	s.vectorStore = vs
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
		minScore = s.matchingConfig.Matching.MinScore
	}

	// Build set of used clip IDs for quick lookup
	usedClipIDs := make(map[string]bool)
	for _, id := range req.UsedClipIDs {
		usedClipIDs[id] = true
	}

	usedFolders := make(map[string]bool)
	for _, f := range req.UsedFolderIDs {
		usedFolders[strings.ToLower(strings.TrimSpace(f))] = true
	}

	usedPaths := make(map[string]bool)
	for _, p := range req.UsedPaths {
		usedPaths[strings.ToLower(strings.TrimSpace(p))] = true
	}

	// Build set of avoid terms
	avoidTerms := make(map[string]bool)
	for _, term := range req.AvoidTerms {
		avoidTerms[strings.ToLower(strings.TrimSpace(term))] = true
	}

	// Search for clips using all search terms across all repositories with weights
	clipScores := make(map[string]*ClipScore)

	type WeightedQuery struct {
		Term   string
		Weight float64
	}

	weightedQueries := []WeightedQuery{}
	seenQueries := make(map[string]bool)

	addWeighted := func(term string, weight float64) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		lower := strings.ToLower(term)
		if seenQueries[lower] {
			return
		}
		seenQueries[lower] = true
		weightedQueries = append(weightedQueries, WeightedQuery{Term: term, Weight: weight})
	}

	// 1. Entity queries (Highest weight for direct subject match)
	for _, q := range req.EntityQueries {
		addWeighted(q, 1.3)
	}

	// 2. Visual prompts (High weight for semantic/vector match)
	for _, q := range req.VisualPrompts {
		addWeighted(q, 1.2)
	}

	// 3. Regular queries (Standard weight)
	for _, q := range req.Queries {
		addWeighted(q, 1.0)
	}

	// 4. Topic as fallback
	if req.Topic != "" {
		addWeighted(req.Topic, 1.0)
	}

	// Prepare query embeddings for all weighted queries
	queryEmbeddings := make(map[string][]float64)
	if s.embedProvider != nil {
		for _, wq := range weightedQueries {
			emb, err := s.embedProvider.EmbedText(ctx, wq.Term)
			if err == nil {
				queryEmbeddings[wq.Term] = emb
			}
		}
	}

	for _, wq := range weightedQueries {
		term := wq.Term
		for source, repo := range s.repos {
			// Step 1: Qdrant ANN search (primary, if configured)
			var candidates []clipcatalog.ClipCandidate
			if s.vectorStore != nil {
				emb, ok := queryEmbeddings[term]
				if ok && len(emb) > 0 {
					emb32 := float64To32(emb)
					
					// Parallel search across text, visual, and audio vectors
					vectorSpaces := []string{"text", "visual", "audio"}
					for _, vSpace := range vectorSpaces {
						results, err := s.vectorStore.Search(ctx, SearchRequest{
							QueryVector: emb32,
							VectorName:  vSpace,
							Limit:       limit * 2,
							MinScore:    s.matchingConfig.Matching.MinScore,
							Source:      source,
						})
						if err == nil && len(results) > 0 {
							for _, r := range results {
								cand := qdrantResultToCandidate(r)
								// Assign vector-specific score to candidate temporarily
								// We'll fuse them in scoreClipWeighted
								candidates = append(candidates, cand)
								
								globalID := fmt.Sprintf("%s:%s", source, cand.ID)
								if clipScores[globalID] == nil {
									clipScores[globalID] = &ClipScore{
										Clip:         s.candidateToClip(cand),
										Score:        0,
										Breakdown:    &ScoreBreakdown{},
										MatchedTerms: make([]string, 0),
									}
									clipScores[globalID].Clip.MediaType = source
								}
								
								// Accumulate vector scores into breakdown
								entry := clipScores[globalID]
								vScore := r.Score * wq.Weight
								switch vSpace {
								case "text":
									if vScore > entry.Breakdown.TextScore {
										entry.Breakdown.TextScore = vScore
									}
								case "visual":
									if vScore > entry.Breakdown.VisualScore {
										entry.Breakdown.VisualScore = vScore
									}
								case "audio":
									if vScore > entry.Breakdown.AudioScore {
										entry.Breakdown.AudioScore = vScore
									}
								}
							}
						}
					}
				}
			}

			// Step 2: FTS fallback if Qdrant returned nothing
			if len(candidates) == 0 {
				ftsCandidates, ftsErr := repo.FindCandidatesFTS(ctx, term, limit*2)
				if ftsErr == nil && len(ftsCandidates) > 0 {
					candidates = ftsCandidates
				}
			}

			// Step 3: Text matching fallback if FTS also fails
			if len(candidates) == 0 {
				textCandidates, textErr := repo.FindCandidates(ctx, term, limit*2)
				if textErr == nil {
					candidates = textCandidates
				}
			}

			for _, cand := range candidates {
				globalID := fmt.Sprintf("%s:%s", source, cand.ID)
				if clipScores[globalID] == nil {
					clipScores[globalID] = &ClipScore{
						Clip:         s.candidateToClip(cand),
						Score:        0,
						Breakdown:    &ScoreBreakdown{},
						MatchedTerms: make([]string, 0),
					}
					// Store original source for boosting (hijack MediaType temporarily)
					clipScores[globalID].Clip.MediaType = source
				}

				entry := clipScores[globalID]
				s.scoreClipWeighted(ctx, entry, wq.Term, wq.Weight, queryEmbeddings[wq.Term], req, avoidTerms, usedClipIDs, usedFolders, usedPaths)
			}
		}
	}

	// Process all scored clips
	for clipID, entry := range clipScores {
		// Final ontology application to the overall score
		entry.Score = ApplyOntologyBoost(s.ontologyScorer, entry.Score, entry.Clip, req.Topic)

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
			ClipID:         clipID,
			Title:          entry.Clip.Name,
			DriveLink:      entry.Clip.DriveLink,
			LocalPath:      entry.Clip.LocalPath,
			FolderID:       entry.Clip.ParentFolderID,
			FolderPath:     entry.Clip.FolderPath,
			Score:          entry.Score,
			MatchedQuery:   entry.MatchedQuery,
			Category:       entry.Clip.Category,
			MatchedTerms:   entry.MatchedTerms,
			ScoreBreakdown: entry.Breakdown,
			Reason:         s.buildRecommendReason(entry, req),
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

func (s *Service) scoreClipWeighted(ctx context.Context, entry *ClipScore, matchedQuery string, queryWeight float64, queryEmbedding []float64, req *RecommendRequest, avoidTerms map[string]bool, usedClipIDs map[string]bool, usedFolders map[string]bool, usedPaths map[string]bool) {
	c := entry.Clip
	bd := entry.Breakdown
	source := c.MediaType // Source name stored here

	// 1. Text score (weighted by query importance)
	textWeight := s.matchingConfig.Matching.TextScoreWeight
	if textWeight == 0 {
		textWeight = 0.35
	}
	
	// Try to get rich metadata
	// Note: Clip catalog candidate doesn't have metadata_json yet, but we'll add it to candidateToClip later
	// For now we check if cand has it via repository lookup or assume it's in SearchText
	
	// Fusion Scorer: Combine text, visual and audio vector scores
	vFusionScore := (bd.TextScore * 0.40) + (bd.VisualScore * 0.40) + (bd.AudioScore * 0.20)
	
	finalTextScore := 0.0
	if vFusionScore > 0 {
		finalTextScore = vFusionScore * textWeight
	} else {
		finalTextScore = s.calculateTextScore(c, matchedQuery) * (textWeight / 0.45) * queryWeight
	}
	
	// Tiered Boost
	tierBoost := 0.0
	if strings.Contains(c.SearchText, "generated_light") || strings.Contains(c.Name, "generated") {
		tierBoost = 0.15 // Generated images are very accurate to prompt
	}
	
	bd.TextScore = finalTextScore
	entry.Score += finalTextScore + tierBoost

	// Update matched query and terms
	if entry.MatchedQuery == "" {
		entry.MatchedQuery = matchedQuery
	}
	entry.MatchedTerms = append(entry.MatchedTerms, matchedQuery)

	// 3. Source boost (PRIORITIZATION)
	if source == "stock" || source == "youtube" {
		boost := 0.50
		bd.SourceBoost = boost
		entry.Score += boost
	}

	// 4. Topic boost
	topicWeight := s.matchingConfig.Matching.TopicBoostWeight
	if topicWeight == 0 {
		topicWeight = 0.20
	}
	if req.Topic != "" && s.matchesTopic(c, req.Topic) {
		boost := topicWeight
		bd.TopicBoost = boost
		entry.Score += boost
	}

	// 5. Category boost
	categoryWeight := s.matchingConfig.Matching.CategoryBoostWeight
	if categoryWeight == 0 {
		categoryWeight = 0.10
	}
	if req.Category != "" && strings.EqualFold(c.Category, req.Category) {
		boost := categoryWeight
		bd.CategoryBoost = boost
		entry.Score += boost
	}

	// 6. UsableFor boost
	usableWeight := s.matchingConfig.Matching.UsableForBoostWeight
	if usableWeight == 0 {
		usableWeight = 0.15
	}
	if len(c.UsableFor) > 0 {
		for _, term := range req.Queries {
			if s.clipUsableFor(c, term) {
				boost := usableWeight
				bd.UsableForBoost += boost
				entry.Score += boost
				break
			}
		}
	}

	// 7. Quality score
	qualityWeight := s.matchingConfig.Matching.QualityScoreWeight
	if qualityWeight == 0 {
		qualityWeight = 0.15
	}
	qualityScore := c.QualityScore * qualityWeight
	if qualityScore > qualityWeight {
		qualityScore = qualityWeight
	}
	bd.QualityScore = qualityScore
	entry.Score += qualityScore

	// 8. Negative penalty
	negativeWeight := s.matchingConfig.Matching.NegativePenaltyWeight
	if negativeWeight == 0 {
		negativeWeight = 0.40
	}
	for term := range avoidTerms {
		if s.clipContainsTerm(c, term) {
			penalty := negativeWeight
			bd.NegativePenalty += penalty
			entry.Score -= penalty
			if entry.RejectReason == "" {
				entry.RejectReason = fmt.Sprintf("Contains avoid term: %s", term)
			}
		}
	}

	// 9. Reuse penalty
	reuseWeight := s.matchingConfig.Matching.ReusePenaltyWeight
	if reuseWeight == 0 {
		reuseWeight = 0.10
	}
	if usedClipIDs[c.ID] {
		penalty := reuseWeight
		bd.ReusePenalty = penalty
		entry.Score -= penalty
		if entry.RejectReason == "" {
			entry.RejectReason = "Clip already used in timeline"
		}
	}

	// 10. Diversity penalty
	if c.LocalPath != "" {
		lowerPath := strings.ToLower(c.LocalPath)
		if usedPaths[lowerPath] {
			penalty := 0.30
			bd.DiversityPenalty += penalty
			entry.Score -= penalty
		}

		folderKey := s.folderKeyFromPath(c.LocalPath)
		if folderKey != "" && usedFolders[folderKey] {
			penalty := 0.25
			bd.DiversityPenalty += penalty
			entry.Score -= penalty
		}
	}

	if entry.Score < 0 {
		entry.Score = 0
	}
}

func (s *Service) scoreClip(ctx context.Context, entry *ClipScore, matchedQuery string, queryEmbedding []float64, req *RecommendRequest, avoidTerms map[string]bool, usedClipIDs map[string]bool, usedFolders map[string]bool, usedPaths map[string]bool) {
	s.scoreClipWeighted(ctx, entry, matchedQuery, 1.0, queryEmbedding, req, avoidTerms, usedClipIDs, usedFolders, usedPaths)
}

func (s *Service) folderKeyFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "/" {
		return ""
	}
	return strings.ToLower(dir)
}

func (s *Service) calculateTextScore(clip *models.MediaAsset, query string) float64 {
	// Weight: 0.45
	baseWeight := 0.45

	searchTermsStr := strings.Join(clip.SearchTerms, " ")
	tagsStr := strings.Join(clip.Tags, " ")
	targetText := clip.Name + " " + searchTermsStr + " " + tagsStr

	queryTokens := textutil.Tokenize(query)
	targetTokens := textutil.Tokenize(targetText)

	score := 0.0
	for _, qt := range queryTokens {
		if !s.matchingConfig.IsMeaningfulToken(qt) {
			continue
		}
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

func (s *Service) matchesTopic(clip *models.MediaAsset, topic string) bool {
	topicTokens := textutil.Tokenize(topic)
	if len(topicTokens) == 0 {
		return false
	}

	searchTermsStr := strings.Join(clip.SearchTerms, " ")
	tagsStr := strings.Join(clip.Tags, " ")
	searchText := strings.ToLower(searchTermsStr + " " + clip.Name + " " + tagsStr)

	// Count matching tokens (filter out meaningless short tokens)
	matched := 0
	for _, tok := range topicTokens {
		if !s.matchingConfig.IsMeaningfulToken(tok) {
			continue
		}
		if strings.Contains(searchText, strings.ToLower(tok)) {
			matched++
		}
	}

	// Return true if at least 1 meaningful token matches
	return matched > 0
}

func (s *Service) clipContainsTerm(clip *models.MediaAsset, term string) bool {
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

func (s *Service) clipUsableFor(clip *models.MediaAsset, term string) bool {
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

func (s *Service) candidateToClip(cand clipcatalog.ClipCandidate) *models.MediaAsset {
	return &models.MediaAsset{
		ID:             cand.ID,
		Name:           cand.Name,
		DriveLink:      cand.DriveLink,
		LocalPath:      cand.LocalPath,
		ParentFolderID: cand.FolderID,
		FolderPath:     cand.FolderPath,
		Category:       cand.Category,
		SearchTerms:    []string{cand.SearchText},
		Tags:           cand.Tags,
		SearchText:     cand.SearchText,
		SceneType:      cand.SceneType,
		QualityScore:   cand.QualityScore,
		ReuseCount:     cand.ReuseCount,
		UsableFor:      cand.UsableFor,
		AvoidFor:       cand.AvoidFor,
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
