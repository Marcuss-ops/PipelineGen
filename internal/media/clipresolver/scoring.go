package clipresolver

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/textutil"
)

func (s *Service) scoreClipWeighted(ctx context.Context, entry *ClipScore, matchedQuery string, queryWeight float64, queryEmbedding []float64, req *RecommendRequest, avoidTerms map[string]bool, usedClipIDs map[string]bool, usedFolders map[string]bool, usedPaths map[string]bool) {
	c := entry.Clip
	bd := entry.Breakdown
	source := c.MediaType

	textWeight := s.matchingConfig.Matching.TextScoreWeight
	if textWeight == 0 {
		textWeight = 0.35
	}

	vFusionScore := (bd.TextScore * 0.40) + (bd.VisualScore * 0.40) + (bd.AudioScore * 0.20)

	finalTextScore := 0.0
	if vFusionScore > 0 {
		finalTextScore = vFusionScore * textWeight
	} else {
		finalTextScore = s.calculateTextScore(c, matchedQuery) * (textWeight / 0.45) * queryWeight
	}

	tierBoost := 0.0
	if strings.Contains(c.SearchText, "generated_light") || strings.Contains(c.Name, "generated") {
		tierBoost = 0.15
	}

	bd.TextScore = finalTextScore
	entry.Score += finalTextScore + tierBoost

	if entry.MatchedQuery == "" {
		entry.MatchedQuery = matchedQuery
	}
	entry.MatchedTerms = append(entry.MatchedTerms, matchedQuery)

	if source == "stock" || source == "youtube" {
		boost := 0.50
		bd.SourceBoost = boost
		entry.Score += boost
	}

	topicWeight := s.matchingConfig.Matching.TopicBoostWeight
	if topicWeight == 0 {
		topicWeight = 0.20
	}
	if req.Topic != "" && s.matchesTopic(c, req.Topic) {
		boost := topicWeight
		bd.TopicBoost = boost
		entry.Score += boost
	}

	categoryWeight := s.matchingConfig.Matching.CategoryBoostWeight
	if categoryWeight == 0 {
		categoryWeight = 0.10
	}
	if req.Category != "" && strings.EqualFold(c.Category, req.Category) {
		boost := categoryWeight
		bd.CategoryBoost = boost
		entry.Score += boost
	}

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

func (s *Service) calculateTextScore(clip *models.MediaAsset, query string) float64 {
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

	score = score * (baseWeight / 0.4)

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

	matched := 0
	for _, tok := range topicTokens {
		if !s.matchingConfig.IsMeaningfulToken(tok) {
			continue
		}
		if strings.Contains(searchText, strings.ToLower(tok)) {
			matched++
		}
	}

	return matched > 0
}

func (s *Service) clipContainsTerm(clip *models.MediaAsset, term string) bool {
	termLower := strings.ToLower(term)

	searchTermsStr := strings.Join(clip.SearchTerms, " ")
	tagsStr := strings.Join(clip.Tags, " ")
	searchText := strings.ToLower(searchTermsStr + " " + clip.Name + " " + tagsStr)
	if strings.Contains(searchText, termLower) {
		return true
	}

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
