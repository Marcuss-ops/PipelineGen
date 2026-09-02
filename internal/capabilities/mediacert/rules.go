// Package mediacert — rules.go implements the individual certification
// rules. Each rule is a pure function (Spec, MediaResult) -> CheckResult
// so rules can be unit-tested in isolation and composed by the certifier
// without hidden coupling.
//
// The rules deliberately mirror the LIVE-test failures they prevent:
//
//   - ruleSceneIdentity         — mediterranean-* must not become scene-N.
//   - ruleSourceImmutability    — Get ready to dive... must not overwrite
//     source_text; the stamped source_text_hash must still match a fresh
//     hash of the current source_text.
//   - ruleSemanticProfiles      — visual_profile must not be null 5/5.
//   - ruleArtlistRelevance      — boxing REJECTED for Greek Salad.
//   - ruleEntityGrounding       — every entity has source evidence;
//     Imagine the / ready never become entities.
//   - ruleImageFanout           — one image query per entity, N images
//     per scene.
//   - ruleQueryOwnership        — a query's owner_segment_id matches the
//     segment that emitted it.
//   - ruleAssetOwnership        — a selected asset's provenance segment
//     matches the segment it is bound to.
//   - ruleCrossSceneReuse       — when the spec forbids reuse, no asset
//     is bound to two segments.
//   - ruleProviderPolicy        — only spec-allowed video providers used.
//   - ruleCrossContamination    — umbrella check: 0 query/asset drift.
package mediacert

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/sceneir"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Rule is one certification check. Rules are pure: the certifier applies
// them in order and folds their CheckResults into the final Report.
type Rule func(spec Spec, result MediaResult) CheckResult

// AllRules is the canonical ordered rule set the certifier applies. Order
// matters for the human-readable report: identity → immutability → profile
// → relevance → grounding → fanout → ownerships → reuse → policy →
// contamination. A failure in an earlier rule does NOT short-circuit the
// later ones: the operator sees every defect in one run.
func AllRules() []Rule {
	return []Rule{
		ruleSceneIdentity,
		ruleSourceImmutability,
		ruleSemanticProfiles,
		ruleArtlistRelevance,
		ruleEntityGrounding,
		ruleImageFanout,
		ruleQueryOwnership,
		ruleAssetOwnership,
		ruleCrossSceneReuse,
		ruleProviderPolicy,
		ruleCrossContamination,
	}
}

// passBool renders a boolean check as a 1/1 CheckResult.
func passBool(name CheckName, passed bool, violations ...Violation) CheckResult {
	total, p := 1, 0
	if passed {
		p = 1
	}
	return CheckResult{Name: name, Passed: passed, PassCount: p, TotalCount: total, Violations: orEmpty(violations)}
}

// passCount renders an X/Y check. Passed is true only when pass==total.
func passCount(name CheckName, pass, total int, violations ...Violation) CheckResult {
	return CheckResult{
		Name:       name,
		Passed:     total > 0 && pass == total,
		PassCount:  pass,
		TotalCount: total,
		Violations: orEmpty(violations),
	}
}

func orEmpty(v []Violation) []Violation {
	if len(v) == 0 {
		return nil
	}
	return v
}

// segmentByID indexes the spec's expected segments by their canonical ID.
func segmentByID(spec Spec) map[string]SpecSegment {
	m := make(map[string]SpecSegment, len(spec.SegmentsExpected))
	for _, s := range spec.SegmentsExpected {
		m[s.ID] = s
	}
	return m
}

// ruleSceneIdentity verifies every result segment's SegmentID matches a
// spec-expected ID, and that the order matches. This is the rule that
// rejects the mediterranean-* → scene-N rewrite.
func ruleSceneIdentity(spec Spec, result MediaResult) CheckResult {
	expected := segmentByID(spec)
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for i, seg := range result.Segments {
		exp, ok := expected[seg.SegmentID]
		if !ok {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckSceneIdentity),
				Detail:    fmt.Sprintf("segment_id %q not in spec expected ids", seg.SegmentID),
			})
			continue
		}
		if i >= len(spec.SegmentsExpected) || spec.SegmentsExpected[i].ID != seg.SegmentID {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckSceneIdentity),
				Detail:    fmt.Sprintf("segment_id %q is at position %d but expected %q at that position", seg.SegmentID, i, exp.ID),
			})
			continue
		}
		pass++
	}
	return passCount(CheckSceneIdentity, pass, total, violations...)
}

// ruleSourceImmutability verifies the stamped source_text_hash still
// matches a fresh hash of the current source_text, and (when a SceneIR is
// attached) that the SceneIR's identity snapshot has not been tampered
// with. This rejects the "Get ready to dive..." overwrite of source_text.
func ruleSourceImmutability(spec Spec, result MediaResult) CheckResult {
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for _, seg := range result.Segments {
		if seg.SceneIR != nil {
			if seg.SceneIR.SourceTextTampered() {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckSourceImmutability),
					Detail:    "source_text_hash does not match a fresh hash of source_text (tampered source)",
				})
				continue
			}
			if seg.SceneIR.SourceText != seg.SourceText {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckSourceImmutability),
					Detail:    "result.source_text does not match scene_ir.source_text (identity drift)",
				})
				continue
			}
		} else if seg.SourceText != "" && seg.SourceTextHash != "" {
			fresh := script.ComputeCanonicalSegmentTextHash(seg.SourceText)
			if fresh != seg.SourceTextHash {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckSourceImmutability),
					Detail:    "source_text_hash does not match a fresh hash of source_text",
				})
				continue
			}
		}
		pass++
	}
	return passCount(CheckSourceImmutability, pass, total, violations...)
}

// ruleSemanticProfiles verifies every segment has a non-null visual
// profile: a non-empty subject and at least one visual term. This is the
// rule that rejects the visual_profile=null 5/5 LIVE-test failure.
//
// A profile is considered present when EITHER the attached SceneIR has a
// non-empty compact profile OR the legacy Insights.VisualProfile is
// non-nil with a non-empty subject + terms.
func ruleSemanticProfiles(spec Spec, result MediaResult) CheckResult {
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for _, seg := range result.Segments {
		if !segmentHasProfile(seg) {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckSemanticProfiles),
				Detail:    "visual_profile is null (missing subject or visual terms)",
			})
			continue
		}
		pass++
	}
	return passCount(CheckSemanticProfiles, pass, total, violations...)
}

// segmentHasProfile reports whether a segment carries a non-null visual
// profile through either the SceneIR canonical profile or the legacy
// Insights.VisualProfile.
func segmentHasProfile(seg ResultSegment) bool {
	if seg.SemanticProfile != nil {
		vp := script.BuildSegmentVisualProfile(*seg.SemanticProfile)
		if strings.TrimSpace(vp.Subject) != "" && len(vp.Terms) > 0 {
			return true
		}
	}
	if seg.SceneIR != nil {
		vp := script.BuildSegmentVisualProfile(seg.SceneIR.Profile)
		if vp.Subject != "" && len(vp.Terms) > 0 {
			return true
		}
	}
	if seg.Insights.VisualProfile != nil {
		vp := seg.Insights.VisualProfile
		if strings.TrimSpace(vp.Subject) != "" && len(vp.Terms) > 0 {
			return true
		}
	}
	return false
}

// ruleArtlistRelevance verifies each segment's winner asset's inferred
// subject is compatible with the spec subject. This is the rule that
// rejects a boxing clip bound to a Greek Salad segment.
func ruleArtlistRelevance(spec Spec, result MediaResult) CheckResult {
	// No video provider in the spec means this is an image/preset (or
	// semantic-only) run. Absence of a video winner is then valid; relevance
	// is enforced only when the plan explicitly requested a video provider.
	if strings.TrimSpace(spec.VideoProvider) == "" {
		return passBool(CheckArtlistRelevance, true)
	}
	expected := segmentByID(spec)
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for _, seg := range result.Segments {
		winner := winnerOf(seg.Assets)
		if winner == nil {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckArtlistRelevance),
				Detail:    "no winner asset bound to the segment",
			})
			continue
		}
		exp, ok := expected[seg.SegmentID]
		if !ok {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckArtlistRelevance),
				Detail:    "segment_id not in spec expected ids",
			})
			continue
		}
		want := strings.TrimSpace(exp.WinnerSubjectMatch)
		if want == "" {
			want = strings.TrimSpace(exp.Subject)
		}
		if want == "" {
			pass++
			continue
		}
		if !subjectCompatible(winner, want, seg) {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckArtlistRelevance),
				Detail:    fmt.Sprintf("winner subject mismatch: winner %q is not compatible with expected subject %q", winnerEntityLabel(winner), want),
			})
			continue
		}
		pass++
	}
	return passCount(CheckArtlistRelevance, pass, total, violations...)
}

// winnerOf returns the primary selected asset (the winner) for a segment.
func winnerOf(sel script.SegmentAssetSelection) *script.SegmentAssetCandidate {
	if sel.PrimaryVideo != nil {
		return sel.PrimaryVideo
	}
	if len(sel.SecondaryImages) > 0 {
		return &sel.SecondaryImages[0]
	}
	return nil
}

// winnerEntityLabel returns a human-facing label for the winner.
func winnerEntityLabel(c *script.SegmentAssetCandidate) string {
	if c == nil {
		return "<nil>"
	}
	if v := strings.TrimSpace(c.Entity); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Query); v != "" {
		return v
	}
	return c.AssetID
}

// subjectCompatible reports whether the winner asset is semantically
// compatible with the expected subject.
func subjectCompatible(c *script.SegmentAssetCandidate, wantSubject string, seg ResultSegment) bool {
	if c == nil {
		return false
	}
	hay := strings.ToLower(winnerEntityLabel(c) + " " + c.SelectionReason)
	want := strings.ToLower(strings.TrimSpace(wantSubject))
	if want == "" {
		return true
	}
	if strings.Contains(hay, want) {
		return true
	}
	for _, concept := range requiredConceptsFor(seg) {
		if strings.Contains(hay, strings.ToLower(concept)) {
			return true
		}
	}
	if strings.TrimSpace(winnerEntityLabel(c)) == "" {
		return true
	}
	return false
}

// requiredConceptsFor returns the spec required concepts for a segment,
// falling back to the SceneIR profile visual terms.
func requiredConceptsFor(seg ResultSegment) []string {
	if seg.SceneIR != nil {
		visual := script.BuildSegmentVisualProfile(seg.SceneIR.Profile)
		return visual.Terms
	}
	return nil
}

// ruleEntityGrounding verifies every segment has the spec-required number
// of entities AND every entity has source evidence.
func ruleEntityGrounding(spec Spec, result MediaResult) CheckResult {
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for _, seg := range result.Segments {
		ents := seg.Insights.Entities
		if spec.EntitiesPerSegment > 0 && len(ents) != spec.EntitiesPerSegment {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckEntityGrounding),
				Detail:    fmt.Sprintf("entity count = %d, expected %d", len(ents), spec.EntitiesPerSegment),
			})
			continue
		}
		grounded := 0
		for _, ent := range ents {
			if entityHasEvidence(ent, seg) {
				grounded++
			} else {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckEntityGrounding),
					Detail:    fmt.Sprintf("entity %q has no source evidence (NO EVIDENCE → NO ENTITY)", ent.Value),
				})
			}
		}
		if grounded == len(ents) {
			pass++
		}
	}
	return passCount(CheckEntityGrounding, pass, total, violations...)
}

// entityHasEvidence reports whether an entity's value appears as a
// substring of the segment's source text.
func entityHasEvidence(ent script.ExtractedEntity, seg ResultSegment) bool {
	needle := strings.ToLower(strings.TrimSpace(ent.Value))
	if needle == "" {
		return false
	}
	hay := strings.ToLower(seg.SourceText)
	if seg.SceneIR != nil {
		hay = strings.ToLower(seg.SceneIR.SourceText)
	}
	return strings.Contains(hay, needle)
}

// ruleImageFanout verifies one image query per entity and the spec-required
// number of images per segment.
func ruleImageFanout(spec Spec, result MediaResult) CheckResult {
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for _, seg := range result.Segments {
		nQueries := len(seg.Insights.ImageQueries)
		nEnts := len(seg.Insights.Entities)
		if spec.EntitiesPerSegment > 0 && nQueries != spec.EntitiesPerSegment {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckImageFanout),
				Detail:    fmt.Sprintf("image queries = %d, expected one per entity (%d)", nQueries, spec.EntitiesPerSegment),
			})
		}
		if nEnts > 0 && nQueries != nEnts {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckImageFanout),
				Detail:    fmt.Sprintf("image queries = %d but entities = %d (fanout mismatch)", nQueries, nEnts),
			})
		}
		nImgs := len(seg.Assets.SecondaryImages) + len(seg.Assets.GeneratedImages)
		if spec.ImagesPerSegment > 0 && nImgs != spec.ImagesPerSegment {
			violations = append(violations, Violation{
				SegmentID: seg.SegmentID,
				Rule:      string(CheckImageFanout),
				Detail:    fmt.Sprintf("images selected = %d, expected %d", nImgs, spec.ImagesPerSegment),
			})
		}
		if allForSegment(violations, seg.SegmentID) == 0 {
			pass++
		}
	}
	return passCount(CheckImageFanout, pass, total, violations...)
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

// ruleQueryOwnership verifies every image/video query's owner_segment_id
// matches the segment that emitted it.
func ruleQueryOwnership(spec Spec, result MediaResult) CheckResult {
	pass, total := 0, len(result.Segments)
	var violations []Violation
	queryOwners := make(map[string]string)
	for _, seg := range result.Segments {
		drift := false
		checkQuery := func(q string, rejectDuplicate bool) {
			qKey := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(q)), " "))
			if rejectDuplicate && qKey != "" {
				if owner, exists := queryOwners[qKey]; exists && owner != seg.SegmentID {
					violations = append(violations, Violation{
						SegmentID: seg.SegmentID,
						Rule:      string(CheckQueryOwnership),
						Detail:    fmt.Sprintf("query %q is also owned by segment %q", q, owner),
					})
					drift = true
				} else {
					queryOwners[qKey] = seg.SegmentID
				}
			}
			// With exactly one result segment, every emitted query has one
			// possible owner. Lexical ownership is still enforced for
			// multi-scene runs, where it can detect cross-scene drift.
			if len(result.Segments) > 1 && !queryOwnedBy(seg, q, seg.SegmentID) {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckQueryOwnership),
					Detail:    fmt.Sprintf("query %q does not belong to this segment", q),
				})
				drift = true
			}
		}
		for _, q := range seg.Insights.ImageQueries {
			checkQuery(q, false)
		}
		// Artlist queries are an ownership contract only when the resolved
		// media plan actually allows Artlist video. In image-only runs a model
		// may still populate the optional legacy Artlist projection; treating
		// those unused strings as globally unique would reject valid shared
		// ingredient queries such as "olive oil".
		if spec.VideoProvider == script.VidRushProviderArtlist {
			for _, q := range seg.Insights.ArtlistQueries {
				checkQuery(q, true)
			}
		}
		if !drift {
			pass++
		}
	}
	return passCount(CheckQueryOwnership, pass, total, violations...)
}

// queryOwnedBy checks ownership by ensuring the query string references the
// segment subject or one of its visual terms.
func queryOwnedBy(seg ResultSegment, query string, ownerID string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	// Long deterministic queries may be a bounded projection of the
	// immutable source text rather than the compact subject/term fields.
	// Accept them only when the complete query is grounded in that same
	// canonical source; this does not weaken cross-scene ownership.
	source := strings.ToLower(strings.TrimSpace(seg.SourceText))
	if source != "" && (strings.Contains(source, q) || strings.Contains(q, source)) {
		return true
	}
	subject := strings.ToLower(strings.TrimSpace(segmentSubject(seg)))
	if subject != "" && strings.Contains(q, subject) {
		return true
	}
	for _, term := range segmentVisualTerms(seg) {
		if strings.Contains(q, strings.ToLower(term)) || strings.Contains(strings.ToLower(term), q) {
			return true
		}
	}
	if subject == "" && len(segmentVisualTerms(seg)) == 0 {
		return true
	}
	return false
}

func segmentSubject(seg ResultSegment) string {
	if seg.SceneIR != nil {
		return script.BuildSegmentVisualProfile(seg.SceneIR.Profile).Subject
	}
	if seg.SemanticProfile != nil {
		return script.BuildSegmentVisualProfile(*seg.SemanticProfile).Subject
	}
	if seg.Insights.VisualProfile != nil {
		return seg.Insights.VisualProfile.Subject
	}
	return ""
}

func segmentVisualTerms(seg ResultSegment) []string {
	if seg.SceneIR != nil {
		return script.BuildSegmentVisualProfile(seg.SceneIR.Profile).Terms
	}
	if seg.SemanticProfile != nil {
		return script.BuildSegmentVisualProfile(*seg.SemanticProfile).Terms
	}
	if seg.Insights.VisualProfile != nil {
		return seg.Insights.VisualProfile.Terms
	}
	return nil
}

// ruleAssetOwnership verifies every selected asset's provenance segment
// matches the segment it is bound to.
func ruleAssetOwnership(spec Spec, result MediaResult) CheckResult {
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for _, seg := range result.Segments {
		owners := candidatesOf(seg.Assets)
		drift := false
		for _, c := range owners {
			if strings.TrimSpace(c.SegmentID) != "" && c.SegmentID != seg.SegmentID {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckAssetOwnership),
					Detail:    fmt.Sprintf("asset %q provenance segment_id=%q does not match bound segment %q", c.AssetID, c.SegmentID, seg.SegmentID),
				})
				drift = true
			}
		}
		if !drift {
			pass++
		}
	}
	return passCount(CheckAssetOwnership, pass, total, violations...)
}

// candidatesOf flattens a SegmentAssetSelection into the list of candidates
// that were actually bound.
func candidatesOf(sel script.SegmentAssetSelection) []script.SegmentAssetCandidate {
	out := []script.SegmentAssetCandidate{}
	if sel.PrimaryVideo != nil {
		out = append(out, *sel.PrimaryVideo)
	}
	out = append(out, sel.SecondaryImages...)
	out = append(out, sel.GeneratedImages...)
	return out
}

// ruleCrossSceneReuse verifies no asset is bound to two segments when the
// spec forbids reuse.
func ruleCrossSceneReuse(spec Spec, result MediaResult) CheckResult {
	if spec.AllowCrossSceneAssetReuse {
		return passBool(CheckCrossSceneReuse, true)
	}
	seen := make(map[string]string)
	var violations []Violation
	reuseCount := 0
	totalSegments := len(result.Segments)
	for _, seg := range result.Segments {
		reused := false
		for _, c := range candidatesOf(seg.Assets) {
			id := strings.TrimSpace(c.AssetID)
			if id == "" {
				continue
			}
			if first, ok := seen[id]; ok && first != seg.SegmentID {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckCrossSceneReuse),
					Detail:    fmt.Sprintf("asset %q already bound to segment %q (reuse forbidden)", id, first),
				})
				reused = true
			} else {
				seen[id] = seg.SegmentID
			}
		}
		if !reused {
			reuseCount++
		}
	}
	return passCount(CheckCrossSceneReuse, reuseCount, totalSegments, violations...)
}

// ruleProviderPolicy verifies only spec-allowed VIDEO providers are used.
func ruleProviderPolicy(spec Spec, result MediaResult) CheckResult {
	allowed := strings.ToLower(strings.TrimSpace(spec.VideoProvider))
	pass, total := 0, len(result.Segments)
	var violations []Violation
	for _, seg := range result.Segments {
		bad := false
		if seg.Assets.PrimaryVideo != nil {
			p := strings.ToLower(strings.TrimSpace(seg.Assets.PrimaryVideo.Provider))
			if allowed != "" && p != "" && p != allowed {
				violations = append(violations, Violation{
					SegmentID: seg.SegmentID,
					Rule:      string(CheckProviderPolicy),
					Detail:    fmt.Sprintf("primary video %q provider %q not allowed (expected %q)", seg.Assets.PrimaryVideo.AssetID, seg.Assets.PrimaryVideo.Provider, allowed),
				})
				bad = true
			}
		}
		if !bad {
			pass++
		}
	}
	return passCount(CheckProviderPolicy, pass, total, violations...)
}

// ruleCrossContamination is the umbrella check that summarizes the
// query-ownership + asset-ownership + cross-scene-reuse results.
func ruleCrossContamination(spec Spec, result MediaResult) CheckResult {
	q := ruleQueryOwnership(spec, result)
	a := ruleAssetOwnership(spec, result)
	r := ruleCrossSceneReuse(spec, result)
	total := len(q.Violations) + len(a.Violations) + len(r.Violations)
	passed := total == 0
	return passBool(CheckCrossContamination, passed, append(append(append([]Violation{}, q.Violations...), a.Violations...), r.Violations...)...)
}

// sceneirUnused keeps the sceneir import alive for the ResultSegment.SceneIR
// field accessors even when no rule directly calls a sceneir function (the
// field type is *sceneir.SceneIR).
var _ = sceneir.SceneIR{}
