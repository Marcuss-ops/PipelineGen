package script

import (
	"math"

	"velox/go-master/internal/pkg/textutil"
)

func roundSeconds(f float64) float64 {
	return math.Round(f*100) / 100
}

func segmentAssociationSubject(segment *TimelineSegment) string {
	if segment == nil {
		return ""
	}
	return textutil.FirstNonEmpty(segment.CanonicalSubject, segment.Subject)
}

func segmentAssociationKeywords(segment *TimelineSegment) []string {
	if segment == nil {
		return nil
	}
	if len(segment.CanonicalKeywords) > 0 {
		return segment.CanonicalKeywords
	}
	return segment.Keywords
}

func segmentAssociationEntities(segment *TimelineSegment) []string {
	if segment == nil {
		return nil
	}
	if len(segment.CanonicalEntities) > 0 {
		return segment.CanonicalEntities
	}
	return segment.Entities
}
