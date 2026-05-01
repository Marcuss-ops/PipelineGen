package script

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"strings"
	"time"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/artlist"
)

// DynamicArtlistAssociation gestisce il fallback dinamico tramite ricerca live
type DynamicArtlistAssociation struct {
	svc       *artlist.Service
	gen       *ollama.Generator
	req       ScriptDocsRequest
	narrative string
}

func NewDynamicArtlistAssociation(svc *artlist.Service, gen *ollama.Generator, req ScriptDocsRequest, narrative string) *DynamicArtlistAssociation {
	return &DynamicArtlistAssociation{svc: svc, gen: gen, req: req, narrative: narrative}
}

func (a *DynamicArtlistAssociation) Associate(ctx context.Context, segment *TimelineSegment) ([]scoredMatch, error) {
	if a.svc == nil {
		return nil, nil
	}

	startedAt := time.Now()
	zap.L().Info("dynamic artlist association started",
		zap.String("subject", segmentAssociationSubject(segment)),
		zap.Int("existing_stock_matches", len(segment.StockMatches)),
		zap.Int("existing_artlist_matches", len(segment.ArtlistMatches)),
	)

	// Il fallback dinamico parte solo se non esiste nessun candidato statico
	// e non c'è già un hint folder-level dal resolver.
	if strings.TrimSpace(segment.PreferredStockGroup) != "" || len(segment.StockMatches) > 0 || len(segment.ArtlistMatches) > 0 || len(segment.DriveMatches) > 0 {
		zap.L().Info("dynamic artlist association skipped due to existing static candidates",
			zap.String("subject", segmentAssociationSubject(segment)),
			zap.String("preferred_group", segment.PreferredStockGroup),
			zap.Int("stock_matches", len(segment.StockMatches)),
			zap.Int("artlist_matches", len(segment.ArtlistMatches)),
			zap.Int("drive_matches", len(segment.DriveMatches)),
			zap.Duration("elapsed", time.Since(startedAt)),
		)
		return nil, nil
	}

	// Generiamo una lista più ampia di termini, non solo due keyword isolate.
	baseTerms := extractDynamicKeywords(ctx, a.gen, segmentAssociationSubject(segment), segment.NarrativeText)
	searchTerms := mergeTimelineSearchTerms(ctx, a.gen, a.req, *segment, a.narrative, baseTerms)
	segment.SearchSuggestions = searchTerms

	zap.L().Info("dynamic artlist search terms prepared",
		zap.String("subject", segmentAssociationSubject(segment)),
		zap.Int("terms", len(searchTerms)),
		zap.Strings("search_terms", searchTerms),
	)

	if len(searchTerms) == 0 {
		zap.L().Warn("dynamic artlist association has no search terms",
			zap.String("subject", segmentAssociationSubject(segment)),
			zap.Duration("elapsed", time.Since(startedAt)),
		)
		return nil, nil
	}

	// Proviamo i termini in ordine, fermandoci al primo che restituisce clip.
	var liveResp *artlist.SearchResponse
	var runTerm string
	attempts := 0
	for idx, term := range searchTerms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		attempts++
		zap.L().Info("dynamic artlist live search attempt",
			zap.String("subject", segmentAssociationSubject(segment)),
			zap.Int("attempt", idx+1),
			zap.String("term", term),
		)
		fmt.Printf("[Dynamic] No perfect match for '%s', searching Artlist live with '%s'...\n", segmentAssociationSubject(segment), term)
		resp, _, err := a.svc.DiscoverAndQueueRun(ctx, term, 5)
		if err != nil {
			zap.L().Error("dynamic artlist live search failed",
				zap.String("subject", segmentAssociationSubject(segment)),
				zap.String("term", term),
				zap.Error(err),
			)
			return nil, err
		}
		if resp != nil && len(resp.Clips) > 0 {
			liveResp = resp
			runTerm = term
			zap.L().Info("dynamic artlist live search hit",
				zap.String("subject", segmentAssociationSubject(segment)),
				zap.String("term", term),
				zap.Int("clips", len(resp.Clips)),
			)
			break
		}
		zap.L().Info("dynamic artlist live search miss",
			zap.String("subject", segmentAssociationSubject(segment)),
			zap.String("term", term),
		)
	}

	if liveResp == nil || len(liveResp.Clips) == 0 {
		zap.L().Warn("dynamic artlist association produced no clips",
			zap.String("subject", segmentAssociationSubject(segment)),
			zap.Int("attempts", attempts),
			zap.Duration("elapsed", time.Since(startedAt)),
		)
		return nil, nil
	}

	queryTokens := matchTokens(segmentAssociationSubject(segment))
	var matches []scoredMatch
	for _, clip := range liveResp.Clips {
		targetTokens := matchTokens(clip.Name + " " + clip.FolderPath + " " + clip.Group + " " + strings.Join(clip.Tags, " "))
		score := calculateTokenScore(queryTokens, targetTokens)

		// Boost per match dinamico
		score += 10

		matches = append(matches, scoredMatch{
			Title:   clip.Name,
			Path:    clip.LocalPath,
			Score:   score,
			Source:  "artlist_dynamic",
			Link:    clip.ExternalURL,
			Details: fmt.Sprintf("Clip trovata dinamicamente per '%s'", runTerm),
		})
	}

	zap.L().Info("dynamic artlist association completed",
		zap.String("subject", segmentAssociationSubject(segment)),
		zap.String("term", runTerm),
		zap.Int("matches", len(matches)),
		zap.Duration("elapsed", time.Since(startedAt)),
	)

	return matches, nil
}
