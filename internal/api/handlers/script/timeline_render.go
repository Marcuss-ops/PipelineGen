package script

import (
	"fmt"
	"strings"
)

// RenderTimeline converts a TimelinePlan into the final formatted text section.
func RenderTimeline(plan *TimelinePlan) string {
	if plan == nil || len(plan.Segments) == 0 {
		return "⏱️ Timeline unavailable."
	}

	var b strings.Builder

	for i := 0; i < len(plan.Segments); {
		j := i + 1
		for j < len(plan.Segments) && canGroup(plan.Segments[i], plan.Segments[j]) {
			j++
		}

		if j-i > 1 {
			b.WriteString(renderGroupedSegments(plan.Segments[i:j]))
		} else {
			seg := plan.Segments[i]
			b.WriteString(renderSegmentHeader(seg))
		}

		if j < len(plan.Segments) {
			b.WriteString("\n")
		}
		i = j
	}

	return strings.TrimSpace(b.String())
}

func canGroup(s1, s2 TimelineSegment) bool {
	if NormalizeRepeatedSubject(s1.Subject) != NormalizeRepeatedSubject(s2.Subject) {
		return false
	}
	// Compare primary asset link
	return getPrimaryLink(s1) == getPrimaryLink(s2)
}

func getPrimaryLink(seg TimelineSegment) string {
	if len(seg.StockMatches) > 0 && hasStrongMatch(seg.StockMatches, 35) {
		return seg.StockMatches[0].Link
	}
	if len(seg.ArtlistMatches) > 0 && hasStrongMatch(seg.ArtlistMatches, 35) {
		return seg.ArtlistMatches[0].Link
	}
	return ""
}

func renderGroupedSegments(segments []TimelineSegment) string {
	var b strings.Builder
	first := segments[0]
	last := segments[len(segments)-1]

	// Range Header
	b.WriteString(fmt.Sprintf("[%d-%d]\n", int(first.StartTime), int(last.EndTime)))

	// List individual parts concisely
	for _, seg := range segments {
		b.WriteString(fmt.Sprintf("   [%s] Part %d\n", seg.Timestamp, seg.Index))
	}

	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("   Subject: %s\n", NormalizeRepeatedSubject(first.Subject)))
	b.WriteString(renderSegmentPrimaryAssociation(first))

	return b.String()
}
