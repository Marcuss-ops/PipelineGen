package mediacert

import (
	"fmt"
	"strings"

	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ruleImageFanout certifies the image target policy. Query counts are
// observations, not gates: providers return different result counts and one
// entity may legitimately use several queries. The gate is the explicit
// image target: when a plan asks for at least one image per segment, a
// non-stock segment with no candidate fails, and a job with zero images can
// never be certified. Stock-bound scenes remain governed by the separate
// STOCK ISOLATION rule.
func ruleImageFanout(spec Spec, result MediaResult) CheckResult {
	if spec.ImagesPerSegment <= 0 {
		return CheckResult{
			Name:       CheckImageFanout,
			Skipped:    true,
			SkipReason: "no image requirement",
		}
	}
	expected := segmentByID(spec)
	total, pass := 0, 0
	var violations []Violation
	for _, seg := range result.Segments {
		if exp, ok := expected[seg.SegmentID]; ok && exp.StockBound {
			continue
		}
		total++
		if violation := CheckSegmentImageFanout(spec, seg); violation != nil {
			violations = append(violations, *violation)
			continue
		}
		pass++
	}
	if total == 0 {
		return passBool(CheckImageFanout, spec.ImagesPerSegment <= 0)
	}
	return passCount(CheckImageFanout, pass, total, violations...)
}

// CheckSegmentImageFanout reports the IMAGE FANOUT violation for one segment,
// or nil when the segment satisfies the run's image target. It is the single
// source of truth for both terminal certification and the producer's per-scene
// fail-fast gate: a scene with zero usable images is rejected as soon as it
// is materialized, instead of after the whole run has paid for TTS and render.
func CheckSegmentImageFanout(spec Spec, segment ResultSegment) *Violation {
	if spec.ImagesPerSegment <= 0 {
		return nil
	}
	if expected, ok := segmentByID(spec)[segment.SegmentID]; ok && expected.StockBound {
		return nil
	}
	if availableImageCount(segment.Assets) > 0 {
		return nil
	}
	return &Violation{
		SegmentID: segment.SegmentID,
		Rule:      string(CheckImageFanout),
		Detail:    fmt.Sprintf("images available = 0, target at least %d", spec.ImagesPerSegment),
	}
}

// availableImageCount counts distinct, usable image assets. Candidates are
// also mirrored in SecondaryImages/GeneratedImages, so summing slice lengths
// counted the same provider result multiple times. The broad candidate pool
// may also contain video candidates; only image providers or image MIME types
// count there.
func availableImageCount(assets script.SegmentAssetSelection) int {
	seen := make(map[string]struct{})
	count := 0
	add := func(candidate script.SegmentAssetCandidate, imageBySlot bool) {
		if candidate.LifecycleFailed() {
			return
		}
		assetID := strings.TrimSpace(candidate.AssetID)
		if assetID == "" {
			return
		}
		if !imageBySlot {
			provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
			mimeType := strings.ToLower(strings.TrimSpace(candidate.MIMEType))
			if provider != script.VidRushProviderInternetImages &&
				provider != script.VidRushProviderImageGeneration &&
				!strings.HasPrefix(mimeType, "image/") {
				return
			}
		}
		key := strings.TrimSpace(candidate.ContentSHA256)
		if key == "" {
			key = strings.TrimSpace(candidate.SourceURL)
		}
		if key == "" {
			key = assetID
		}
		key = strings.ToLower(key)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		count++
	}
	for _, candidate := range assets.SecondaryImages {
		add(candidate, true)
	}
	for _, candidate := range assets.GeneratedImages {
		add(candidate, true)
	}
	for _, candidate := range assets.Candidates {
		add(candidate, false)
	}
	return count
}

// ruleStockIsolation certifies the clip/stock separation positively: a
// segment declared stock_bound in the spec (a caller stock folder binding)
// must carry NO provider video winner and NO provider image candidates.
// Its media comes from the binding, never from discovery. Segments not
// declared stock_bound are not constrained by this rule.
func ruleStockIsolation(spec Spec, result MediaResult) CheckResult {
	expected := segmentByID(spec)
	total, pass := 0, 0
	var violations []Violation
	for _, seg := range result.Segments {
		exp, ok := expected[seg.SegmentID]
		if !ok || !exp.StockBound {
			continue
		}
		total++
		if winner := winnerOf(seg.Assets); winner != nil {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckStockIsolation),
				Detail:    fmt.Sprintf("stock-bound segment carries provider winner asset %q (provider %q)", winner.AssetID, winner.Provider),
			})
		}
		if n := len(seg.Assets.SecondaryImages) + len(seg.Assets.GeneratedImages); n > 0 {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckStockIsolation),
				Detail:    fmt.Sprintf("stock-bound segment carries %d provider image selection(s)", n),
			})
		}
		for _, candidate := range seg.Assets.Candidates {
			if stockBoundEntityOverlayCandidate(seg, candidate) ||
				(exp.AllowEntityImageCandidates && strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) && strings.TrimSpace(candidate.Entity) != "") {
				continue
			}
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckStockIsolation),
				Detail:    fmt.Sprintf("stock-bound segment carries provider candidate %q outside an entity overlay (provider=%q entity=%q entity_id=%q query=%q)", candidate.AssetID, candidate.Provider, candidate.Entity, candidate.EntityID, candidate.Query),
			})
		}
		if allForSegment(violations, seg.SegmentID) == 0 {
			pass++
		}
	}
	if total == 0 {
		return passBool(CheckStockIsolation, true)
	}
	return passCount(CheckStockIsolation, pass, total, violations...)
}

// Entity overlay candidates are a separate surface from scene media: they
// can bind a portrait to an extracted entity card on a stock-backed scene.
// Accept only internet-image candidates whose entity name is present among
// the segment's imageable semantic entities; all other provider candidates
// remain a stock-isolation violation.
func stockBoundEntityOverlayCandidate(seg ResultSegment, candidate scriptpkg.SegmentAssetCandidate) bool {
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	if provider != scriptpkg.VidRushProviderInternetImages && provider != "media_manifest" {
		return false
	}
	entityName := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(candidate.Entity)), " "))
	query := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(candidate.Query)), " "))
	if entityName == "" && query == "" {
		return false
	}
	if candidate.EntityID != "" {
		for _, canonicalID := range seg.Insights.ImageEntityCanonicalIDs {
			if strings.EqualFold(strings.TrimSpace(candidate.EntityID), strings.TrimSpace(canonicalID)) {
				return true
			}
		}
		for _, link := range seg.Insights.EntityMediaLinks {
			if strings.EqualFold(strings.TrimSpace(candidate.EntityID), strings.TrimSpace(link.CanonicalEntityID)) {
				return true
			}
		}
	}
	for _, entity := range seg.Insights.Entities {
		if !scriptpkg.IsAnnotationEntityKind(scriptpkg.NormalizeAnnotationType(entity.Type)) {
			continue
		}
		entityID := strings.TrimSpace(candidate.EntityID)
		if entityID != "" && (strings.EqualFold(entityID, strings.TrimSpace(entity.IdentityID)) || strings.EqualFold(entityID, strings.TrimSpace(entity.CanonicalID))) {
			return true
		}
		got := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(entity.Value)), " "))
		if got == entityName && entityName != "" || got == query && query != "" ||
			got != "" && (strings.Contains(query, got) || strings.Contains(entityName, got)) {
			return true
		}
		for _, intent := range entity.SearchIntents {
			intent = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(intent)), " "))
			if intent != "" && (query == intent || strings.Contains(query, intent)) {
				return true
			}
		}
	}
	return false
}

// allForSegment counts how many violations already belong to a segment.
func allForSegment(violations []Violation, segID string) int {
	n := 0
	for _, v := range violations {
		if v.SegmentID == segID {
			n++
		}
	}
	return n
}
