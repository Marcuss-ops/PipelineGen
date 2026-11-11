package geography

import (
	"context"
	"errors"
	"fmt"
	"strings"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func MapPlaceCandidateIgnoredWords(registry *linguistics.LexiconRegistry, language string) (map[string]struct{}, error) {
	if registry == nil {
		return nil, fmt.Errorf("lexicon registry is not installed")
	}
	profile, err := registry.ResolveRequired(language)
	if err != nil {
		return nil, err
	}
	ignored := make(map[string]struct{}, len(profile.EntityBlocklist)+len(profile.StopWords)+len(profile.FunctionWords))
	for word := range profile.EntityBlocklist {
		ignored[word] = struct{}{}
	}
	for word := range profile.StopWords {
		ignored[word] = struct{}{}
	}
	for word := range profile.FunctionWords {
		ignored[word] = struct{}{}
	}
	// Sentence-initial adverbs can look like proper place names after
	// title-casing. Keep this small extension in the same exclusion set so a
	// geocoder homonym cannot turn prose such as "Infine" into a map pin.
	if strings.EqualFold(language, "it") || strings.HasPrefix(strings.ToLower(language), "it-") {
		ignored["infine"] = struct{}{}
		// These common Italian discourse words were misclassified as place
		// entities and geocoded to unrelated homonymous settlements.
		for _, word := range []string{"tale", "tali", "ciò", "cio", "egli", "ella", "esso", "essa", "essi", "esse", "lui", "lei", "loro", "questo", "questa", "questi", "queste", "quello", "quella", "quelli", "quelle", "primo", "prima", "secondo", "seconda", "solo", "usare", "dunque", "inoltre", "durante", "corte", "procura", "cassazione", "tribunale", "stasi", "sempio", "poggi", "alberto", "andrea", "chiara"} {
			ignored[word] = struct{}{}
		}
	}
	return ignored, nil
}

func IgnoredMapCandidate(ignored map[string]struct{}, candidate, language string) bool {
	if len(ignored) == 0 {
		return false
	}
	key := strings.ToLower(normalizeGeocodePlaceQuery(candidate, language))
	_, ok := ignored[key]
	if ok {
		return true
	}
	// A multiword extraction can bypass the exact-word discourse list even
	// though one of its tokens is a known institution or person name. The
	// article is not part of the name: "La Cassazione" is the court, not a
	// geocodable place, and "Suprema Corte" is the same office.
	if strings.EqualFold(language, "it") || strings.HasPrefix(strings.ToLower(language), "it-") {
		for _, word := range []string{"corte", "cassazione", "procura", "tribunale", "stasi", "sempio", "poggi", "alberto", "andrea", "chiara"} {
			if strings.Contains(" "+key+" ", " "+word+" ") {
				return true
			}
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

func geocodeAnnotatedEntity(ctx context.Context, geocoder capabilitygeocoding.Geocoder, resolved map[string]capabilitygeocoding.Result, entity *scriptpkg.AnnotatedEntity, sceneIndex int, language string, geoContext *capabilitygeocoding.Context) error {
	if entity == nil || !isPlaceEntityType(entity.Type) || entity.Geo != nil {
		return nil
	}
	name := normalizeGeocodePlaceQuery(entity.CanonicalName, language)
	if name == "" {
		return nil
	}
	if isGenericPlaceLabel(name) {
		return nil
	}
	lookupContext := capabilitygeocoding.Context{}
	if geoContext != nil {
		lookupContext = *geoContext
	}
	cacheKey := GeocodeCacheKey(language, name, lookupContext)
	result, ok := resolved[cacheKey]
	if !ok {
		var err error
		result, err = geocoder.Geocode(ctx, capabilitygeocoding.Request{Query: name, Language: language, Context: lookupContext})
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
		resolved[cacheKey] = result
	}
	entity.Geo = &scriptpkg.GeoCoordinate{
		Latitude: result.Latitude, Longitude: result.Longitude,
		DisplayName: strings.TrimSpace(result.DisplayName),
		Scope:       result.Scope,
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
func normalizeGeocodePlaceQuery(value, language string) string {
	words := strings.Fields(strings.TrimSpace(value))
	lang := strings.ToLower(strings.TrimSpace(language))
	lang = strings.SplitN(lang, "-", 2)[0]
	if lang == "pt" && len(words) == 2 && strings.EqualFold(words[0], "o") && strings.EqualFold(words[1], "Brasil") {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

func isGenericPlaceLabel(name string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " ")) {
	case "here", "there", "local", "nearby", "home", "workplace", "city", "town", "village", "region", "area", "location",
		"paese", "paesi", "nesta", "neste", "nessa", "nesse", "esta", "este", "essa", "esse",
		"dopo", "capire":
		return true
	default:
		return false
	}
}
