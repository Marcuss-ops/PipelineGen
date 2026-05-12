package script

import (
	"context"
	"strings"

	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/visualquery"
	"velox/go-master/pkg/sliceutil"
)

func associateSegment(ctx context.Context, seg *TimelineSegment, assocService *association.Service, topic string) {
	if assocService == nil {
		return
	}

	input := association.SegmentInput{
		Topic:     topic,
		Subject:   segmentAssociationSubject(seg),
		Keywords:  segmentAssociationKeywords(seg),
		Entities:  segmentAssociationEntities(seg),
		Narrative: seg.NarrativeText,
	}

	// 1. Try preferred stock match first (e.g. "Mike Tyson" folder)
	if preferred, ok := assocService.ResolvePreferredStockMatch(ctx, input); ok {
		seg.StockMatches = append(seg.StockMatches, *preferred)
		// If we found a direct match, we still might want Artlist clips for variety,
		// but the preferred one is now first and scored high (1000).
	}

	// 2. Run general association engine
	matches := assocService.Associate(ctx, input)
	for _, m := range matches {
		switch m.Source {
		case "drive_stock", "stock_folder", "clip_drive":
			seg.StockMatches = append(seg.StockMatches, m)
		case "artlist_folder", "artlist_stock", "artlist_dynamic", "artlist_clip":
			seg.ArtlistMatches = append(seg.ArtlistMatches, m)
		default:
			// Ignore unrecognized sources
		}
	}
}

func injectPreferredAssociation(seg *TimelineSegment) {
	if seg == nil {
		return
	}
	// If we already have strong matches, don't inject from preferred
	if len(seg.StockMatches) > 0 || len(seg.ArtlistMatches) > 0 {
		return
	}
	if strings.TrimSpace(seg.PreferredStockGroup) == "" || len(seg.PreferredStockPaths) == 0 {
		return
	}

	title := visualquery.FirstNonEmpty(seg.CanonicalSubject, seg.Subject, "Asset")
	link := ""
	path := ""
	if len(seg.PreferredStockPaths) > 0 {
		path = strings.TrimSpace(seg.PreferredStockPaths[0])
	}
	if len(seg.PreferredStockPaths) > 1 {
		link = strings.TrimSpace(seg.PreferredStockPaths[1])
	}
	if link == "" && strings.HasPrefix(strings.ToLower(path), "http") {
		link = path
		path = ""
	}

	match := association.ScoredMatch{
		Title:   title,
		Path:    path,
		Score:   80,
		Link:    link,
		Details: seg.PreferredStockReason,
	}

	switch strings.ToLower(strings.TrimSpace(seg.PreferredStockGroup)) {
	case "stock_folder", "stock_drive":
		match.Source = "drive_stock"
		seg.StockMatches = append(seg.StockMatches, match)
	case "artlist_folder":
		match.Source = string(timelineAssetSourceArtlistFolder)
		seg.ArtlistMatches = append(seg.ArtlistMatches, match)
	}
}

func applyAssociationHints(seg *TimelineSegment, resp *association.CandidatesResponse) {
	if seg == nil || resp == nil || len(resp.Candidates) == 0 {
		return
	}
	best := resp.Candidates[0]
	seg.PreferredStockReason = best.Reason
	seg.PreferredStockGroup = best.Source
	preferredLink := association.NormalizeDriveFolderLink(best.Link, best.FolderID)
	seg.PreferredStockPaths = sliceutil.UniqueStrings(sliceutil.TrimStrings([]string{best.Path, preferredLink}))
}

// firstNonEmpty is defined in artlist_query_generator.go
