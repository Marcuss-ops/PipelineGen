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
	"sort"
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
	accepted, _ := filterPersonEntityImageCandidatesWithRejections(identity, results)
	return accepted
}

// filterPersonEntityImageCandidatesWithRejections applies the same gate as
// filterPersonEntityImageCandidates but returns the rejection histogram. The
// histogram is persisted in the catalog refresh error so a zero-candidate
// refresh names the dominant gate instead of reporting only "no candidate".
func filterPersonEntityImageCandidatesWithRejections(identity entitycatalog.PersonIdentity, results []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, map[string]int) {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(results))
	rejected := make(map[string]int)
	for _, result := range results {
		accepted, semanticScore, technicalScore, reason := evaluatePersonImageCandidate(identity, result)
		if !accepted {
			if strings.TrimSpace(reason) == "" {
				reason = "rejected by PERSON identity/quality gate"
			}
			rejected[reason]++
			continue
		}
		result.SemanticStatus = entitycatalog.CandidateSemanticAccepted
		result.SemanticScore = semanticScore
		result.TechnicalQualityScore = technicalScore
		result.QualityReason = reason
		out = append(out, result)
	}
	return out, rejected
}

// formatPersonImageRejectionSummary renders a deterministic rejection
// breakdown: count descending, then reason ascending. It is part of the
// durable catalog error, so the ordering must not depend on map iteration.
func formatPersonImageRejectionSummary(total int, rejected map[string]int) string {
	if total <= 0 {
		return "provider returned no candidates"
	}
	type rejectionRow struct {
		reason string
		count  int
	}
	rows := make([]rejectionRow, 0, len(rejected))
	rejectedTotal := 0
	for reason, count := range rejected {
		rows = append(rows, rejectionRow{reason: reason, count: count})
		rejectedTotal += count
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].reason < rows[j].reason
	})
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf("%s (%d)", row.reason, row.count))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("provider returned %d candidate(s); no rejection reason recorded", total)
	}
	return fmt.Sprintf("provider returned %d candidate(s), rejected %d: %s", total, rejectedTotal, strings.Join(parts, "; "))
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

// personImageSourceIdentityFailure rejects only a high-confidence provenance
// conflict: an image filename that explicitly names the requested person with
// a different surname (for example Angela-Tuccia for Angela Taccia). A generic
// editorial filename (IMG_6075.jpeg) is NOT proof of a wrong identity — the
// pixel/visual verifier owns that decision. Requiring the canonical name in
// every URL made the catalog drop valid portraits before the VLM could see
// them, which is the Kaufmann zero-image failure mode.
func personImageSourceIdentityFailure(canonicalName, sourceURL, _ string) string {
	return personImageSourceNameMismatch(canonicalName, sourceURL)
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
	query = strings.TrimSpace(query)
	if best >= 0 && best < len(spec.Scenes) && spec.Scenes[best].Annotations != nil {
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
	}
	// The media stage can run before the semantic annotations are projected
	// onto SpecScene. The same PERSON is already present in segment.Insights
	// (it is what produced the image query), so use it as the identity source
	// instead of silently downgrading the query to a non-catalog search. This
	// is the Kaufmann r15 gap: 28 provider candidates were found, but the
	// query was not catalog-eligible and the durable pool stayed empty.
	return personCatalogIdentityForSegmentQuery(segment, query)
}
