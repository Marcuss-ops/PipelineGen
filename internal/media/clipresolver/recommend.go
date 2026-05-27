package clipresolver

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/media/clipcatalog"
)

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

	avoidTerms := make(map[string]bool)
	for _, term := range req.AvoidTerms {
		avoidTerms[strings.ToLower(strings.TrimSpace(term))] = true
	}

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

	for _, q := range req.EntityQueries {
		addWeighted(q, 1.3)
	}

	for _, q := range req.VisualPrompts {
		addWeighted(q, 1.2)
	}

	for _, q := range req.Queries {
		addWeighted(q, 1.0)
	}

	if req.Topic != "" {
		addWeighted(req.Topic, 1.0)
	}

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
			var candidates []clipcatalog.ClipCandidate
			if s.vectorStore != nil {
				emb, ok := queryEmbeddings[term]
				if ok && len(emb) > 0 {
					emb32 := float64To32(emb)

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

			if len(candidates) == 0 {
				ftsCandidates, ftsErr := repo.FindCandidatesFTS(ctx, term, limit*2)
				if ftsErr == nil && len(ftsCandidates) > 0 {
					candidates = ftsCandidates
				}
			}

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
					clipScores[globalID].Clip.MediaType = source
				}

				entry := clipScores[globalID]
				s.scoreClipWeighted(ctx, entry, wq.Term, wq.Weight, queryEmbeddings[wq.Term], req, avoidTerms, usedClipIDs, usedFolders, usedPaths)
			}
		}
	}

	for clipID, entry := range clipScores {
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

	s.sortRecommendations(resp.Recommended)

	if len(resp.Recommended) > limit {
		resp.Recommended = resp.Recommended[:limit]
	}

	if len(resp.Recommended) == 0 {
		resp.NeedsHarvest = true
		resp.HarvestTerms = req.Queries
		if len(resp.HarvestTerms) == 0 && req.Topic != "" {
			resp.HarvestTerms = []string{req.Topic}
		}

		if req.AutoHarvest && s.harvestSvc != nil {
			resp.HarvestJobIDs = s.enqueueHarvestForTerms(ctx, resp.HarvestTerms)
		}
	}

	return resp, nil
}
