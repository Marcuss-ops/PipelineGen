package script

import (
	"fmt"
	"strings"

	"velox/go-master/pkg/textutil"
)

// renderSegmentHeader renders the segment header with timestamp and subject
func renderSegmentHeader(seg TimelineSegment) string {
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(seg.Timestamp)
	b.WriteString("]\n")

	if seg.Subject != "" {
		b.WriteString(fmt.Sprintf("   Subject: %s\n", seg.Subject))
	}

	if strings.TrimSpace(seg.OpeningSentence) != "" {
		b.WriteString("   Start: ")
		b.WriteString(textutil.Truncate(seg.OpeningSentence, 80))
		b.WriteString("\n")
	}
	if strings.TrimSpace(seg.ClosingSentence) != "" {
		b.WriteString("   End:   ")
		b.WriteString(textutil.Truncate(seg.ClosingSentence, 80))
		b.WriteString("\n")
	}

	return b.String()
}
