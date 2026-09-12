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
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

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
			if candidate, ok := findEntityImageCandidate(*entity, *seg); ok {
				entity.Image = &scriptpkg.EntityImageBinding{
					Status: "resolved", AssetID: candidate.AssetID,
					DriveLink: candidate.DriveLink, Source: candidate.Provider,
					MediaType:  candidate.MIMEType,
					License:    candidate.RightsBasis,
					PreviewURL: entityImagePreviewURL(candidate),
					// The verified content address is what lets the binding be
					// promoted into the content-addressed EntityMediaIndex for
					// the entity card asset (bindings without it stay plain
					// references).
					SHA256: candidate.LegacyFileMD5,
				}
			}
		}
	}
	return out
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

func findEntityImageCandidate(entity scriptpkg.AnnotatedEntity, seg scriptpkg.VidRushSegmentResult) (scriptpkg.SegmentAssetCandidate, bool) {
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
	all := append(append([]scriptpkg.SegmentAssetCandidate(nil), seg.Assets.Candidates...), seg.Assets.SecondaryImages...)
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
		entityText := normalizeEntityMatch(candidate.Entity)
		query := normalizeEntityMatch(candidate.Query)
		candidateQuery := strings.TrimSpace(strings.TrimPrefix(query, "describe "))
		candidateEntity := strings.TrimSpace(strings.TrimPrefix(entityText, "describe "))
		if candidateQuery == want || candidateEntity == want || strings.Contains(candidateQuery, want) || strings.Contains(candidateEntity, want) {
			// Search results are projected once before acquisition and again
			// after Drive/SQLite/Qdrant materialization. Prefer the durable
			// candidate on the second pass; otherwise an early discovered hit
			// can leave the document with an asset_id but no drive_link.
			if readyVidRushCandidate(candidate) {
				return candidate, true
			}
		}
	}
	return scriptpkg.SegmentAssetCandidate{}, false
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
