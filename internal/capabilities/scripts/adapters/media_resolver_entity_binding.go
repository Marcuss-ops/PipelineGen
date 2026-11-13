// Package adapters — media_resolver_entity_binding.go: the entity-image
// projection half of the media resolver image stage.
//
// It answers one question: given a scene's certified PrimaryEntities and the
// materialized segment candidates, which provider candidate (if any) may be
// bound as that entity's card image? Generic scene candidates are never
// promoted to an entity image, and only internet-images candidates may bind.
//
// Extracted 2026-09-12 from media_resolver_image_stage.go to keep both halves
// under max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package adapters

import (
	"hash/fnv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// projectEntityImageBindings attaches only provider candidates that are
// explicitly relevant to the primary entity. Generic scene candidates are
// never promoted to an entity image, preventing unrelated images from being
// presented as a person/org/place match.
// isPersonOrgOrPlace is the query-building subset of the imageable kinds:
// people, organisations and places. The spellings come from
// kernel/entitytype, so GPE, LOC, CITY and LOCATION all agree.
func isPersonOrgOrPlace(raw string) bool {
	switch entitytype.Fold(raw) {
	case entitytype.Person, entitytype.Org, entitytype.Place:
		return true
	}
	return false
}

func projectEntityImageBindings(spec scriptpkg.SpecSceneOutput, segments []scriptpkg.VidRushSegmentResult, policy mediadomain.EntityImagePolicy) scriptpkg.SpecSceneOutput {
	if !policy.Enabled || len(spec.Scenes) == 0 {
		return spec
	}
	out := cloneSpecSceneOutput(spec)
	// The default imageable set IS the kernel taxonomy registry
	// (script.AnnotationEntityKinds): PERSON/ORG/GPE entity cards plus the
	// PRODUCT/LOGO media kinds. An explicit policy replaces the set.
	allowed := make(map[string]bool, 5)
	for _, kind := range scriptpkg.AnnotationEntityKinds() {
		allowed[kind] = true
	}
	if len(policy.EntityTypes) > 0 {
		allowed = make(map[string]bool, len(policy.EntityTypes))
		for _, raw := range policy.EntityTypes {
			allowed[normalizeAnnotationType(raw)] = true
		}
	}
	// Pre-index segments by SegmentID and SceneID (first occurrence wins, so
	// the min-index semantics of the old linear scan are preserved). The
	// per-scene lookup then becomes O(1) instead of a full O(segments) scan
	// for every scene.
	bySegmentID := make(map[string]int, len(segments))
	bySceneID := make(map[string]int, len(segments))
	for i := range segments {
		if segments[i].SegmentID != "" {
			if _, ok := bySegmentID[segments[i].SegmentID]; !ok {
				bySegmentID[segments[i].SegmentID] = i
			}
		}
		if segments[i].SceneID != "" {
			if _, ok := bySceneID[segments[i].SceneID]; !ok {
				bySceneID[segments[i].SceneID] = i
			}
		}
	}
	resolvedEntityAssets := make(map[string]struct{}, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	// usedCandidates is the run-scoped distribution ledger. The entity image
	// library is SHARED across runs on purpose (published once per entity,
	// reused for cost). Within one run each occurrence still PREFERS an unused
	// variant, but a person with only one verified portrait must be able to
	// reuse it in every scene: per_scene controls overlay placement, not the
	// obligation to find N distinct photos of one face.
	usedCandidates := make(map[string]struct{})
	for i := range out.Scenes {
		if out.Scenes[i].Annotations == nil {
			continue
		}
		seg := findSegmentForScene(out.Scenes[i], segments, bySegmentID, bySceneID)
		for entityIndex := range out.Scenes[i].Annotations.PrimaryEntities {
			entity := &out.Scenes[i].Annotations.PrimaryEntities[entityIndex]
			if !allowed[normalizeAnnotationType(entity.Type)] {
				continue
			}
			// A content-addressed binding written by the semantic production
			// plan is a fallback the media stage must not erase when it has no
			// candidate of its own: the hit was verified, it simply was not
			// discovered by this segment's fan-out.
			previousImage := entity.Image
			entity.Image = &scriptpkg.EntityImageBinding{Status: "not_found"}
			if seg == nil {
				entity.Image = missingEntityImageBinding(previousImage, "SEGMENT_MISSING")
				continue
			}
			variantSeed := strings.TrimSpace(out.Scenes[i].ID) + "|" + entityImageIdentity(*entity)
			candidate, ok := findEntityImageCandidateFrom(*entity, *seg, variantSeed, usedCandidates)
			if !ok {
				// The unused-variant pass is a preference, not a requirement.
				// Reusing one verified portrait across scenes is correct when the
				// entity pool has a single usable image.
				candidate, ok = findEntityImageCandidateFrom(*entity, *seg, variantSeed)
			}
			if ok {
				assetKey := vidRushCandidateIdentity(candidate)
				if assetKey == "" {
					assetKey = strings.TrimSpace(candidate.AssetID)
				}
				if assetKey != "" {
					usedCandidates[assetKey] = struct{}{}
					if _, alreadyBound := resolvedEntityAssets[assetKey]; !alreadyBound {
						if len(resolvedEntityAssets) >= capabilityoverlay.MaxEntityImageOverlaysPerRun {
							// Keep the entity in the semantic result, but do not bind
							// another distinct asset once the run-level render budget is
							// full. Reusing an already-admitted asset stays free: the budget
							// counts unique images, not scene occurrences.
							entity.Image = missingEntityImageBinding(previousImage, "BUDGET_EXHAUSTED", candidate.Query, candidate.AssetID, candidate.SelectionReason)
							continue
						}
						resolvedEntityAssets[assetKey] = struct{}{}
					}
				}
				entity.Image = &scriptpkg.EntityImageBinding{
					Status: "resolved", AssetID: candidate.AssetID,
					Query: candidate.Query, CandidateID: firstNonEmptyImageValue(candidate.AssetID, vidRushCandidateIdentity(candidate)),
					SelectionReason: candidate.SelectionReason,
					DriveLink:       candidate.DriveLink, DriveFileID: entityImageDriveFileID(candidate), Source: candidate.Provider,
					LocalPath:  candidate.LocalPath,
					MediaType:  candidate.MIMEType,
					License:    candidate.RightsBasis,
					PreviewURL: entityImagePreviewURL(candidate),
					// The verified content address is what lets the binding be
					// promoted into the content-addressed EntityMediaIndex for
					// the entity card asset (bindings without it stay plain
					// references).
					//
					// MEDIA-IDENTITY (Sept 2026): the candidate field is named
					// ContentSHA256 for historical reasons, so it is RESOLVED through
					// the canonical rule rather than forwarded verbatim — only a 64-hex
					// SHA-256 is a content address, and a legacy MD5 becomes "" so the
					// binding stays a plain reference instead of claiming an identity
					// it cannot prove.
					SHA256: asset.ResolveContentAddress(candidate.ContentSHA256),
				}
			} else {
				entity.Image = missingEntityImageBinding(previousImage, entityImageFailureReason(*entity, *seg), entityImageSearchQuery(*entity, *seg))
			}
		}
	}
	return out
}

// entityImageDriveFileID resolves the canonical Drive file id from the
// candidate's public link. The finalizer stores only DriveLink on a
// SegmentAssetCandidate, but the plan/audit contract requires the id to prove
// that an uploaded entity image is drive-addressable; deriving it here keeps
// the projection lossless without widening the candidate schema.
func entityImageDriveFileID(candidate scriptpkg.SegmentAssetCandidate) string {
	link := strings.TrimSpace(candidate.DriveLink)
	if link == "" {
		return ""
	}
	id, err := urlutil.FileIDFromDriveLink(link)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

// preservedPlanImageBinding keeps a verified binding that the planner wrote
// when the media stage has nothing to replace it with. Only a bound, hashed
// image qualifies: an unhashed reference cannot become a card asset, and a
// media-stage "resolved" binding follows its own re-projection rules.
func preservedPlanImageBinding(binding *scriptpkg.EntityImageBinding) *scriptpkg.EntityImageBinding {
	if binding != nil && strings.EqualFold(strings.TrimSpace(binding.Status), "bound") && strings.TrimSpace(binding.SHA256) != "" {
		return binding
	}
	return &scriptpkg.EntityImageBinding{Status: "not_found"}
}

func missingEntityImageBinding(previous *scriptpkg.EntityImageBinding, reason string, provenance ...string) *scriptpkg.EntityImageBinding {
	if preserved := preservedPlanImageBinding(previous); preserved != nil && preserved.Status != "not_found" {
		return preserved
	}
	binding := &scriptpkg.EntityImageBinding{Status: "not_found", FailureReason: reason}
	if len(provenance) > 0 {
		binding.Query = provenance[0]
	}
	if len(provenance) > 1 {
		binding.CandidateID = provenance[1]
	}
	if len(provenance) > 2 {
		binding.SelectionReason = provenance[2]
	}
	return binding
}

func entityImageSearchQuery(entity scriptpkg.AnnotatedEntity, seg scriptpkg.VidRushSegmentResult) string {
	want := normalizeEntityMatch(entity.CanonicalName)
	if want == "" {
		want = normalizeEntityMatch(entity.Text)
	}
	for query, canonicalID := range seg.Insights.ImageEntityCanonicalIDs {
		if normalizeEntityMatch(query) == want && canonicalID != "" {
			return strings.TrimSpace(query)
		}
	}
	return strings.TrimSpace(entity.CanonicalName)
}

func entityImageFailureReason(entity scriptpkg.AnnotatedEntity, seg scriptpkg.VidRushSegmentResult) string {
	want := normalizeEntityMatch(entity.CanonicalName)
	if want == "" {
		want = normalizeEntityMatch(entity.Text)
	}
	want = strings.TrimSpace(strings.TrimPrefix(want, "describe "))
	for _, candidate := range append(append([]scriptpkg.SegmentAssetCandidate(nil), seg.Assets.Candidates...), seg.Assets.SecondaryImages...) {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		identity := normalizeEntityMatch(candidate.Entity)
		query := normalizeEntityMatch(candidate.Query)
		if want != "" && identity != want && !strings.Contains(query, want) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(candidate.LifecycleFailureStage)) {
		case scriptpkg.VidRushLifecycleStageAcquire:
			return "DOWNLOAD_FAILED"
		case scriptpkg.VidRushLifecycleStageVerify:
			return "UNVERIFIED_IMAGE"
		case scriptpkg.VidRushLifecycleStageFinalize:
			return "UPLOAD_FAILED"
		}
		if strings.EqualFold(candidate.AcquisitionStatus, "failed") {
			return "DOWNLOAD_FAILED"
		}
		if strings.EqualFold(candidate.VerificationStatus, "failed") {
			return "UNVERIFIED_IMAGE"
		}
		if strings.EqualFold(candidate.PersistenceStatus, "failed") {
			return "UPLOAD_FAILED"
		}
	}
	matching := 0
	for _, candidate := range append(append([]scriptpkg.SegmentAssetCandidate(nil), seg.Assets.Candidates...), seg.Assets.SecondaryImages...) {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		identity := normalizeEntityMatch(candidate.Entity)
		query := normalizeEntityMatch(candidate.Query)
		if want != "" && identity != want && !strings.Contains(query, want) {
			continue
		}
		matching++
		if !validVidRushCandidate(candidate) || !strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), "accepted") {
			return "UNVERIFIED_IMAGE"
		}
		if !readyVidRushCandidate(candidate) {
			return "NOT_MATERIALIZED"
		}
	}
	if matching > 0 {
		return "IDENTITY_MISMATCH"
	}
	if len(seg.Insights.ImageQueries) == 0 {
		return "SEARCH_EMPTY"
	}
	return "SEARCH_EMPTY"
}

func firstNonEmptyImageValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func entityImageIdentity(entity scriptpkg.AnnotatedEntity) string {
	name := normalizeEntityMatch(entity.CanonicalName)
	if name == "" {
		name = normalizeEntityMatch(entity.Text)
	}
	return strings.ToUpper(strings.TrimSpace(entity.Type)) + "|" + name
}

// ProjectEntityImageBindings reapplies the canonical identity-image
// projection to a final scene envelope. It is intentionally exported for the
// persistence boundary: later postprocessors may rebuild annotations while
// retaining the already materialized segment candidates.
func ProjectEntityImageBindings(spec scriptpkg.SpecSceneOutput, segments []scriptpkg.VidRushSegmentResult, policy mediadomain.EntityImagePolicy) scriptpkg.SpecSceneOutput {
	return projectEntityImageBindings(spec, segments, policy)
}

// sceneIdentityIndex pre-indexes spec.Scenes by SegmentID and ID so the
// per-segment scene lookup is O(1) instead of a full O(scenes) scan for
// every segment. firstNoID records the first scene carrying neither
// identity key (the "matches any segment" fallback).
type sceneIdentityIndex struct {
	bySegmentID map[string]int
	bySceneID   map[string]int
	byIndex     map[int]int
	firstNoID   int
}

// buildSceneIdentityIndex builds the scene identity index. First-occurrence
// wins per key so min-index semantics match the old linear scan exactly.
func buildSceneIdentityIndex(spec scriptpkg.SpecSceneOutput) sceneIdentityIndex {
	idx := sceneIdentityIndex{
		bySegmentID: make(map[string]int, len(spec.Scenes)),
		bySceneID:   make(map[string]int, len(spec.Scenes)),
		byIndex:     make(map[int]int, len(spec.Scenes)),
		firstNoID:   -1,
	}
	for i := range spec.Scenes {
		s := spec.Scenes[i]
		if _, ok := idx.byIndex[s.Index]; !ok {
			idx.byIndex[s.Index] = i
		}
		switch {
		case s.SegmentID != "":
			if _, ok := idx.bySegmentID[s.SegmentID]; !ok {
				idx.bySegmentID[s.SegmentID] = i
			}
		case s.ID != "":
			if _, ok := idx.bySceneID[s.ID]; !ok {
				idx.bySceneID[s.ID] = i
			}
		default:
			if idx.firstNoID == -1 {
				idx.firstNoID = i
			}
		}
	}
	return idx
}

// sceneFor returns the index of the first scene matching the segment,
// mirroring the original linear-scan precedence: SegmentID match, then
// ID match, then the first identity-less scene — whichever is earliest.
func (idx sceneIdentityIndex) sceneFor(segment scriptpkg.VidRushSegmentResult) int {
	best := -1
	// The durable SpecScene uses the segment id as the scene id (`cold-case`),
	// while the VidRush result may carry it in either SegmentID or SceneID.
	// Both keys are matched against both maps so the association does not
	// depend on which side of the contract filled which field.
	for _, key := range []string{segment.SegmentID, segment.SceneID} {
		if key == "" {
			continue
		}
		if i, ok := idx.bySegmentID[key]; ok && (best == -1 || i < best) {
			best = i
		}
		if i, ok := idx.bySceneID[key]; ok && (best == -1 || i < best) {
			best = i
		}
	}
	// Last resort: the ordinal is the one identity both timelines share even
	// when a producer regenerated the string ids.
	if i, ok := idx.byIndex[segment.Position]; ok && (best == -1 || i < best) {
		best = i
	}
	if idx.firstNoID != -1 && (best == -1 || idx.firstNoID < best) {
		best = idx.firstNoID
	}
	return best
}

func scenePrimaryEntityQueries(spec scriptpkg.SpecSceneOutput, idx sceneIdentityIndex, segment scriptpkg.VidRushSegmentResult) []string {
	best := idx.sceneFor(segment)
	queries := make([]string, 0, len(segment.Insights.Entities))
	seen := make(map[string]struct{}, cap(queries))
	add := func(query string) {
		query = strings.TrimSpace(query)
		key := strings.ToLower(query)
		if query == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		queries = append(queries, query)
	}
	// Include every grounded imageable entity found by segment extraction.
	// Scene annotations can be incomplete at this pipeline stage (often only
	// the lead person has been projected); using them as the sole source drops
	// co-present entities before their image searches can run.
	for _, entity := range segment.Insights.Entities {
		if !isPersonOrgOrPlace(entity.Type) {
			continue
		}
		add(entity.Value)
	}
	if best >= 0 && spec.Scenes[best].Annotations != nil {
		for _, entity := range spec.Scenes[best].Annotations.PrimaryEntities {
			if !isPersonOrgOrPlace(entity.Type) {
				continue
			}
			query := strings.TrimSpace(entity.CanonicalName)
			if query == "" {
				query = strings.TrimSpace(entity.Text)
			}
			add(query)
		}
	}
	return queries
}

func findSegmentForScene(scene scriptpkg.SpecScene, segments []scriptpkg.VidRushSegmentResult, bySegmentID, bySceneID map[string]int) *scriptpkg.VidRushSegmentResult {
	if scene.SegmentID == "" && scene.ID == "" {
		// Positional fallback for scenes that carry neither identity key.
		for i := range segments {
			if scene.Index == segments[i].Position {
				return &segments[i]
			}
		}
		return nil
	}
	// The old single-pass scan returned the first index where EITHER key
	// matched; taking the min of the two first-occurrence indices preserves
	// that exactly.
	best := -1
	if scene.SegmentID != "" {
		if i, ok := bySegmentID[scene.SegmentID]; ok {
			best = i
		}
	}
	if scene.ID != "" {
		if i, ok := bySceneID[scene.ID]; ok && (best == -1 || i < best) {
			best = i
		}
	}
	if best == -1 {
		return nil
	}
	return &segments[best]
}

func findEntityImageCandidate(entity scriptpkg.AnnotatedEntity, seg scriptpkg.VidRushSegmentResult, used ...map[string]struct{}) (scriptpkg.SegmentAssetCandidate, bool) {
	return findEntityImageCandidateFrom(entity, seg, "", used...)
}

// findEntityImageCandidateFrom is findEntityImageCandidate plus deterministic
// N-variant selection. seed is the per-scene identity (scene id + entity
// identity): it selects a stable STARTING VARIANT inside the entity's ranked
// candidate pool, so a shared library holding several variants of one entity
// gives different scenes different images instead of every scene fronting the
// same portrait. The used ledger still wins — an already-distributed candidate
// is skipped to its next unused sibling. Once every candidate has appeared in
// the run, this returns no match instead of reusing a portrait as false variety.
func findEntityImageCandidateFrom(entity scriptpkg.AnnotatedEntity, seg scriptpkg.VidRushSegmentResult, seed string, used ...map[string]struct{}) (scriptpkg.SegmentAssetCandidate, bool) {
	want := normalizeEntityMatch(entity.CanonicalName)
	if want == "" {
		want = normalizeEntityMatch(entity.Text)
	}
	// Small local models sometimes turn prompt instructions into the
	// extracted entity text (for example, "Describe John Cena").  The
	// provider query and the public person's canonical name are still
	// "John Cena", so remove that non-semantic instruction prefix before
	// comparing the entity with a retrieved candidate.
	want = strings.TrimSpace(strings.TrimPrefix(want, "describe "))
	var excluded map[string]struct{}
	if len(used) > 0 {
		excluded = used[0]
	}
	matches := entityImageMatches(want, seg)
	if len(matches) == 0 {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	// Deterministic variant rotation: the scene+entity seed starts the scan at
	// a stable offset, while the run ledger spreads repeated scenes across the
	// available pool.
	start := entityVariantOffset(seed, len(matches))
	for step := 0; step < len(matches); step++ {
		candidate := matches[(start+step)%len(matches)]
		key := vidRushCandidateIdentity(candidate)
		if excluded != nil && key != "" {
			if _, taken := excluded[key]; taken {
				continue
			}
		}
		return candidate, true
	}
	return scriptpkg.SegmentAssetCandidate{}, false
}

// entityImageMatches returns the identity-matching, ready, internet-images-only
// candidates for an entity, de-duplicated by candidate identity and preserving
// their upstream order.
func entityImageMatches(want string, seg scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	fields := strings.Fields(strings.TrimSpace(want))
	if len(fields) > 1 && isUppercaseAcronym(fields[0]) {
		return nil
	}
	all := append(append([]scriptpkg.SegmentAssetCandidate(nil), seg.Assets.Candidates...), seg.Assets.SecondaryImages...)
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(all))
	seen := make(map[string]struct{}, len(all))
	for _, candidate := range all {
		if !validVidRushCandidate(candidate) || strings.TrimSpace(candidate.AssetID) == "" {
			continue
		}
		// Entity images are internet-images-only by contract: a candidate from
		// any other provider (even a fully materialized video whose query
		// matches the person) must never bind as a person/org/place image.
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		candidateEntity := strings.TrimSpace(strings.TrimPrefix(normalizeEntityMatch(candidate.Entity), "describe "))
		// Query is retrieval provenance, not identity evidence. In particular,
		// a query such as "Milano" can return a title containing "Rotmilan";
		// substring/query matches must never bind that result to an entity.
		if candidateEntity != want || !strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), "accepted") {
			continue
		}
		// Search results are projected once before acquisition and again after
		// Drive/SQLite/Qdrant materialization. Prefer the durable candidate on
		// the second pass; otherwise an early discovered hit can leave the
		// document with an asset_id but no drive_link.
		if !readyVidRushCandidate(candidate) {
			continue
		}
		if key := vidRushCandidateIdentity(candidate); key != "" {
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, candidate)
	}
	return out
}

func isUppercaseAcronym(value string) bool {
	letters := 0
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
		letters++
	}
	return letters >= 2 && letters <= 6
}

// entityVariantOffset maps a scene+entity seed deterministically onto an offset
// in [0, poolSize). The same (scene, entity, pool) always yields the same
// variant, so replays are stable, while different scenes spread across the pool.
func entityVariantOffset(seed string, poolSize int) int {
	if poolSize <= 1 || strings.TrimSpace(seed) == "" {
		return 0
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(seed))
	return int(hasher.Sum32() % uint32(poolSize))
}

// entityImagePreviewURL returns the direct image URL used for inline rendering:
// the candidate's source image first, then its preview URL. It never falls back
// to the Drive view-page link, which is not a renderable image.
func entityImagePreviewURL(candidate scriptpkg.SegmentAssetCandidate) string {
	if url := strings.TrimSpace(candidate.SourceURL); url != "" {
		return url
	}
	return strings.TrimSpace(candidate.PreviewURL)
}

func normalizeEntityMatch(value string) string {
	decomposed := norm.NFD.String(strings.ToLower(strings.TrimSpace(value)))
	var b strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	// NER commonly emits an English possessive when the name is the subject
	// of a sentence ("Dwayne Johnson's journey"), while image candidates are
	// keyed by the canonical identity ("Dwayne Johnson").
	return strings.TrimSuffix(strings.TrimSuffix(b.String(), "'s"), "’s")
}
