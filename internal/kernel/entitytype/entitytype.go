// Package entitytype owns the ONE entity-type alias table of the pipeline.
//
// The NLP side deliberately keeps its own vocabulary (spaCy emits GPE, LOC,
// FAC; the semantic profile emits LOCATION; some providers say CITY or
// COUNTRY), but an entity's IDENTITY must not depend on which extractor saw
// it. Before this table each layer carried its own switch, so the same place
// could be GPE for the annotation merger, LOCATION for the overlay planner and
// PLACE for the catalog:
//
//	("GPE", "Garlasco")      → ent_a… (one identity)
//	("LOCATION", "Garlasco") → ent_b… (a different identity)
//
// and the two ids could not join. That is why an image verified under one
// spelling was invisible to a plan written with another, and why a
// documentary about a country reported zero bound images.
//
// The table lives in the kernel because every layer needs it and only the
// kernel is importable by all of them (kernel/script cannot import
// capabilities/entities; platform/nlp already depends on capabilities). The
// layers now PROJECT this single table instead of restating it:
//
//   - capabilities/entities folds it into the identity type (PLACE, ORG, …)
//     used by CanonicalKey / StableEntityID / CanonicalEntityID;
//   - kernel/script projects the family onto an annotation kind (GPE for
//     places, WORK_OF_ART for works) for the overlay/media vocabulary;
//   - the place/entity predicates of scripts, imagesearch, overlays and
//     kernel/media ask Is(raw, Family) instead of listing spellings again.
//
// Canonical families are a CLOSED set: every alias maps to a member of
// canonicalFamilies and every family maps to itself (pinned by test). An
// unknown type is NOT folded, it is upper-cased and trimmed exactly as
// before: inventing a family for a vocabulary nobody declared would collapse
// unrelated entities.
package entitytype

import (
	"sort"
	"strings"
	"unicode"
)

// Canonical entity families. They are the values Fold maps every spelling
// onto; the rest of the pipeline may keep using its extractor vocabulary as
// long as identity and predicates go through this package.
const (
	Person        = "PERSON"
	Org           = "ORG"
	Place         = "PLACE"
	Product       = "PRODUCT"
	Logo          = "LOGO"
	Event         = "EVENT"
	WorkOfArt     = "WORK_OF_ART"
	Date          = "DATE"
	Time          = "TIME"
	Number        = "NUMBER"
	Money         = "MONEY"
	Percent       = "PERCENT"
	Quote         = "QUOTE"
	Concept       = "CONCEPT"
	Keyword       = "KEYWORD"
	VisualSubject = "VISUAL_SUBJECT"
	Chapter       = "CHAPTER"
)

// canonicalFamilies is the closed canonical vocabulary, in declaration order
// (usage: docs and tests; lookups use canonicalSet).
var canonicalFamilies = []string{
	Person,
	Org,
	Place,
	Product,
	Logo,
	Event,
	WorkOfArt,
	Date,
	Time,
	Number,
	Money,
	Percent,
	Quote,
	Concept,
	Keyword,
	VisualSubject,
	Chapter,
}

var canonicalSet = func() map[string]bool {
	set := make(map[string]bool, len(canonicalFamilies))
	for _, family := range canonicalFamilies {
		set[family] = true
	}
	return set
}()

// aliases maps every accepted spelling (already upper-case) to its canonical
// family. Keep it flat and explicit: a generated or inferred alias is
// impossible to review, and this table is the contract identity, the audit and
// every place/entity predicate depend on.
var aliases = map[string]string{
	// People.
	"PERSON":    Person,
	"PER":       Person,
	"HUMAN":     Person,
	"CEO":       Person,
	"FOUNDER":   Person,
	"PRESIDENT": Person,

	// Organizations.
	"ORG":          Org,
	"ORGANIZATION": Org,
	"ORGANISATION": Org,
	"COMPANY":      Org,
	"CORP":         Org,
	"CORPORATION":  Org,
	"BUSINESS":     Org,
	"INSTITUTION":  Org,
	"AGENCY":       Org,
	"COURT":        Org,
	"NGO":          Org,
	"GOVERNMENT":   Org,
	"UNIVERSITY":   Org,
	"TEAM":         Org,

	// Places (the group that motivated the fold: GPE/LOC/LOCATION/CITY/
	// COUNTRY/FAC are the same real-world thing spelled by different
	// extractors).
	"PLACE":    Place,
	"GPE":      Place,
	"LOC":      Place,
	"LOCATION": Place,
	"CITY":     Place,
	"COUNTRY":  Place,
	"NATION":   Place,
	"REGION":   Place,
	"STATE":    Place,
	"PROVINCE": Place,
	"TOWN":     Place,
	"VILLAGE":  Place,
	"LANDMARK": Place,
	"SITE":     Place,
	"GEO":      Place,
	"ADDRESS":  Place,
	"FAC":      Place,
	"FACILITY": Place,

	// Products and brands.
	"PRODUCT": Product,
	"LOGO":    Logo,
	"BRAND":   Logo,

	// Editorial kinds.
	"EVENT":       Event,
	"WORK_OF_ART": WorkOfArt,
	"WORK":        WorkOfArt,
	"ARTWORK":     WorkOfArt,

	// Value kinds. CARDINAL/ORDINAL/QUANTITY and the metric spellings are the
	// same identity as NUMBER: a value annotated as "CARDINAL" by one pass and
	// "NUMBER" by another must not mint two ids.
	"DATE":          Date,
	"DATETIME":      Date,
	"CALENDAR_DATE": Date,
	"TIME":          Time,
	"NUMBER":        Number,
	"NUM":           Number,
	"CARDINAL":      Number,
	"ORDINAL":       Number,
	"QUANTITY":      Number,
	"METRIC":        Number,
	"METRICS":       Number,
	"STATISTIC":     Number,
	"STATISTICS":    Number,
	"COUNT":         Number,
	"MONEY":         Money,
	"CURRENCY":      Money,
	"PERCENT":       Percent,
	"PERCENTAGE":    Percent,
	"QUOTE":         Quote,

	// Concepts and search surfaces.
	"CONCEPT":        Concept,
	"MISC":           Concept,
	"NORP":           Concept,
	"LAW":            Concept,
	"LANGUAGE":       Concept,
	"IDEA":           Concept,
	"VISUAL_CONCEPT": Concept,
	"KEYWORD":        Keyword,
	"VISUAL_SUBJECT": VisualSubject,
	"CHAPTER":        Chapter,
}

// Fold returns the canonical family of a raw extractor spelling: trimmed,
// upper-cased, then resolved through the alias table. An unknown type keeps
// its upper-case spelling so the result stays deterministic.
func Fold(raw string) string {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	if family, ok := aliases[normalized]; ok {
		return family
	}
	return normalized
}

// Surface heads that are strong evidence for a place or an organisation when
// the extractor has already claimed PERSON. The table is intentionally small
// and conservative: it only corrects a multi-token surface, never a bare word
// that may be a surname ("Villa", "Corte").
var surfacePlaceHeads = map[string]bool{
	"villa": true, "parco": true, "piazza": true, "piazzale": true,
	"via": true, "viale": true, "corso": true, "largo": true,
	"palazzo": true, "castello": true, "torre": true, "ponte": true,
	"museo": true, "teatro": true, "chiesa": true, "basilica": true,
	"cattedrale": true, "duomo": true, "stazione": true, "aeroporto": true,
	"porto": true, "lago": true, "fiume": true, "monte": true, "monti": true,
	"isola": true, "spiaggia": true, "riserva": true, "giardino": true,
	"giardini": true, "cimitero": true, "foro": true, "anfiteatro": true,
	"terme": true, "fontana": true, "obelisco": true, "arco": true,
	"mura": true, "borgo": true, "località": true, "localita": true,
	"regione": true, "provincia": true, "quartiere": true, "lungomare": true,
	"riva": true, "cala": true, "golfo": true, "stretto": true, "passo": true,
	"valle": true, "collina": true, "pianura": true, "deserto": true,
	"foresta": true, "bosco": true, "cascata": true, "ghiacciaio": true,
	"vulcano": true,
}

var surfaceOrganizationHeads = map[string]bool{
	"corte": true, "tribunale": true, "procura": true, "ministero": true,
	"governo": true, "parlamento": true, "commissione": true,
	"fondazione": true, "società": true, "societa": true, "compagnia": true,
	"associazione": true, "agenzia": true, "press": true, "university": true,
	"università": true, "universita": true, "polizia": true,
	"carabinieri": true, "guardia": true, "esercito": true, "banca": true,
	"partito": true, "sindacato": true, "federazione": true,
	"confederazione": true, "istituto": true, "ospedale": true,
	"azienda": true, "impresa": true, "gruppo": true,
}

// CorrectSurfaceType corrects a PERSON claim only when the surface itself
// carries a strong, unambiguous place or organisation head. It runs on the
// projection boundary so the entity timeline, annotations and image queries
// consume one kind; it never promotes a single token, because "Villa" or
// "Corte" may be a surname.
func CorrectSurfaceType(surface, current string) string {
	family := Fold(current)
	if family != Person {
		return family
	}
	tokens := surfaceTypeTokens(surface)
	if len(tokens) < 2 {
		return Person
	}
	if surfacePlaceHeads[tokens[0]] || surfacePlaceHeads[tokens[len(tokens)-1]] {
		return Place
	}
	if surfaceOrganizationHeads[tokens[0]] || surfaceOrganizationHeads[tokens[len(tokens)-1]] {
		return Org
	}
	return Person
}

func surfaceTypeTokens(surface string) []string {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(surface)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

// Is reports whether raw folds onto the given canonical family. A blank raw
// type is never any family (it is not evidence of a place, a person or
// anything else).
func Is(raw, family string) bool {
	folded := Fold(raw)
	return folded != "" && folded == family
}

// IsCanonical reports whether the (already folded) value is one of the closed
// canonical families.
func IsCanonical(family string) bool {
	return canonicalSet[family]
}

// Families returns a copy of the closed canonical vocabulary.
func Families() []string {
	return append([]string(nil), canonicalFamilies...)
}

// AliasSpellings returns a copy of the whole alias table (spelling → family).
// It exists for the closure test and for generated documentation; callers that
// need the spellings of ONE type read Aliases.
func AliasSpellings() map[string]string {
	out := make(map[string]string, len(aliases))
	for spelling, family := range aliases {
		out[spelling] = family
	}
	return out
}

// Aliases returns every spelling that folds onto the canonical family,
// canonical spelling first then the legacy spellings in lexical order. An
// unknown type yields just its normalized self (it has no aliases), and a
// blank type yields nil. It is the ONE enumeration compat lookups use.
func Aliases(raw string) []string {
	canonical := Fold(raw)
	if canonical == "" {
		return nil
	}
	if !canonicalSet[canonical] {
		return []string{canonical}
	}
	spellings := make([]string, 0, 4)
	spellings = append(spellings, canonical)
	legacy := make([]string, 0, 4)
	for alias, target := range aliases {
		if target == canonical && alias != canonical {
			legacy = append(legacy, alias)
		}
	}
	sort.Strings(legacy)
	return append(spellings, legacy...)
}
