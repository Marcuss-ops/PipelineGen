package scriptdocs

import (
	"context"
	"math"
	"strings"
)

func (s *ScriptDocService) buildMixedSegments(ctx context.Context, topic string, chapters []ScriptChapter, stockFolder StockFolder, entityImages map[string]string) []MixedSegment {
	if len(chapters) == 0 {
		return nil
	}

	seenURLs := make(map[string]bool)
	segments := make([]MixedSegment, 0, len(chapters))

	for _, chapter := range chapters {
		phrases := SelectImportantPhrases(chapter.SourceText, topic, 2)
		if len(phrases) == 0 {
			phrases = []string{compactSnippet(chapter.SourceText, 120)}
		}

		clipAssocs := s.associateClips(phrases, stockFolder, topic)
		clipChoice := pickBestClipAssociation(clipAssocs)

		imageAssocs := s.buildImageAssociationsForChapter(ctx, topic, chapter, entityImages, seenURLs)
		imageChoice := pickBestImageAssociation(imageAssocs)

		sourceKind := "clip"
		reason := "clip association selected"
		confidence := 0.0
		var clipCopy *ClipAssociation
		var imageCopy *ImageAssociation
		trace := newAssetResolution("scriptdocs.mixed", "clip", "image", "jit-stock")
		trace.RequestKey = normalizeKeyword(topic) + "|" + normalizeKeyword(firstNonEmpty(phrases...))

		clipScore := 0.0
		if clipChoice != nil {
			clipScore = clipChoice.Confidence
		}
		imageScore := 0.0
		if imageChoice != nil {
			imageScore = imageChoice.Score
		}

		switch {
		case imageChoice != nil && (clipChoice == nil || imageScore >= clipScore+0.12 || imageScore >= 1.20 || chapterLooksHighlyVisual(chapter)):
			sourceKind = "image"
			reason = "image relevance outranked clip candidates"
			confidence = imageChoice.Score
			copy := *imageChoice
			imageCopy = &copy
			trace.withOutcome("image", reason, imageCopy.Cached).addNote("image score outranked clip score")
			if imageCopy.Resolution != nil {
				trace.Notes = append(trace.Notes, "image resolver: "+strings.TrimSpace(imageCopy.Resolution.SelectedFrom))
			}
		case clipChoice != nil && clipScore >= 0.65:
			sourceKind = clipChoice.Type
			reason = "clip relevance outranked images"
			confidence = clipChoice.Confidence
			copy := *clipChoice
			clipCopy = &copy
			selectedFrom := strings.ToLower(strings.TrimSpace(clipChoice.Type))
			if selectedFrom == "" {
				selectedFrom = "clip"
			}
			trace.withOutcome(selectedFrom, reason, false)
			if clipCopy.Resolution != nil {
				trace.Notes = append(trace.Notes, "clip resolver: "+strings.TrimSpace(clipCopy.Resolution.SelectedFrom))
			}
		case imageChoice != nil:
			sourceKind = "image"
			reason = "fallback to image because no strong clip matched"
			confidence = imageChoice.Score
			copy := *imageChoice
			imageCopy = &copy
			trace.withOutcome("image", reason, imageCopy.Cached)
		case clipChoice != nil:
			sourceKind = clipChoice.Type
			reason = "fallback to clip because no image matched"
			confidence = clipChoice.Confidence
			copy := *clipChoice
			clipCopy = &copy
			selectedFrom := strings.ToLower(strings.TrimSpace(clipChoice.Type))
			if selectedFrom == "" {
				selectedFrom = "clip"
			}
			trace.withOutcome(selectedFrom, reason, false)
		default:
			continue
		}

		if clipCopy == nil && s.jitResolver != nil && s.allowJITFallback() {
			if jitRes := s.resolveJITClip(ctx, topic, firstNonEmpty(phrases...), chapter.StartTime, chapter.EndTime); jitRes != nil {
				clipAssoc := s.jitResultToAssociation(jitRes)
				if clipAssoc != nil {
					sourceKind = strings.ToLower(strings.TrimSpace(jitRes.SourceKind))
					if sourceKind == "" {
						sourceKind = "clip"
					}
					reason = "jit stock fallback selected"
					confidence = jitRes.Confidence
					clipCopy = clipAssoc
					trace.withOutcome("jit-stock", reason, jitRes.Cached)
					trace.RequestKey = strings.TrimSpace(jitRes.RequestID)
					if clipCopy.Resolution != nil {
						trace.Notes = append(trace.Notes, "jit resolver: "+strings.TrimSpace(clipCopy.Resolution.SelectedFrom))
					}
				}
			}
		}

		if clipCopy != nil && strings.TrimSpace(clipCopy.Type) == "ARTLIST" {
			sourceKind = "artlist"
		}
		if clipCopy != nil && strings.TrimSpace(clipCopy.Type) == "DYNAMIC" {
			sourceKind = "clip"
		}
		if clipCopy != nil && strings.TrimSpace(clipCopy.Type) == "STOCK_DB" {
			sourceKind = "clip"
		}

		segments = append(segments, MixedSegment{
			ChapterIndex: chapter.Index + 1,
			StartTime:    chapter.StartTime,
			EndTime:      chapter.EndTime,
			Phrase:       compactSnippet(firstNonEmpty(phrases...), 180),
			SourceKind:   sourceKind,
			Reason:       reason,
			Confidence:   round2(confidence),
			Clip:         clipCopy,
			Image:        imageCopy,
			Resolution:   cloneAssetResolution(trace),
		})
	}

	return segments
}

func pickBestClipAssociation(assocs []ClipAssociation) *ClipAssociation {
	if len(assocs) == 0 {
		return nil
	}
	best := assocs[0]
	for _, assoc := range assocs[1:] {
		if assoc.Confidence > best.Confidence {
			best = assoc
		}
	}
	copy := best
	return &copy
}

func pickBestImageAssociation(assocs []ImageAssociation) *ImageAssociation {
	if len(assocs) == 0 {
		return nil
	}
	best := assocs[0]
	for _, assoc := range assocs[1:] {
		if assoc.Score > best.Score {
			best = assoc
		}
	}
	copy := best
	return &copy
}

func chapterLooksHighlyVisual(chapter ScriptChapter) bool {
	blob := strings.ToLower(chapter.Title + " " + chapter.SourceText)
	visualTerms := []string{"visual", "image", "scenery", "landscape", "scene", "close-up", "close up", "mountain", "lake", "river", "wildlife", "skyline"}
	for _, term := range visualTerms {
		if strings.Contains(blob, term) {
			return true
		}
	}
	return false
}

func firstNonEmpty(items ...string) string {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return item
		}
	}
	return ""
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
