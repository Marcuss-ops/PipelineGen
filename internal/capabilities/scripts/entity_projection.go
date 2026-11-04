// Package scriptgeneration — entity_projection.go owns the deterministic
// projection of the incremental VidRush enrichment results onto the durable
// result's typed entity aggregate (persons / places / concepts), the legacy
// compatibility projection derived from it, the SINGLE derivation of an
// annotation entity's identity, and the entity-image overlay composition
// (grouping nearby portraits into composites of up to five items with per-layer
// certified motions).
//
// The durable runner consumes these helpers after the final barrier so a
// SUCCEEDED run exposes the entities its extraction backend actually produced —
// the same typed buckets the batch flow projects into Artifacts.Entities — and
// so every downstream join (overlay intent, entity-card media index, semantic
// render bundle) resolves an entity's identity through ONE function instead of
// re-deriving it from a display name.
package scriptgeneration

import (
	"strconv"
	"strings"
	"unicode"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// aggregateEntityResult merges the fenced per-scene VidRush segment results
// into one canonical EntityResult. Classification is deterministic and matches
// the legacy batch projection: PERSON → persons; LOCATION/PLACE/COUNTRY/CITY →
// places; every other type → concepts. Repeated mentions collapse onto one row
// per canonical (type, value), and a surname-only PERSON mention resolves onto
// the grounded full name of its segment. Returns nil when no segment produced
// any entity (or no segments were passed), so the durable surface omits the
// block instead of exposing an empty aggregate.
func aggregateEntityResult(segments []scriptpkg.VidRushSegmentResult) *scriptpkg.EntityResult {
	agg := &scriptpkg.EntityResult{}
	seenPhrases := make(map[string]struct{})
	seenWords := make(map[string]struct{})
	seenPersons := make(map[string]int)
	seenPlaces := make(map[string]int)
	seenConcepts := make(map[string]int)
	// addEntity keeps ONE row per canonical (type, value). A repeated mention
	// is not a second entity: the strongest score wins and the row keeps its
	// first position, so the aggregate stays deterministic across arrival
	// order.
	addEntity := func(bucket *[]scriptpkg.Entity, seen map[string]int, entity scriptpkg.Entity) {
		key := entitytype.Fold(entity.Type) + "\x00" + strings.ToLower(entity.Value)
		if index, exists := seen[key]; exists {
			if entity.Score > (*bucket)[index].Score {
				(*bucket)[index] = entity
			}
			return
		}
		seen[key] = len(*bucket)
		*bucket = append(*bucket, entity)
	}
	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			text = seg.SourceText
		}
		for _, phrase := range seg.Insights.ImportantPhrases {
			phrase = normalizeImportantPhrase(text, phrase)
			key := strings.ToLower(phrase)
			if phrase != "" {
				if _, exists := seenPhrases[key]; !exists {
					seenPhrases[key] = struct{}{}
					agg.ImportantPhrases = append(agg.ImportantPhrases, phrase)
				}
			}
		}
		for _, word := range seg.Insights.ImportantWords {
			word = strings.TrimSpace(word)
			key := strings.ToLower(word)
			if word != "" {
				if _, exists := seenWords[key]; !exists {
					seenWords[key] = struct{}{}
					agg.ImportantWords = append(agg.ImportantWords, word)
				}
			}
		}
		for _, ent := range seg.Insights.Entities {
			value := strings.TrimSpace(ent.Value)
			if value == "" {
				continue
			}
			if entitytype.Is(ent.Type, entitytype.Person) {
				// One identity per person: a surname-only mention that this
				// segment resolves to a grounded full name collapses onto it,
				// exactly like the annotation projection.
				value = expandPersonName(text, value)
			}
			entity := scriptpkg.Entity{Value: value, Type: ent.Type, Score: float32(ent.Confidence)}
			switch {
			case entitytype.Is(ent.Type, entitytype.Person):
				addEntity(&agg.Persons, seenPersons, entity)
			case entitytype.Is(ent.Type, entitytype.Place):
				addEntity(&agg.Places, seenPlaces, entity)
			default:
				addEntity(&agg.Concepts, seenConcepts, entity)
			}
		}
	}
	// Cross-segment pass: a scene that says only "Stasi" cannot resolve the
	// surname inside its own text, so the aggregate must match it against the
	// unique grounded full name elsewhere in the run.
	agg.Persons = unifySurnameOnlyPersons(agg.Persons)
	agg.Concepts = canonicalizeDateConcepts(agg.Concepts)
	if len(agg.Persons)+len(agg.Places)+len(agg.Concepts)+
		len(agg.ImportantPhrases)+len(agg.ImportantWords) == 0 {
		return nil
	}
	return agg
}

// unifySurnameOnlyPersons collapses a surname-only PERSON onto the unique
// grounded full name that ends with that surname anywhere in the run. The
// same-segment expansion cannot see scenes that mention only the surname
// ("Stasi" in scene 03 and 10); this pass closes that gap without inventing an
// identity: with zero or multiple full-name candidates the surname stays
// untouched.
func unifySurnameOnlyPersons(persons []scriptpkg.Entity) []scriptpkg.Entity {
	fullBySurname := make(map[string]map[string]string)
	for _, person := range persons {
		fields := strings.Fields(strings.TrimSpace(person.Value))
		if len(fields) < 2 {
			continue
		}
		surname := normalizePersonToken(fields[len(fields)-1])
		if surname == "" {
			continue
		}
		if fullBySurname[surname] == nil {
			fullBySurname[surname] = make(map[string]string)
		}
		full := strings.Join(fields, " ")
		fullBySurname[surname][strings.ToLower(full)] = full
	}
	out := make([]scriptpkg.Entity, 0, len(persons))
	seen := make(map[string]int, len(persons))
	for _, person := range persons {
		fields := strings.Fields(strings.TrimSpace(person.Value))
		if len(fields) == 1 {
			if names := fullBySurname[normalizePersonToken(fields[0])]; len(names) == 1 {
				for _, full := range names {
					person.Value = full
				}
			}
		}
		key := strings.ToLower(strings.TrimSpace(person.Value))
		if index, exists := seen[key]; exists {
			if person.Score > out[index].Score {
				out[index].Score = person.Score
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, person)
	}
	return out
}

var italianMonthNumbers = map[string]int{
	"gennaio": 1, "febbraio": 2, "marzo": 3, "aprile": 4,
	"maggio": 5, "giugno": 6, "luglio": 7, "agosto": 8,
	"settembre": 9, "ottobre": 10, "novembre": 11, "dicembre": 12,
}

type parsedItalianDate struct {
	Day, Month, Year int
	Surface          string
}

// parseItalianDate recognizes the exact Italian date surfaces the extractor
// emits: day month year, day month, month year and a bare four-digit year.
// Anything else stays an opaque DATE rather than being guessed.
func parseItalianDate(value string) (parsedItalianDate, bool) {
	surface := strings.TrimSpace(value)
	fields := strings.Fields(strings.ToLower(strings.Trim(surface, ".,;:")))
	parsed := parsedItalianDate{Surface: surface}
	number := func(token string) (int, bool) {
		n, err := strconv.Atoi(token)
		return n, err == nil
	}
	switch len(fields) {
	case 1:
		if year, ok := number(fields[0]); ok && year >= 1000 && year <= 2100 {
			parsed.Year = year
			return parsed, true
		}
	case 2:
		if day, ok := number(fields[0]); ok {
			month, ok := italianMonthNumbers[fields[1]]
			if !ok || day < 1 || day > 31 {
				return parsedItalianDate{}, false
			}
			parsed.Day, parsed.Month = day, month
			return parsed, true
		}
		if month, ok := italianMonthNumbers[fields[0]]; ok {
			year, ok := number(fields[1])
			if !ok || year < 1000 || year > 2100 {
				return parsedItalianDate{}, false
			}
			parsed.Month, parsed.Year = month, year
			return parsed, true
		}
	case 3:
		day, ok := number(fields[0])
		if !ok {
			return parsedItalianDate{}, false
		}
		month, ok := italianMonthNumbers[fields[1]]
		if !ok {
			return parsedItalianDate{}, false
		}
		year, ok := number(fields[2])
		if !ok || day < 1 || day > 31 || year < 1000 || year > 2100 {
			return parsedItalianDate{}, false
		}
		parsed.Day, parsed.Month, parsed.Year = day, month, year
		return parsed, true
	}
	return parsedItalianDate{}, false
}

// canonicalizeDateConcepts maps partial DATE surfaces onto the unique full
// date that contains them and drops a partial when the match is ambiguous or
// absent. A bare year remains a distinct display unit. This is the aggregate
// cleanup for the 22-row DATE list: six full dates plus five years, instead of
// the same facts repeated as day/month fragments.
func canonicalizeDateConcepts(concepts []scriptpkg.Entity) []scriptpkg.Entity {
	fullDates := make([]parsedItalianDate, 0, len(concepts))
	for _, concept := range concepts {
		if !strings.EqualFold(concept.Type, "DATE") {
			continue
		}
		if parsed, ok := parseItalianDate(concept.Value); ok && parsed.Day > 0 && parsed.Month > 0 && parsed.Year > 0 {
			fullDates = append(fullDates, parsed)
		}
	}
	out := make([]scriptpkg.Entity, 0, len(concepts))
	seen := make(map[string]int, len(concepts))
	for _, concept := range concepts {
		if strings.EqualFold(concept.Type, "DATE") {
			parsed, ok := parseItalianDate(concept.Value)
			if ok {
				switch {
				case parsed.Day > 0 && parsed.Month > 0 && parsed.Year > 0:
					// canonical full date: keep
				case parsed.Day > 0 && parsed.Month > 0:
					canonical, found := uniqueFullDate(fullDates, parsed.Day, parsed.Month, 0)
					if !found {
						continue
					}
					concept.Value = canonical
				case parsed.Month > 0 && parsed.Year > 0:
					canonical, found := uniqueFullDate(fullDates, 0, parsed.Month, parsed.Year)
					if !found {
						continue
					}
					concept.Value = canonical
				case parsed.Year > 0:
					// A year is a distinct display unit; keep it.
				default:
					continue
				}
			}
		}
		key := entitytype.Fold(concept.Type) + "\x00" + strings.ToLower(concept.Value)
		if index, exists := seen[key]; exists {
			if concept.Score > out[index].Score {
				out[index].Score = concept.Score
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, concept)
	}
	return out
}

func uniqueFullDate(dates []parsedItalianDate, day, month, year int) (string, bool) {
	matches := make(map[string]struct{})
	for _, date := range dates {
		if day > 0 && date.Day != day {
			continue
		}
		if month > 0 && date.Month != month {
			continue
		}
		if year > 0 && date.Year != year {
			continue
		}
		matches[date.Surface] = struct{}{}
	}
	if len(matches) != 1 {
		return "", false
	}
	for surface := range matches {
		return surface, true
	}
	return "", false
}

// normalizeImportantPhrase repairs the two selector artefacts that reached the
// durable surface: a lowercase sentence start and a phrase that begins in the
// middle of its sentence ("rinvio significa…"). It prepends at most two words
// from the sentence start and capitalizes the first rune; the result remains a
// verbatim span of the source text.
func normalizeImportantPhrase(text, phrase string) string {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return ""
	}
	span, ok := findEntitySpan(text, phrase)
	if !ok {
		return capitalizeFirstRune(phrase)
	}
	runes := []rune(text)
	sentenceStart := 0
scan:
	for i := span.StartRune - 1; i >= 0; i-- {
		switch runes[i] {
		case '.', '!', '?', '\n':
			sentenceStart = i + 1
			break scan
		}
	}
	candidate := span.Text
	if prefix := strings.TrimSpace(string(runes[sentenceStart:span.StartRune])); prefix != "" {
		words := strings.Fields(prefix)
		if len(words) <= 2 {
			candidate = strings.Join(words, " ") + " " + span.Text
		}
	}
	return capitalizeFirstRune(candidate)
}

func capitalizeFirstRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// dropSurnameOnlyAnnotationDuplicates removes a one-token PERSON annotation
// when exactly one grounded multi-token PERSON in the run ends with that
// token. The full name is already spoken and annotated elsewhere; keeping the
// surname-only copy would mint a second identity and an extra entity card.
// With zero or multiple candidate full names the surname is preserved.
func dropSurnameOnlyAnnotationDuplicates(annotations map[int]*scriptpkg.SceneAnnotations) {
	fullBySurname := make(map[string]map[string]struct{})
	for _, ann := range annotations {
		if ann == nil {
			continue
		}
		for _, entity := range ann.PrimaryEntities {
			if !strings.EqualFold(entity.Type, "PERSON") {
				continue
			}
			name := strings.TrimSpace(entity.CanonicalName)
			if name == "" {
				name = strings.TrimSpace(entity.Text)
			}
			fields := strings.Fields(name)
			if len(fields) < 2 {
				continue
			}
			surname := normalizePersonToken(fields[len(fields)-1])
			if surname == "" {
				continue
			}
			if fullBySurname[surname] == nil {
				fullBySurname[surname] = make(map[string]struct{})
			}
			fullBySurname[surname][strings.ToLower(strings.Join(fields, " "))] = struct{}{}
		}
	}
	for _, ann := range annotations {
		if ann == nil {
			continue
		}
		kept := ann.PrimaryEntities[:0]
		for _, entity := range ann.PrimaryEntities {
			if strings.EqualFold(entity.Type, "PERSON") {
				name := strings.TrimSpace(entity.CanonicalName)
				if name == "" {
					name = strings.TrimSpace(entity.Text)
				}
				fields := strings.Fields(name)
				if len(fields) == 1 {
					if names := fullBySurname[normalizePersonToken(fields[0])]; len(names) == 1 {
						continue
					}
				}
			}
			kept = append(kept, entity)
		}
		// Keep the annotation object even when the dropped surname was its only
		// entity: downstream geocoding uses the scene annotation as its carrier
		// and must still be able to add a place from the same text.
		ann.PrimaryEntities = kept
	}
}

// GenerateArtifacts contains compatibility projections that are derived from
// the canonical durable result. It is not an independent entity source.
type GenerateArtifacts struct {
	Entities *scriptpkg.EntityResult `json:"entities,omitempty"`
}

// projectEntityCompatibility restores the legacy wire surfaces consumed by
// existing E2E clients while keeping EntityResult and VidRush segment results
// as the only semantic sources of truth.
func projectEntityCompatibility(result *GenerateResult, segments []scriptpkg.VidRushSegmentResult) {
	if result == nil {
		return
	}
	if len(segments) > 0 {
		result.Segments = append([]scriptpkg.VidRushSegmentResult(nil), segments...)
	}
	if result.Entities != nil {
		result.Artifacts = &GenerateArtifacts{Entities: result.Entities}
	}
}

// annotationCanonicalEntityID returns the canonical, readable identity
// ("person:floyd-mayweather", "gpe:los-angeles") of one annotation entity.
//
// Precedence — one identity owner, never a second spelling:
//
//  1. the id the Image Search Intent resolver stamped on the annotation
//     (AnnotatedEntity.CanonicalEntityID): the DISAMBIGUATED decision (an
//     Italian surface that canonicalizes to the English identity);
//  2. the deterministic derivation from (type, canonical name) through the
//     canonical identity owner (capabilities/entities.CanonicalEntityID), which
//     is exactly the derivation the image-search resolver and the entity-image
//     catalog use, so an unstamped annotation still yields the SAME id instead
//     of a locally invented one.
//
// Empty only when the annotation carries no usable (type, name); a caller must
// then treat the entity as identity-less rather than mint an id itself. Every
// consumer that needs the canonical id (overlay intent, entity card media
// index, semantic render bundle) joins through this one function.
func annotationCanonicalEntityID(entity scriptpkg.AnnotatedEntity) string {
	if id := strings.TrimSpace(entity.CanonicalEntityID); id != "" {
		return id
	}
	return capabilityentities.CanonicalEntityID(entity.Type, entity.CanonicalName)
}

// annotationStableEntityID returns the content-addressed machine identity
// ("ent_<16 hex>") of one annotation entity — the id the canonical entity
// timeline stamps on every occurrence and the render plane keys plan items by.
// It is derived through the same single owner as annotationCanonicalEntityID,
// so the annotation surface, the overlay intent and the plan item always agree
// WITHOUT any consumer comparing display names.
func annotationStableEntityID(entity scriptpkg.AnnotatedEntity) string {
	return capabilityentities.StableEntityID(entity.Type, entity.CanonicalName)
}

// entityImageMergeGapMS is the largest gap between certified mention anchors
// that still reads as one shared visual sequence. Grouping prevents nearby
// images from being rendered as independent overlays that cover one another.
const (
	entityImageMergeGapMS       int64 = 5_000
	entityImageHoldMS           int64 = 1_500
	entityImageTravelMS         int64 = 2_000
	maxEntityImageGroup               = 5
	maxNamedEntityCardsPerScene       = 2
)
