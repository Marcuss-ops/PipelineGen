// Package adapters — media_resolver_entity_matching.go: the identity-matching
// half of the entity-image projection: scene indexing, candidate lookup and the
// alias/acronym surfaces that decide whether a provider candidate really is the
// annotated entity.
//
// Split 2026-11-20 from media_resolver_entity_binding.go (BONIFICA-04): the
// blocks are moved verbatim, no behaviour change.
package adapters

import (
	"fmt"
	"hash/fnv"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/imagegate"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

type sceneIdentityIndex struct {
	bySegmentID   map[string]int
	bySceneID     map[string]int
	byIndex       map[int]int
	firstNoID     int
	duplicateKeys []string
}

func (idx *sceneIdentityIndex) recordDuplicate(kind, key string) {
	idx.duplicateKeys = append(idx.duplicateKeys, kind+"="+key)
}

// diagnostic names the first ambiguous identity key. A caller that sees a
// duplicate cannot safely choose either scene: the old first-occurrence rule
// silently attached an entity image to the wrong scene.
func (idx sceneIdentityIndex) diagnostic() string {
	if len(idx.duplicateKeys) == 0 {
		return ""
	}
	return "duplicate scene identity: " + strings.Join(idx.duplicateKeys, ", ")
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
		if _, ok := idx.byIndex[s.Index]; ok {
			idx.recordDuplicate("Index", fmt.Sprintf("%d", s.Index))
		} else {
			idx.byIndex[s.Index] = i
		}
		switch {
		case s.SegmentID != "":
			if _, ok := idx.bySegmentID[s.SegmentID]; ok {
				idx.recordDuplicate("SegmentID", s.SegmentID)
			} else {
				idx.bySegmentID[s.SegmentID] = i
			}
		case s.ID != "":
			if _, ok := idx.bySceneID[s.ID]; ok {
				idx.recordDuplicate("SceneID", s.ID)
			} else {
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
	if len(idx.duplicateKeys) > 0 {
		return -1
	}
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

func scenePrimaryEntityQueries(_ scriptpkg.SpecSceneOutput, _ sceneIdentityIndex, segment scriptpkg.VidRushSegmentResult) []string {
	queries := make([]string, 0, len(segment.Insights.Entities))
	seen := make(map[string]struct{}, cap(queries))
	for _, entity := range segment.Insights.Entities {
		if !isPersonOrgOrPlace(entity.Type) {
			continue
		}
		for _, intent := range entity.SearchIntents {
			if strings.TrimSpace(intent) == "" {
				continue
			}
			if _, exists := seen[intent]; exists {
				continue
			}
			seen[intent] = struct{}{}
			queries = append(queries, intent)
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
	matches := entityImageMatches(want, entity.Type, seg)
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
// their upstream order. Matching goes through the canonical identity/alias
// surfaces: a provider may return "Teflon Don" for a person whose annotation
// is "John Gotti", and an ORG/GPE may legitimately be an uppercase acronym.
func entityImageMatches(want, entityType string, seg scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	kind := normalizeAnnotationType(entityType)
	fields := strings.Fields(strings.TrimSpace(want))
	if kind == entitycatalog.EntityTypePerson && len(fields) > 1 && isUppercaseAcronym(fields[0]) {
		// An acronym is not a person name; an ORG/GPE acronym is handled by
		// canonicalEntitySurfaceMatch below.
		return nil
	}
	wantSurface := canonicalEntitySurfaceMatch(want, kind)
	aliases := entityAliasSurfaces(seg, want, kind)
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
		if !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) {
			continue
		}
		rawCandidateEntity := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate.Entity), "describe "))
		candidateEntity := strings.TrimSpace(strings.TrimPrefix(normalizeEntityMatch(candidate.Entity), "describe "))
		// Query is retrieval provenance, not identity evidence. In particular,
		// a query such as "Milano" can return a title containing "Rotmilan";
		// substring/query matches must never bind that result to an entity.
		if !entitySurfaceMatches(candidateEntity, rawCandidateEntity, wantSurface, aliases, kind) ||
			!imagegate.CandidateEligibleForSlot(candidate, false) {
			continue
		}
		// Search results are projected once before acquisition and again after
		// Drive/SQLite/pgvector materialization. Prefer the durable candidate on
		// the second pass; otherwise an early discovered hit can leave the
		// document with an asset_id but no drive_link.
		if !readyVidRushCandidate(candidate) && !verifiedImageCandidateWithoutDrive(candidate) {
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

// canonicalEntitySurfaceMatch folds a surface onto the identity space used by
// the catalog: PERSON names go through CanonicalizePersonName, every other
// kind uses the shared normalized surface.
func canonicalEntitySurfaceMatch(value, kind string) string {
	value = strings.TrimSpace(strings.TrimPrefix(normalizeEntityMatch(value), "describe "))
	if kind == entitycatalog.EntityTypePerson {
		if identity, err := entitycatalog.CanonicalizePersonName(value); err == nil {
			return normalizeEntityMatch(identity.CanonicalName)
		}
	}
	return value
}

// entityAliasSurfaces expands the wanted surface with the planner's alias map
// for this segment, so "Teflon Don" and "John Gotti" resolve to one identity.
func entityAliasSurfaces(seg scriptpkg.VidRushSegmentResult, want, kind string) map[string]struct{} {
	surfaces := map[string]struct{}{canonicalEntitySurfaceMatch(want, kind): {}}
	wantNormalized := normalizeEntityMatch(want)
	for alias, canonical := range seg.Insights.ImageEntityCanonicalNames {
		if normalizeEntityMatch(alias) == wantNormalized || normalizeEntityMatch(canonical) == wantNormalized {
			surfaces[canonicalEntitySurfaceMatch(alias, kind)] = struct{}{}
			surfaces[canonicalEntitySurfaceMatch(canonical, kind)] = struct{}{}
		}
	}
	return surfaces
}

func entitySurfaceMatches(candidate, rawCandidate, want string, aliases map[string]struct{}, kind string) bool {
	canonical := canonicalEntitySurfaceMatch(candidate, kind)
	if canonical == want {
		return true
	}
	if _, ok := aliases[canonical]; ok {
		return true
	}
	switch kind {
	case "ORG", "GPE", "LOCATION", "PLACE":
		// Check the original surface: the normalized form is lowercase, so
		// an uppercase acronym would otherwise be classified as a non-acronym.
		if isUppercaseAcronym(rawCandidate) {
			return acronymMatchesSurface(rawCandidate, want)
		}
	}
	return false
}

func acronymMatchesSurface(acronym, surface string) bool {
	fields := strings.Fields(normalizeEntityMatch(surface))
	if len(fields) == 0 {
		return false
	}
	initials := make([]byte, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, ".,;:()[]{}\"'")
		if field == "" || acronymConnectorWord(field) {
			continue
		}
		initials = append(initials, field[0])
	}
	if len(initials) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(acronym), string(initials))
}

// acronymConnectorWord lists the lowercase connectors that do not contribute
// a letter to an organization/place acronym. Without this, "Federal Bureau of
// Investigation" produced FBOI and never matched its canonical FBI surface.
func acronymConnectorWord(word string) bool {
	switch word {
	case "of", "the", "and", "for", "de", "del", "della", "di", "da", "van", "von", "la", "le", "les", "du", "des", "&":
		return true
	default:
		return false
	}
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

// verifiedImageCandidateWithoutDrive keeps a verified, content-addressed
// internet image bindable when only the Drive persistence step failed. The
// original source URL is a renderable fallback; RenderingGen still verifies
// the SHA-256 before using the bytes. Without this, a transient upload error
// silently destroys an otherwise valid entity image.
func verifiedImageCandidateWithoutDrive(candidate scriptpkg.SegmentAssetCandidate) bool {
	if !scriptpkg.IsInternetImageSourceProvider(candidate.Provider) {
		return false
	}
	return readyVidRushCandidateWithMode(candidate, true)
}

// entityImagePreviewURL returns the durable download URL for inline rendering.
// A Drive-backed candidate is preferred because the provider URL is not stable
// for a content-addressed asset; a later fetch of the same provider URL can
// return different bytes and fail RenderingGen's prefetch. Source/Preview URLs
// remain the fallback only when no Drive copy exists.
func entityImagePreviewURL(candidate scriptpkg.SegmentAssetCandidate) string {
	if id, err := urlutil.FileIDFromDriveLink(candidate.DriveLink); err == nil && strings.TrimSpace(id) != "" {
		return "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(strings.TrimSpace(id))
	}
	if source := strings.TrimSpace(candidate.SourceURL); source != "" {
		return source
	}
	return strings.TrimSpace(candidate.PreviewURL)
}
