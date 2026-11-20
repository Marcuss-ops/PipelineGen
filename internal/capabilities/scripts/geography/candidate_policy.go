package geography

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MapCandidateExclusions is the map/geocode gate projected from one language
// profile. Words reject a whole normalized query; Tokens reject any candidate
// that contains them, which is what catches "La Cassazione" and "Suprema
// Corte" once the article is gone and the extraction is multiword.
//
// The data lives in BrainRust/lexicon/languages/<lang>/ (see
// place_candidate_blocklist.txt and place_candidate_token_blocklist.txt)
// and reaches Go through the generated profile snapshot: no language word
// list is hardcoded here.
type MapCandidateExclusions struct {
	Words  map[string]struct{}
	Tokens map[string]struct{}
}

// MapCandidateExclusionsFor resolves the map candidate gate for a language.
// A language whose sources carry no map blocklist yields an empty gate, never
// another language's words; a missing registry or language fails closed.
func MapCandidateExclusionsFor(registry *linguistics.Profiles, language string) (*MapCandidateExclusions, error) {
	if registry == nil {
		return nil, fmt.Errorf("lexicon registry is not installed")
	}
	profile, err := registry.ResolveRequired(language)
	if err != nil {
		return nil, err
	}
	words := make(map[string]struct{}, len(profile.EntityBlocklist)+len(profile.StopWords)+len(profile.FunctionWords)+len(profile.PlaceCandidateBlocklist))
	for word := range profile.EntityBlocklist {
		words[word] = struct{}{}
	}
	for word := range profile.StopWords {
		words[word] = struct{}{}
	}
	for word := range profile.FunctionWords {
		words[word] = struct{}{}
	}
	for word := range profile.PlaceCandidateBlocklist {
		words[word] = struct{}{}
	}
	tokens := make(map[string]struct{}, len(profile.PlaceCandidateTokenBlocklist))
	for token := range profile.PlaceCandidateTokenBlocklist {
		tokens[token] = struct{}{}
	}
	return &MapCandidateExclusions{Words: words, Tokens: tokens}, nil
}

// MapPlaceCandidateIgnoredWords returns the whole-query exclusion set only.
// Callers that also need the token rule must use MapCandidateExclusionsFor.
func MapPlaceCandidateIgnoredWords(registry *linguistics.Profiles, language string) (map[string]struct{}, error) {
	exclusions, err := MapCandidateExclusionsFor(registry, language)
	if err != nil {
		return nil, err
	}
	return exclusions.Words, nil
}

// IgnoredMapCandidate reports whether a candidate must never reach the
// geocoder. Exclusions may come from any language profile; the token rule is
// data, so a language without tokens simply has none.
func IgnoredMapCandidate(exclusions *MapCandidateExclusions, candidate, language string) bool {
	if exclusions == nil || (len(exclusions.Words) == 0 && len(exclusions.Tokens) == 0) {
		return false
	}
	key := strings.ToLower(normalizeGeocodePlaceQuery(candidate, language))
	if _, ok := exclusions.Words[key]; ok {
		return true
	}
	if len(exclusions.Tokens) == 0 {
		return false
	}
	padded := " " + key + " "
	for token := range exclusions.Tokens {
		if strings.Contains(padded, " "+token+" ") {
			return true
		}
	}
	return false
}

func groundedPersonName(annotation *scriptpkg.SceneAnnotations, candidate string) bool {
	if annotation == nil {
		return false
	}
	normalize := func(value string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
	}
	name := normalize(candidate)
	if name == "" {
		return false
	}
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...), annotation.SecondaryEntities...) {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "PERSON") {
			continue
		}
		person := normalize(entity.CanonicalName)
		if person == name || strings.Contains(" "+name+" ", " "+person+" ") || strings.Contains(" "+person+" ", " "+name+" ") {
			return true
		}
	}
	return false
}

func geocodeAnnotatedEntity(ctx context.Context, geocoder capabilitygeocoding.Geocoder, state *enrichState, entity *scriptpkg.AnnotatedEntity, sceneIndex int, language, level string, geoContext *capabilitygeocoding.Context) error {
	if entity == nil || !isPlaceEntityType(entity.Type) || entity.Geo != nil {
		return nil
	}
	name := normalizeGeocodePlaceQuery(entity.CanonicalName, language)
	if name == "" {
		return nil
	}
	if isGenericPlaceLabel(name, language) {
		return nil
	}
	if state.nameRefused(name) {
		// Already certified as outside the requested map level: the scope
		// cannot change between retry frames, so no lookup is paid for it.
		return nil
	}
	lookupContext := placeLookupContext(name, language, geoContext)
	cacheKey := GeocodeCacheKey(language, name, lookupContext)
	if state.refused(cacheKey) {
		return nil
	}
	result, ok := state.resolved[cacheKey]
	if !ok {
		var err error
		result, err = geocoder.Geocode(ctx, capabilitygeocoding.Request{Query: name, Language: language, Context: lookupContext, Level: level})
		if err != nil {
			if errors.Is(err, capabilitygeocoding.ErrNoResult) {
				// Unresolvable, ambiguous-without-context, or malformed generated
				// labels stay visible as ordinary entities, but cannot produce a
				// map pin. Provider, transport, cancellation, and validation
				// errors still fail closed.
				return nil
			}
			return fmt.Errorf("geocode grounded place %q in scene annotation %d: %w", name, sceneIndex, err)
		}
		if err := result.Validate(); err != nil {
			return fmt.Errorf("geocode grounded place %q returned invalid coordinates: %w", name, err)
		}
		if level != "" && !capabilitygeocoding.ScopeFitsLevel(result.Scope, level) {
			// An adapter that ignores the level hint can still answer with a
			// place this map cannot render. Leave the entity unlocated instead
			// of handing the planner a coordinate it would drop, and remember
			// the refusal so the retry frames do not pay for it again.
			state.refuse(cacheKey)
			return nil
		}
		state.resolved[cacheKey] = result
	}
	entity.Geo = &scriptpkg.GeoCoordinate{
		Latitude: result.Latitude, Longitude: result.Longitude,
		DisplayName: strings.TrimSpace(result.DisplayName),
		Scope:       result.Scope,
		Country:     strings.TrimSpace(result.Country),
		CountryCode: strings.ToLower(strings.TrimSpace(result.CountryCode)),
		AdminRegion: strings.TrimSpace(result.AdminRegion),
	}
	if name != strings.Join(strings.Fields(strings.TrimSpace(entity.CanonicalName)), " ") {
		entity.CanonicalName = name
		entity.Text = name
	}
	// A resolved country/region becomes context for the finer places the
	// scene names after it (never the reverse: context is only ever grounded).
	AdoptGeoContext(geoContext, entity)
	return nil
}

// normalizeGeocodePlaceQuery removes the Portuguese sentence article in the
// observed country phrase "O Brasil". Nominatim ranked a locality in Galicia
// for that full phrase; the query should name the country itself, Brasil.
//
// It also removes the Italian article from a known administrative region:
// "La Toscana" matched a town in Venezuela before the Italian region, while
// "Toscana" resolves to the region. The article is stripped only when the
// remainder is a known Italian region, so "La Spezia" and "L'Aquila" keep
// their official names.
func normalizeGeocodePlaceQuery(value, language string) string {
	words := strings.Fields(strings.TrimSpace(value))
	lang := strings.ToLower(strings.TrimSpace(language))
	lang = strings.SplitN(lang, "-", 2)[0]
	if lang == "pt" && len(words) == 2 && strings.EqualFold(words[0], "o") {
		if _, isCountry := geoNameWordSet(lang, "country_names")[strings.ToLower(words[1])]; isCountry {
			words = words[1:]
		}
	}
	if lang == "it" {
		words = stripItalianRegionArticle(words, lang)
	}
	return strings.Join(words, " ")
}

// geoNameWordSet resolves one geographic vocabulary set ("admin_regions",
// "country_names") from the language pack. The data is curated in
// BrainRust/lexicon/languages/<lang>/ and regenerated into the profile
// snapshot: no region or country list is written in Go. A language without
// the file has no set, never another language's names.
func geoNameWordSet(language, set string) map[string]struct{} {
	profile, err := lexiconSnapshot().ResolveRequired(language)
	if err != nil {
		return nil
	}
	return profile.WordSets[set]
}

// lexiconSnapshot returns the installed lexicon registry, or the embedded
// Rust-generated snapshot when the process has not bootstrapped one yet. The
// normalization helpers run before any gate and must stay deterministic in
// tests and in tools; they still read only generated data.
func lexiconSnapshot() *linguistics.Profiles {
	if registry := linguistics.DefaultProfilesOrNil(); registry != nil {
		return registry
	}
	embeddedProfilesOnce.Do(func() {
		embeddedProfiles, _ = linguistics.NewProfiles()
	})
	return embeddedProfiles
}

var (
	embeddedProfilesOnce sync.Once
	embeddedProfiles     *linguistics.Profiles
)

// countryNamesFor returns the country exonyms that suppress the scene's
// country as lookup context. The set is language-owned; a language that has
// not curated it yet falls back to the shared set in the Italian pack, which
// is the historical authority for this behaviour.
func countryNamesFor(language string) map[string]struct{} {
	if names := geoNameWordSet(language, "country_names"); len(names) > 0 {
		return names
	}
	return geoNameWordSet("it", "country_names")
}

func isKnownItalianArticlePlace(name, language string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if _, ok := geoNameWordSet(language, "admin_regions")[name]; ok {
		return true
	}
	_, ok := countryNamesFor(language)[name]
	return ok
}

// isKnownCountryName reports whether a normalized query is itself a country.
// A country is a global place: carrying a previously resolved country as
// provider context either filters it out ("Francia" under country=Italia) or
// resolves a namesake in the wrong country ("Svizzera" under Germany).
func isKnownCountryName(name, language string) bool {
	name = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " "))
	_, ok := countryNamesFor(language)[name]
	return ok
}

// placeLookupContext is the grounded context actually sent to the provider.
// City and region names still inherit the scene's country; a country query is
// resolved without that frame because it is the frame itself.
func placeLookupContext(name, language string, geoContext *capabilitygeocoding.Context) capabilitygeocoding.Context {
	if isKnownCountryName(name, language) {
		return capabilitygeocoding.Context{}
	}
	if geoContext == nil {
		return capabilitygeocoding.Context{}
	}
	return *geoContext
}

// stripItalianRegionArticle removes a leading Italian article only when the
// remaining phrase is a known administrative region or country. It never
// rewrites a city or a generic proper name.
func stripItalianRegionArticle(words []string, language string) []string {
	if len(words) == 0 {
		return words
	}
	first := strings.ToLower(words[0])
	if len(words) > 1 {
		switch first {
		case "la", "lo", "il", "le":
			rest := strings.Join(words[1:], " ")
			if isKnownItalianArticlePlace(rest, language) {
				return words[1:]
			}
		}
	}
	for _, prefix := range []string{"l'", "l’"} {
		if !strings.HasPrefix(first, prefix) {
			continue
		}
		rest := words[0][len(prefix):]
		if len(words) > 1 {
			rest += " " + strings.Join(words[1:], " ")
		}
		if isKnownItalianArticlePlace(rest, language) {
			out := make([]string, 0, len(words))
			out = append(out, rest)
			return append(out, words[1:]...)
		}
	}
	return words
}

// isGenericPlaceLabel reports whether a name is a common noun or a deictic,
// so it can never become a map pin. The vocabulary is curated per language
// (generic_place_labels.txt) plus the cross-linguistic fallback set, which is
// why a shared word like "city" is listed once and applies everywhere.
func isGenericPlaceLabel(name, language string) bool {
	key := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " "))
	if key == "" {
		return false
	}
	if _, ok := geoNameWordSet(language, "generic_place_labels")[key]; ok {
		return true
	}
	_, ok := geoNameWordSet("fallback", "generic_place_labels")[key]
	return ok
}
