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
		accepted, _, technicalScore, reason := evaluatePersonImageCandidate(identity, result)
		if !accepted {
			if strings.TrimSpace(reason) == "" {
				reason = "rejected by PERSON identity/quality gate"
			}
			rejected[reason]++
			continue
		}
		// This gate checks only retrieval metadata (query identity, source
		// filename conflicts and technical dimensions). It cannot certify the
		// depicted person. Keep the candidate pending until the pixel verifier
		// returns an explicit identity verdict.
		if !ReusablePersonImageVerdict(result) {
			result.SemanticStatus = entitycatalog.CandidateSemanticUnknown
			result.SemanticScore = 0
			result.QualityReason = reason
		}
		result.TechnicalQualityScore = technicalScore
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
	if reason := personImageSourceNotRaster(candidate.SourceURL); reason != "" {
		return false, 0, 0, reason
	}
	// A definitive visual rejection is final: metadata must never resurrect
	// it. A rejection caused by a scorer outage stays retryable (its reason
	// is one of the transient shapes) and falls through to the unknown path.
	if strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticRejected) &&
		ReusablePersonImageVerdict(candidate) {
		reason := strings.TrimSpace(candidate.QualityReason)
		if reason == "" {
			reason = "candidate was rejected by visual identity verification"
		}
		return false, 0, 0, reason
	}
	expectedID := identity.CanonicalEntityID
	semanticScore := 0.0
	reason := "candidate is a retrieval match only; pixel identity remains unverified"

	if entity := strings.TrimSpace(candidate.Entity); entity != "" {
		cleanEntity := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(entity, "'s"), "’s"))
		actual, err := entitycatalog.CanonicalizePersonName(cleanEntity)
		if err != nil || actual.CanonicalEntityID != expectedID {
			return false, 0, 0, "candidate entity does not match requested PERSON identity"
		}
		reason = "candidate entity label matches canonical PERSON query; pixels remain unverified"
	} else if query := strings.TrimSpace(candidate.Query); query != "" {
		if !personQueryContainsCanonicalName(query, identity.CanonicalName) {
			return false, 0, 0, "candidate query does not contain requested PERSON identity"
		}
		reason = "candidate query contains canonical PERSON identity; pixels remain unverified"
	}

	technicalScore := 0.0
	if candidate.TechnicalQualityScore > 0 {
		if candidate.TechnicalQualityScore < minimumQualityScore {
			return false, semanticScore, candidate.TechnicalQualityScore, "technical quality score below minimum"
		}
		technicalScore = candidate.TechnicalQualityScore
	}
	if candidate.Width > 0 && candidate.Height > 0 {
		if !entitycatalog.ImageMeetsCatalogMinimum(candidate.Width, candidate.Height) {
			return false, semanticScore, technicalScore, "image dimensions below PERSON catalog minimum"
		}
		if technicalScore == 0 {
			technicalScore = 1
		}
		reason += "; dimensions meet minimum"
	}
	return true, semanticScore, technicalScore, reason
}

// personImageSourceNotRaster rejects provider links that resolve to a
// document viewer instead of an image file. HTML/PDF pages were being
// downloaded as if they were pixels: the decoder then either failed late or
// produced a non-image asset, which is exactly the class of failure the
// identity gate must stop at discovery time.
func personImageSourceNotRaster(sourceURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil || parsed.Path == "" {
		return ""
	}
	switch strings.ToLower(path.Ext(parsed.Path)) {
	case ".pdf", ".html", ".htm", ".xhtml", ".txt", ".doc", ".docx", ".ppt", ".pptx":
		return "source URL points to a document, not a raster image"
	default:
		return ""
	}
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
	basename = strings.TrimSuffix(basename, path.Ext(basename))
	tokens := strings.FieldsFunc(strings.ToLower(basename), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(tokens) < 2 || tokens[0] != name[0] {
		return ""
	}
	second := tokens[1]
	if second != name[len(name)-1] && (len([]rune(second)) < 3 || personImageFilenameQualifier(second)) {
		return ""
	}
	// A filename may contain one canonical surname plus generic qualifiers
	// ("john-gotti-photo-2020"). Two additional name-like tokens are not a
	// qualifier: "john-gotti-sammy-gravano" names a second person and must not
	// be reusable as John Gotti's portrait.
	consumed := 1
	for consumed < len(name) && consumed < len(tokens) && tokens[consumed] == name[consumed] {
		consumed++
	}
	if consumed < len(name) && second == name[len(name)-1] {
		consumed = 2
	}
	extraNames := 0
	for _, token := range tokens[consumed:] {
		if len([]rune(token)) < 3 || personImageFilenameQualifier(token) || isASCIIDigits(token) {
			continue
		}
		extraNames++
	}
	if extraNames >= 2 {
		return fmt.Sprintf("image source filename names multiple people, not only requested person %s", canonicalName)
	}
	if second != name[len(name)-1] {
		return fmt.Sprintf("image source filename names %s %s, not requested person %s", name[0], second, canonicalName)
	}
	return ""
}

func isASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// PersonImageSourceIdentityFailure exposes the fail-closed source-provenance
// gate to adapters that verify cached or freshly discovered image pixels.
func PersonImageSourceIdentityFailure(canonicalName, sourceURL, sourcePageURL string) string {
	return personImageSourceIdentityFailure(canonicalName, sourceURL, sourcePageURL)
}

func personImageFilenameQualifier(token string) bool {
	switch token {
	case "foto", "photo", "portrait", "headshot", "image", "img", "profile", "person", "persona", "ritratto", "di", "della", "de", "del", "la", "il", "e", "the", "for", "case", "news", "ufficiale", "ufficiali":
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

func canonicalImageEntityName(segment scriptpkg.VidRushSegmentResult, surface string) string {
	for alias, canonical := range segment.Insights.ImageEntityCanonicalNames {
		if normalizeEntityMatch(alias) == normalizeEntityMatch(surface) {
			return strings.TrimSpace(canonical)
		}
	}
	return ""
}

func canonicalImageEntityType(segment scriptpkg.VidRushSegmentResult, surface string) string {
	return canonicalImageEntityTypeFromIDs(segment.Insights.ImageEntityCanonicalIDs, surface)
}

// canonicalImageEntityTypeFromIDs resolves an entity surface through the
// canonical IDs produced by the image-intent resolver. It is the ONE type
// resolver for surfaces discovered outside the NER list: a canonical
// "place:...", "event:...", "org:..." id wins over a fallible NER claim.
// Only when no canonical id exists does the caller fall back to generic
// surface cues.
func canonicalImageEntityTypeFromIDs(canonicalIDs map[string]string, surface string) string {
	for alias, canonicalID := range canonicalIDs {
		if normalizeEntityMatch(alias) != normalizeEntityMatch(surface) {
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(canonicalID), ":", 2)
		if len(parts) == 2 {
			return normalizeAnnotationType(parts[0])
		}
	}
	return ""
}

func segmentPersonAliasGraph(segment scriptpkg.VidRushSegmentResult) *entitycatalog.PersonAliasGraph {
	graph := entitycatalog.NewPersonAliasGraph()
	for alias, canonicalID := range segment.Insights.ImageEntityCanonicalIDs {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(canonicalID)), "person:") {
			_ = graph.Register(canonicalID, alias)
		}
	}
	return graph
}

func personCatalogIdentityForSegmentQuery(segment scriptpkg.VidRushSegmentResult, query string) (entitycatalog.PersonIdentity, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return entitycatalog.PersonIdentity{}, false, nil
	}
	aliasGraph := segmentPersonAliasGraph(segment)
	// Resolve against the segment's actual PERSON mentions before consulting
	// aliases produced by retrieval planning. A provider query can be shortened
	// ("Floyd Mayweather") while VisualNER extracted the unambiguous full name
	// ("Floyd Mayweather Jr"). Using the retrieval alias as the catalog key
	// created a second PERSON pool that the annotation binder could never join.
	var exactEntityName string
	exactEntityWords := 0
	for _, entity := range segment.Insights.Entities {
		if normalizeAnnotationType(entity.Type) != entitycatalog.EntityTypePerson {
			continue
		}
		words := personQueryIdentityMatchWords(query, entity.Value)
		if words > exactEntityWords || (words == exactEntityWords && words > 0 && strings.ToLower(entity.Value) < strings.ToLower(exactEntityName)) {
			exactEntityName, exactEntityWords = entity.Value, words
		}
	}
	if exactEntityName == "" {
		// A query may itself be the shorter alias. Promote it to a longer
		// extracted identity only when that match is unique in this segment.
		// Ambiguous surnames are left unresolved rather than guessed.
		var aliasEntityName string
		aliasEntityWords := 0
		ambiguous := false
		for _, entity := range segment.Insights.Entities {
			if normalizeAnnotationType(entity.Type) != entitycatalog.EntityTypePerson ||
				personQueryIdentityMatchWords(entity.Value, query) == 0 {
				continue
			}
			words := len(strings.Fields(normalizeEntityMatch(entity.Value)))
			if words > aliasEntityWords {
				aliasEntityName, aliasEntityWords, ambiguous = entity.Value, words, false
			} else if words == aliasEntityWords && !strings.EqualFold(aliasEntityName, entity.Value) {
				ambiguous = true
			}
		}
		if aliasEntityName != "" && !ambiguous {
			exactEntityName = aliasEntityName
		}
	}
	if exactEntityName != "" {
		identity, err := entitycatalog.CanonicalizePersonNameWithAliases(exactEntityName, aliasGraph)
		return identity, err == nil, err
	}
	// Provider adapters commonly expand an exact identity query with a
	// retrieval qualifier ("photo", "portrait", etc.) and keep that expanded
	// value on each candidate. Resolve the canonical identity from the query's
	// full token sequence instead of requiring byte-for-byte equality. Prefer
	// the longest match so "Floyd Mayweather Jr photo" resolves to Floyd Jr,
	// not the shorter Floyd Mayweather alias.
	bestName, bestID, bestWords, bestNonPersonWords := "", "", 0, 0
	if len(segment.Insights.ImageEntityCanonicalIDs) > 0 {
		for name, id := range segment.Insights.ImageEntityCanonicalIDs {
			words := personQueryIdentityMatchWords(query, name)
			if words == 0 {
				continue
			}
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), "person:") {
				if words > bestNonPersonWords {
					bestNonPersonWords = words
				}
				continue
			}
			if words > bestWords || bestName == "" || strings.ToLower(name) < strings.ToLower(bestName) {
				bestName, bestID, bestWords = name, id, words
			}
		}
	}
	if bestNonPersonWords >= bestWords && bestNonPersonWords > 0 {
		// The canonical resolver is authoritative over baseline NER. Do not
		// resurrect a location/organization as a person image from a weaker
		// extracted entity match.
		return entitycatalog.PersonIdentity{}, false, nil
	}
	if bestName != "" {
		if candidates := aliasGraph.Candidates(bestName); len(candidates) > 1 {
			return entitycatalog.PersonIdentity{CanonicalName: bestName, Unresolved: true, Candidates: candidates}, false, entitycatalog.ErrAmbiguousPersonName
		}
		canonicalName := canonicalImageEntityName(segment, bestName)
		if canonicalName == "" {
			canonicalName = bestName
		}
		identity, err := entitycatalog.CanonicalizePersonIdentityWithAliases(canonicalName, bestID, aliasGraph)
		return identity, err == nil, err
	}
	// Fall back to the extracted entity list only when the canonical ID map
	// has no matching person. The canonical resolver's non-PERSON matches
	// remain authoritative and cannot be resurrected from weaker baseline NER.
	for _, entity := range segment.Insights.Entities {
		if normalizeAnnotationType(entity.Type) != entitycatalog.EntityTypePerson {
			continue
		}
		words := personQueryIdentityMatchWords(query, entity.Value)
		if words > bestWords || (words == bestWords && words > 0 && strings.ToLower(entity.Value) < strings.ToLower(bestName)) {
			bestName, bestWords = entity.Value, words
		}
	}
	if bestName != "" && bestWords > 0 {
		identity, err := entitycatalog.CanonicalizePersonName(bestName)
		return identity, err == nil, err
	}
	return entitycatalog.PersonIdentity{}, false, nil
}

func personQueryIdentityMatchWords(query, identity string) int {
	want := strings.Fields(normalizeEntityMatch(identity))
	got := strings.Fields(normalizeEntityMatch(query))
	if len(want) == 0 || len(got) < len(want) {
		return 0
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
			return len(want)
		}
	}
	return 0
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
