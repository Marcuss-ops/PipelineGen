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
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// projectEntityImageBindings attaches only provider candidates that are
// explicitly relevant to the primary entity. Generic scene candidates are
// never promoted to an entity image, preventing unrelated images from being
// presented as a person/org/place match.
func projectEntityImageBindings(spec scriptpkg.SpecSceneOutput, segments []scriptpkg.VidRushSegmentResult, policy mediadomain.EntityImagePolicy) scriptpkg.SpecSceneOutput {
	if !policy.Enabled || len(spec.Scenes) == 0 {
		return spec
	}
	out := cloneSpecSceneOutput(spec)
	// The default imageable set mirrors the kernel taxonomy registry
	// (script.IsAnnotationEntityKind): PERSON/ORG/GPE entity cards plus the
	// PRODUCT/LOGO media kinds. An explicit policy replaces the set.
	allowed := map[string]bool{"PERSON": true, "ORG": true, "GPE": true, "PRODUCT": true, "LOGO": true}
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
	resolvedEntityImages := make(map[string]struct{}, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	// usedCandidates is the run-scoped distribution ledger. The canonical
	// entity image library is SHARED across runs on purpose (published once per
	// entity, reused for cost), but within ONE run the same candidate must not
	// be rebound to every scene that mentions the entity: each occurrence
	// prefers a DISTINCT candidate from the same entity pool. When the pool has
	// only one usable candidate the fallback still binds it (availability beats
	// strict distinctness), so a cold catalog never loses the image entirely.
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
			entity.Image = &scriptpkg.EntityImageBinding{Status: "not_found"}
			if seg == nil {
				continue
			}
			variantSeed := strings.TrimSpace(out.Scenes[i].ID) + "|" + entityImageIdentity(*entity)
			if candidate, ok := findEntityImageCandidateFrom(*entity, *seg, variantSeed, usedCandidates); ok {
				if key := vidRushCandidateIdentity(candidate); key != "" {
					usedCandidates[key] = struct{}{}
				}
				identity := entityImageIdentity(*entity)
				bindingKey := identity
				if policy.PerScene() {
					// Per-scene scope deliberately lets the same person receive a
					// separately selected image in each scene. Keep the one-image-per-
					// scene guard here; run-scoped identity dedup belongs only to the
					// canonical shared-entity mode.
					bindingKey = strings.TrimSpace(out.Scenes[i].ID)
				}
				if _, alreadyBound := resolvedEntityImages[bindingKey]; !alreadyBound {
					if len(resolvedEntityImages) >= capabilityoverlay.MaxEntityImageOverlaysPerRun {
						// Keep the entity in the semantic result, but do not bind
						// another image once the run-level render budget is full.
						continue
					}
					resolvedEntityImages[bindingKey] = struct{}{}
				}
				entity.Image = &scriptpkg.EntityImageBinding{
					Status: "resolved", AssetID: candidate.AssetID,
					DriveLink: candidate.DriveLink, Source: candidate.Provider,
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
					// LegacyFileMD5 for historical reasons, so it is RESOLVED through
					// the canonical rule rather than forwarded verbatim — only a 64-hex
					// SHA-256 is a content address, and a legacy MD5 becomes "" so the
					// binding stays a plain reference instead of claiming an identity
					// it cannot prove.
					SHA256: asset.ResolveContentAddress(candidate.LegacyFileMD5),
				}
			}
		}
	}
	return out
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
	firstNoID   int
}

// buildSceneIdentityIndex builds the scene identity index. First-occurrence
// wins per key so min-index semantics match the old linear scan exactly.
func buildSceneIdentityIndex(spec scriptpkg.SpecSceneOutput) sceneIdentityIndex {
	idx := sceneIdentityIndex{
		bySegmentID: make(map[string]int, len(spec.Scenes)),
		bySceneID:   make(map[string]int, len(spec.Scenes)),
		firstNoID:   -1,
	}
	for i := range spec.Scenes {
		s := spec.Scenes[i]
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
	if segment.SegmentID != "" {
		if i, ok := idx.bySegmentID[segment.SegmentID]; ok {
			best = i
		}
	}
	if segment.SceneID != "" {
		if i, ok := idx.bySceneID[segment.SceneID]; ok && (best == -1 || i < best) {
			best = i
		}
	}
	if idx.firstNoID != -1 && (best == -1 || idx.firstNoID < best) {
		best = idx.firstNoID
	}
	return best
}

func scenePrimaryEntityQueries(spec scriptpkg.SpecSceneOutput, idx sceneIdentityIndex, segment scriptpkg.VidRushSegmentResult) []string {
	best := idx.sceneFor(segment)
	if best == -1 {
		return nil
	}
	scene := spec.Scenes[best]
	if scene.Annotations == nil {
		return nil
	}
	queries := make([]string, 0, len(scene.Annotations.PrimaryEntities))
	seen := make(map[string]struct{}, len(queries))
	for _, entity := range scene.Annotations.PrimaryEntities {
		if entity.Type != "PERSON" && entity.Type != "ORG" && entity.Type != "GPE" {
			continue
		}
		query := strings.TrimSpace(entity.CanonicalName)
		if query == "" {
			query = strings.TrimSpace(entity.Text)
		}
		key := strings.ToLower(query)
		if query == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		queries = append(queries, query)
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
// is skipped to its next unused sibling — and the rotated first match is the
// last-resort fallback so a single-candidate pool still binds.
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
	// a stable offset, so two scenes sharing the entity anchor on different
	// variants when the pool has them.
	start := entityVariantOffset(seed, len(matches))
	var firstMatch *scriptpkg.SegmentAssetCandidate
	for step := 0; step < len(matches); step++ {
		candidate := matches[(start+step)%len(matches)]
		key := vidRushCandidateIdentity(candidate)
		if excluded != nil && key != "" {
			if _, taken := excluded[key]; taken {
				if firstMatch == nil {
					match := candidate
					firstMatch = &match
				}
				continue
			}
		}
		return candidate, true
	}
	if firstMatch != nil {
		return *firstMatch, true
	}
	return scriptpkg.SegmentAssetCandidate{}, false
}

// entityImageMatches returns the identity-matching, ready, internet-images-only
// candidates for an entity, de-duplicated by candidate identity and preserving
// their upstream order.
func entityImageMatches(want string, seg scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
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
		candidateQuery := strings.TrimSpace(strings.TrimPrefix(normalizeEntityMatch(candidate.Query), "describe "))
		candidateEntity := strings.TrimSpace(strings.TrimPrefix(normalizeEntityMatch(candidate.Entity), "describe "))
		if candidateQuery != want && candidateEntity != want && !strings.Contains(candidateQuery, want) && !strings.Contains(candidateEntity, want) {
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

// entityVariantOffset maps a scene+entity seed deterministically onto an offset
// in [0, poolSize). The same (scene, entity, pool) always yields the same
// variant, so replays are stable, while different scenes spread across the
// pool. A pool of one (or an empty seed) always starts at zero.
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
