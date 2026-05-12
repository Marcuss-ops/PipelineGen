package script

import (
	"sort"
	"strings"
)

func normalizeTimelineLLMPlan(plan *timelineLLMPlan, duration int) *timelineLLMPlan {
	if plan == nil || len(plan.Segments) == 0 || duration <= 0 {
		return nil
	}

	segments := make([]timelineLLMSegment, 0, len(plan.Segments))
	for _, seg := range plan.Segments {
		if strings.TrimSpace(seg.NarrativeText) == "" && strings.TrimSpace(seg.OpeningSentence) == "" && strings.TrimSpace(seg.ClosingSentence) == "" {
			continue
		}
		if seg.EndTime <= seg.StartTime {
			continue
		}
		segments = append(segments, seg)
	}
	if len(segments) == 0 {
		return nil
	}

	minSegments := 2
	if duration >= 60 {
		minSegments = 4
	}
	if duration >= 300 {
		minSegments = 8
	}
	if len(segments) < minSegments {
		return nil
	}

	sort.SliceStable(segments, func(i, j int) bool {
		if segments[i].StartTime == segments[j].StartTime {
			if segments[i].EndTime == segments[j].EndTime {
				return segments[i].Index < segments[j].Index
			}
			return segments[i].EndTime < segments[j].EndTime
		}
		return segments[i].StartTime < segments[j].StartTime
	})

	dur := float64(duration)
	if segments[0].StartTime < 0 {
		segments[0].StartTime = 0
	}
	if segments[0].StartTime > 0 {
		segments[0].StartTime = 0
	}

	prevEnd := 0.0
	for i := range segments {
		if i == 0 {
			segments[i].StartTime = 0
		} else if segments[i].StartTime < prevEnd {
			segments[i].StartTime = prevEnd
		}

		if segments[i].StartTime > dur {
			segments[i].StartTime = dur
		}
		if segments[i].EndTime > dur {
			segments[i].EndTime = dur
		}

		if i == len(segments)-1 {
			segments[i].EndTime = dur
		}
		if segments[i].EndTime <= segments[i].StartTime {
			return nil
		}
		prevEnd = segments[i].EndTime
	}

	for i := range segments {
		segments[i].Index = i + 1
		segments[i].StartTime = roundSeconds(segments[i].StartTime)
		segments[i].EndTime = roundSeconds(segments[i].EndTime)
		if i == 0 {
			segments[i].StartTime = 0
		}
		if i == len(segments)-1 {
			segments[i].EndTime = dur
		}
	}

	plan.Segments = segments
	if strings.TrimSpace(plan.PrimaryFocus) == "" {
		plan.PrimaryFocus = "Timeline"
	}
	return plan
}
