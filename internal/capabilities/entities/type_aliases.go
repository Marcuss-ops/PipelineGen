// Package entities — type_aliases.go owns entity IDENTITY on top of the ONE
// alias table in internal/kernel/entitytype.
//
// The table itself is not restated here: entitytype.Fold is the single
// spelling → family mapping shared by kernel/script (annotation kinds),
// platform/nlp, overlays, imagesearch and this package. What lives here is the
// identity layer:
//
//   - NormalizeType folds a raw extractor spelling onto the identity type;
//   - new ids are minted from that folded type (CanonicalKey, StableEntityID,
//     CanonicalEntityID);
//   - FoldCanonicalEntityID / CanonicalEntityIDCandidates translate the legacy
//     ids already persisted in the sqlite catalog, Drive-derived artifacts and
//     job payloads, so a pre-migration row still resolves without a second
//     identity format.
package entities

import (
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
)

// Canonical entity types. They are the values NormalizeType folds every
// spelling onto; the values are owned by kernel/entitytype so the annotation
// projection and the identity projection can never drift apart.
const (
	CanonicalTypePerson        = entitytype.Person
	CanonicalTypeOrg           = entitytype.Org
	CanonicalTypePlace         = entitytype.Place
	CanonicalTypeProduct       = entitytype.Product
	CanonicalTypeLogo          = entitytype.Logo
	CanonicalTypeEvent         = entitytype.Event
	CanonicalTypeWorkOfArt     = entitytype.WorkOfArt
	CanonicalTypeDate          = entitytype.Date
	CanonicalTypeTime          = entitytype.Time
	CanonicalTypeNumber        = entitytype.Number
	CanonicalTypeMoney         = entitytype.Money
	CanonicalTypePercent       = entitytype.Percent
	CanonicalTypeQuote         = entitytype.Quote
	CanonicalTypeConcept       = entitytype.Concept
	CanonicalTypeKeyword       = entitytype.Keyword
	CanonicalTypeVisualSubject = entitytype.VisualSubject
	CanonicalTypeChapter       = entitytype.Chapter
)

// foldEntityType is the raw table lookup behind NormalizeType: trimmed,
// upper-cased, then resolved through kernel/entitytype. An unknown type keeps
// its upper-case spelling so the identity stays deterministic.
func foldEntityType(entityType string) string {
	return entitytype.Fold(entityType)
}

// CanonicalEntityTypes returns a copy of the closed canonical vocabulary.
func CanonicalEntityTypes() []string {
	return entitytype.Families()
}

// EntityTypeAliases returns every spelling that folds onto the canonical type,
// canonical spelling first then the legacy spellings in lexical order. An
// unknown type yields just its normalized self (it has no aliases), and a
// blank type yields nil. It is the ONE enumeration compat lookups use.
func EntityTypeAliases(entityType string) []string {
	return entitytype.Aliases(entityType)
}

// isCanonicalEntityType reports whether entityType folds onto one of the
// closed canonical families (as opposed to an unknown extractor vocabulary).
func isCanonicalEntityType(entityType string) bool {
	return entitytype.IsCanonical(foldEntityType(entityType))
}

// FoldCanonicalEntityID rewrites an already-derived canonical entity id onto
// its canonical type spelling:
//
//	FoldCanonicalEntityID("gpe:garlasco")          == "place:garlasco"
//	FoldCanonicalEntityID("organization:acme-corp") == "org:acme-corp"
//	FoldCanonicalEntityID("person:michael-jordan")  == "person:michael-jordan"
//
// It is the compatibility READ for ids written before the fold: the slug is
// preserved verbatim (SafeEntityID did not change), only the type prefix is
// folded. A value without ":" — a StableEntityID ("ent_…"), a bare slug, an
// asset id — is returned trimmed and unchanged, so callers may pass any
// identity they hold. A blank input yields "".
func FoldCanonicalEntityID(canonicalID string) string {
	trimmed := strings.TrimSpace(canonicalID)
	if trimmed == "" {
		return ""
	}
	separator := strings.Index(trimmed, ":")
	if separator <= 0 {
		return trimmed
	}
	rawType := trimmed[:separator]
	slug := strings.TrimSpace(trimmed[separator+1:])
	if slug == "" {
		return trimmed
	}
	canonicalType := foldEntityType(rawType)
	if canonicalType == "" {
		return trimmed
	}
	return strings.ToLower(canonicalType) + ":" + slug
}

// CanonicalEntityIDCandidates returns the canonical entity id first, followed
// by the ids the same (type, name) pair would have carried under every legacy
// spelling of its type. It is the compatibility READ for durable rows written
// before the fold (the sqlite entity catalog, Drive-derived artifacts, job
// payloads): a lookup can try each candidate and find the pre-migration row
// without minting a second identity format.
//
//	CanonicalEntityIDCandidates("LOCATION", "Garlasco")
//	→ ["place:garlasco", "city:garlasco", "country:garlasco", "gpe:garlasco", …]
//
// The canonical id is always first; an unknown type yields exactly one
// candidate, and a name with no usable slug yields nil.
func CanonicalEntityIDCandidates(entityType, name string) []string {
	canonical := CanonicalEntityID(entityType, name)
	if canonical == "" {
		return nil
	}
	if !isCanonicalEntityType(entityType) {
		return []string{canonical}
	}
	candidates := []string{canonical}
	for _, alias := range EntityTypeAliases(entityType) {
		id := legacyCanonicalEntityID(alias, name)
		if id == "" || id == canonical {
			continue
		}
		candidates = append(candidates, id)
	}
	sort.Strings(candidates[1:])
	return candidates
}

// FoldCanonicalEntityIDCandidates is the id-only twin of
// CanonicalEntityIDCandidates, for callers that hold an already-derived id
// instead of a (type, name) pair — the media index, the catalog reader, a
// payload written before the fold. The canonical id is first, then the same
// slug under every legacy spelling of its type:
//
//	FoldCanonicalEntityIDCandidates("place:garlasco")
//	→ ["place:garlasco", "city:garlasco", "country:garlasco", "gpe:garlasco", …]
//
// A StableEntityID, a bare slug or a blank value yields itself (or nil), and
// an UNKNOWN type yields one candidate: its aliases are unknowable, so it is
// never guessed.
func FoldCanonicalEntityIDCandidates(canonicalID string) []string {
	canonical := FoldCanonicalEntityID(canonicalID)
	if canonical == "" {
		return nil
	}
	separator := strings.Index(canonical, ":")
	if separator <= 0 || canonical[separator+1:] == "" {
		return []string{canonical}
	}
	if !isCanonicalEntityType(canonical[:separator]) {
		return []string{canonical}
	}
	candidates := []string{canonical}
	for _, alias := range EntityTypeAliases(canonical[:separator]) {
		id := strings.ToLower(alias) + ":" + canonical[separator+1:]
		if id == canonical {
			continue
		}
		candidates = append(candidates, id)
	}
	sort.Strings(candidates[1:])
	return candidates
}

// legacyCanonicalEntityID formats an id with the RAW (unfolded) type prefix.
// It exists only so CanonicalEntityIDCandidates can name the ids written before
// the fold; new identities must go through CanonicalEntityID.
func legacyCanonicalEntityID(entityType, name string) string {
	rawType := strings.ToUpper(strings.TrimSpace(entityType))
	// Only the historical PERSON spelling stripped the possessive; every
	// other spelling kept it, and a compat candidate must be the id that was
	// actually written, not the one the new rule would have written.
	if rawType == CanonicalTypePerson {
		name = stripPersonPossessive(name)
	}
	slug := SafeEntityID(NormalizeName(name))
	if rawType == "" || slug == "" {
		return ""
	}
	return strings.ToLower(rawType) + ":" + slug
}
