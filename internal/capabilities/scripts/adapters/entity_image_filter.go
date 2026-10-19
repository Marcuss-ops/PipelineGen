// Package adapters — entity_image_filter.go: person identity/quality
// filtering for internet entity-image catalog results.
//
// Split out of entity_image_catalog.go to respect max_lines_per_file_strict
// (same package, no behavior change).
package adapters

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// if the provider supplies an explicit entity, it must resolve to the exact
// canonical PERSON identity (so Michael B. Jordan cannot enter Michael
// Jordan's pool). When dimensions or quality scores are present, they must
// also meet the minimum retrieval quality.
func filterPersonEntityImageCandidates(identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(results))
	for _, result := range results {
		accepted, semanticScore, technicalScore, reason := evaluatePersonImageCandidate(identity, result)
		if !accepted {
			continue
		}
		result.SemanticStatus = entitycatalog.CandidateSemanticAccepted
		result.SemanticScore = semanticScore
		result.TechnicalQualityScore = technicalScore
		result.QualityReason = reason
		out = append(out, result)
	}
	return out
}

func evaluatePersonImageCandidate(identity entitycatalog.PersonIdentity, candidate scriptpkg.SegmentAssetCandidate) (bool, float64, float64, string) {
	if reason := personImageSourceIdentityFailure(identity.CanonicalName, candidate.SourceURL, candidate.SourcePageURL); reason != "" {
		return false, 0, 0, reason
	}
	expectedID := identity.CanonicalEntityID
	semanticScore := 0.5
	reason := "PERSON identity inferred from query; technical dimensions not supplied"

	if entity := strings.TrimSpace(candidate.Entity); entity != "" {
		cleanEntity := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(entity, "'s"), "’s"))
		actual, err := entitycatalog.CanonicalizePersonName(cleanEntity)
		if err != nil || actual.CanonicalEntityID != expectedID {
			return false, 0, 0, "candidate entity does not match requested PERSON identity"
		}
		semanticScore = 1
		reason = "explicit PERSON entity matches canonical identity"
	} else if query := strings.TrimSpace(candidate.Query); query != "" {
		if !personQueryContainsCanonicalName(query, identity.CanonicalName) {
			return false, 0, 0, "candidate query does not contain requested PERSON identity"
		}
		semanticScore = 0.85
		reason = "candidate query contains canonical PERSON identity"
	}

	technicalScore := 0.0
	if candidate.TechnicalQualityScore > 0 {
		if candidate.TechnicalQualityScore < minimumQualityScore {
			return false, semanticScore, candidate.TechnicalQualityScore, "technical quality score below minimum"
		}
		technicalScore = candidate.TechnicalQualityScore
	}
	if candidate.Width > 0 && candidate.Height > 0 {
		longSide := candidate.Width
		if candidate.Height > longSide {
			longSide = candidate.Height
		}
		if longSide < minimumPersonImageLongSide || candidate.Width*candidate.Height < minimumPersonImagePixels {
			return false, semanticScore, technicalScore, "image dimensions below PERSON catalog minimum"
		}
		if technicalScore == 0 {
			technicalScore = 1
		}
		reason += "; dimensions meet minimum"
	}
	return true, semanticScore, technicalScore, reason
}

// personImageSourceIdentityFailure fails closed unless the durable image URL
// or persisted editorial source page carries the full canonical name. Search
// query/entity fields are not provenance: providers and cache adapters may
// copy them from the requested query even when the returned image is unrelated.
func personImageSourceIdentityFailure(canonicalName, sourceURL, sourcePageURL string) string {
	if mismatch := personImageSourceNameMismatch(canonicalName, sourceURL); mismatch != "" {
		return mismatch
	}
	if sourcePathHasCanonicalPersonName(canonicalName, sourceURL) {
		return ""
	}
	if sourcePageURL != "" && sourcePathHasCanonicalPersonName(canonicalName, sourcePageURL) {
		return ""
	}
	return fmt.Sprintf("image source URL does not establish canonical PERSON identity %q", canonicalName)
}

func sourcePathHasCanonicalPersonName(canonicalName, source string) bool {
	if strings.TrimSpace(canonicalName) == "" || strings.TrimSpace(source) == "" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return false
	}
	decoded := parsed.Path
	for i := 0; i < 2; i++ {
		if next, decodeErr := url.PathUnescape(decoded); decodeErr == nil && next != decoded {
			decoded = next
		}
	}
	tokens := strings.FieldsFunc(strings.ToLower(decoded), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	want := strings.FieldsFunc(normalizeEntityMatch(canonicalName), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(want) < 2 || len(tokens) < len(want) {
		return false
	}
	for start := 0; start <= len(tokens)-len(want); start++ {
		match := true
		for i := range want {
			if tokens[start+i] != want[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// personImageSourceNameMismatch catches a high-confidence provenance conflict
// when an image filename explicitly names the queried person but spells the
// surname differently (for example Angela-Tuccia for Angela Taccia). Provider
// adapters label results with the search query, so Entity alone is not proof
// of image identity. Generic filenames remain eligible for the pixel/layout
// verifier and later source checks.
func personImageSourceNameMismatch(canonicalName, sourceURL string) string {
	name := strings.Fields(normalizeEntityMatch(canonicalName))
	if len(name) < 2 || strings.TrimSpace(sourceURL) == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil {
		return ""
	}
	basename := path.Base(parsed.Path)
	for i := 0; i < 2; i++ {
		if decoded, decodeErr := url.PathUnescape(basename); decodeErr == nil && decoded != basename {
			basename = decoded
		}
	}
	tokens := strings.FieldsFunc(strings.ToLower(basename), func(r rune) bool { return !unicode.IsLetter(r) })
	if len(tokens) < 2 || tokens[0] != name[0] {
		return ""
	}
	second := tokens[1]
	if second == name[len(name)-1] || len([]rune(second)) < 3 || personImageFilenameQualifier(second) {
		return ""
	}
	return fmt.Sprintf("image source filename names %s %s, not requested person %s", name[0], second, canonicalName)
}

// PersonImageSourceIdentityFailure exposes the fail-closed source-provenance
// gate to adapters that verify cached or freshly discovered image pixels.
func PersonImageSourceIdentityFailure(canonicalName, sourceURL, sourcePageURL string) string {
	return personImageSourceIdentityFailure(canonicalName, sourceURL, sourcePageURL)
}

func personImageFilenameQualifier(token string) bool {
	switch token {
	case "foto", "photo", "portrait", "headshot", "image", "img", "profile", "person", "persona", "ritratto", "avvocato", "avvocata", "di", "della", "de", "del", "la", "il", "e", "the", "for", "case", "news", "ufficiale", "ufficiali", "ansa", "ipa":
		return true
	default:
		return false
	}
}

func personQueryContainsCanonicalName(query, canonicalName string) bool {
	want := strings.Fields(normalizeEntityMatch(canonicalName))
	got := strings.Fields(normalizeEntityMatch(query))
	if len(want) == 0 || len(got) < len(want) {
		return false
	}
	for start := 0; start <= len(got)-len(want); start++ {
		match := true
		for i := range want {
			if strings.Trim(got[start+i], ".,;:!?\"'") != strings.Trim(want[i], ".,;:!?\"'") {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func normalizeInternetImageCatalogResults(results []scriptpkg.SegmentAssetCandidate, query string) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.Provider) == "" {
			result.Provider = scriptpkg.VidRushProviderInternetImages
		}
		if strings.TrimSpace(result.Query) == "" {
			result.Query = query
		}
		out = append(out, result)
	}
	return out
}

func personCatalogIdentityForSegmentQuery(segment scriptpkg.VidRushSegmentResult, query string) (entitycatalog.PersonIdentity, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return entitycatalog.PersonIdentity{}, false, nil
	}
	if len(segment.Insights.ImageEntityCanonicalIDs) > 0 {
		for name, id := range segment.Insights.ImageEntityCanonicalIDs {
			if normalizeEntityMatch(name) != normalizeEntityMatch(query) || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), "person:") {
				continue
			}
			identity, err := entitycatalog.CanonicalizePersonIdentity(query, id)
			return identity, err == nil, err
		}
	}
	for _, entity := range segment.Insights.Entities {
		if normalizeAnnotationType(entity.Type) != entitycatalog.EntityTypePerson || normalizeEntityMatch(entity.Value) != normalizeEntityMatch(query) {
			continue
		}
		identity, err := entitycatalog.CanonicalizePersonName(entity.Value)
		return identity, err == nil, err
	}
	return entitycatalog.PersonIdentity{}, false, nil
}

func personCatalogIdentityForQuery(spec scriptpkg.SpecSceneOutput, idx sceneIdentityIndex, segment scriptpkg.VidRushSegmentResult, query string) (entitycatalog.PersonIdentity, bool, error) {
	best := idx.sceneFor(segment)
	if best == -1 || best >= len(spec.Scenes) || spec.Scenes[best].Annotations == nil {
		return entitycatalog.PersonIdentity{}, false, nil
	}
	query = strings.TrimSpace(query)
	for _, entity := range spec.Scenes[best].Annotations.PrimaryEntities {
		if normalizeAnnotationType(entity.Type) != entitycatalog.EntityTypePerson {
			continue
		}
		name := strings.TrimSpace(entity.CanonicalName)
		if name == "" {
			name = strings.TrimSpace(entity.Text)
		}
		if name == "" || (normalizeEntityMatch(name) != normalizeEntityMatch(query) && normalizeEntityMatch(entity.Text) != normalizeEntityMatch(query)) {
			continue
		}
		identity, err := entitycatalog.CanonicalizePersonIdentity(name, entity.CanonicalEntityID)
		if err != nil {
			return entitycatalog.PersonIdentity{}, false, err
		}
		return identity, true, nil
	}
	return entitycatalog.PersonIdentity{}, false, nil
}
