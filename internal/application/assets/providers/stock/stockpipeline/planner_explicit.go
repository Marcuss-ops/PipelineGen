package stockpipeline

import (
	"context"
	"strconv"
)

type explicitPlanner struct {
	clips []ClipSpec
}

var _ ClipPlanner = (*explicitPlanner)(nil)

func (p *explicitPlanner) Plan(_ context.Context, src VideoSource, budgetSec int, clipDur int, policyVer string) ([]ClipPlan, error) {
	if len(p.clips) == 0 {
		return nil, ErrExplicitPlannerNoClips
	}
	plans := make([]ClipPlan, 0, len(p.clips))
	for i, clip := range p.clips {
		// godlike/06 SSOT — route through buildClipPlan so the
		// inference + ID derivation stay canonical (PR-003 only
		// added fields; the literal was deliberately modified here
		// to share buildClipPlan rather than duplicate the
		// 11-field struct literal — future field additions stay
		// in one place).
		plan := buildClipPlan(src, clip.StartSec, clip.EndSec, i, policyVer)
		plan.Title = clip.Title
		plan.Description = clip.Description
		// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): thread the 4
		// new content fields from ClipSpec → ClipPlan so downstream
		// consumers (ChunkState, ChunkMetadataEntry, perClipLeafName)
		// see them verbatim. godlike/06 SSOT one canonical owner per
		// fact: explicitPlanner is the SOLE place that copies
		// operator-supplied clip metadata into the plan shape.
		plan.Round = clip.Round
		plan.Tags = append([]string(nil), clip.Tags...) // defensive copy so the plan doesn't share the caller's slice
		plan.Category = clip.Category
		plan.Slug = clip.Slug
		// PR-STOCK-TIMESTAMP-CLIPS (July 2026): thread ParentSlug so
		// timestampParentLeafName uses the correct parent identity for
		// expanded 5-second children. Without this, children whose
		// Title is empty would fall through to their child-specific
		// Slug (e.g. "la-fase-di-studio-0-0-0_to_0-0-5") instead of
		// sharing the parent folder name.
		plan.ParentSlug = clip.ParentSlug
		plans = append(plans, plan)
	}
	return plans, nil
}

// expandExplicitClipSpecs splits explicit clips into successive
// fixed-length slices when secondsPerSegment is positive.
//
// Backward-compat fallback: when secondsPerSegment is zero, clips
// whose duration is at least 60 seconds are still expanded into
// 5-second slices. Shorter explicit clips remain single chunks.
func expandExplicitClipSpecs(clips []ClipSpec, secondsPerSegment int) []ClipSpec {
	if len(clips) == 0 {
		return append([]ClipSpec(nil), clips...)
	}
	expanded := make([]ClipSpec, 0, len(clips))
	for _, clip := range clips {
		stepSec := secondsPerSegment
		if stepSec <= 0 && clip.EndSec > clip.StartSec {
			if clip.EndSec-clip.StartSec >= explicitClipAutoSegmentThresholdSec {
				stepSec = explicitClipAutoSegmentSeconds
			}
		}
		if stepSec <= 0 || clip.EndSec <= clip.StartSec {
			expanded = append(expanded, clip)
			continue
		}
		step := float64(stepSec)
		for cursor := clip.StartSec; cursor < clip.EndSec; cursor += step {
			next := cursor + step
			if next > clip.EndSec {
				next = clip.EndSec
			}
			child := clip
			child.ParentSlug = clip.ParentSlug
			if child.ParentSlug == "" {
				child.ParentSlug = clip.Slug
			}
			child.StartSec = cursor
			child.EndSec = next
			if clip.Slug != "" {
				child.Slug = clip.Slug + "-" + formatTimestampForSlug(cursor, next)
			}
			expanded = append(expanded, child)
		}
	}
	return expanded
}

func formatTimestampForSlug(startSec, endSec float64) string {
	return formatTimestampSeconds(startSec) + "_to_" + formatTimestampSeconds(endSec)
}

func formatTimestampSeconds(sec float64) string {
	total := int(sec)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return strconv.Itoa(h) + "-" + strconv.Itoa(m) + "-" + strconv.Itoa(s)
}
