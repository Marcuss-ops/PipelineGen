package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// shouldGeocodeScriptLocations keeps the compatibility opt-in while also
// activating grounded LOCATION/GPE maps for ordinary entity-extraction runs
// when both production adapters are configured. No lookup happens in a
// deployment that has not configured the geocoder and map plate source.
func (r *Runner) shouldGeocodeScriptLocations(req GenerateRequest) bool {
	if req.MediaPlan.ProviderPolicy.Geocoding == mediadomain.MediaToggleDisabled {
		return false
	}
	if req.MediaPlan.ProviderPolicy.Geocoding == mediadomain.MediaToggleEnabled {
		return true
	}
	return r != nil && r.geocoder != nil && r.mapPlates != nil &&
		!req.EntityExtractionDisabled() && req.NeedsSemanticEnrichment() &&
		req.MediaPlan.Extraction.EntityExtractionRequested() &&
		req.MediaPlan.Extraction.IncludesEntityType("LOCATION")
}

// geocodePlaceAnnotations enriches the prepare branch's grounded scene
// annotations with validated WGS84 coordinates after explicit or configured
// automatic activation. Stable scene and
// annotation order makes request ordering deterministic; duplicate place
// spellings in a run share one lookup and the adapter's durable cache shares
// results across jobs. An enabled policy without an adapter, or an invalid
// provider result, fails closed rather than emitting a map from guessed data.
type geocodeOptions struct {
	sceneTexts map[int]string
	mapsOnly   bool
}

func (r *Runner) geocodePlaceAnnotations(ctx context.Context, annotations map[int]*scriptpkg.SceneAnnotations, language string, options ...geocodeOptions) error {
	if r == nil || r.geocoder == nil {
		return fmt.Errorf("geocoding is enabled but no geocoder adapter is wired")
	}
	indices := make([]int, 0, len(annotations))
	for index := range annotations {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	resolved := make(map[string]capabilitygeocoding.Result)
	for _, index := range indices {
		annotation := annotations[index]
		if annotation == nil {
			continue
		}
		for entityIndex := range annotation.PrimaryEntities {
			if err := geocodeAnnotatedEntity(ctx, r.geocoder, resolved, &annotation.PrimaryEntities[entityIndex], index, language); err != nil {
				return err
			}
		}
		for entityIndex := range annotation.SecondaryEntities {
			if err := geocodeAnnotatedEntity(ctx, r.geocoder, resolved, &annotation.SecondaryEntities[entityIndex], index, language); err != nil {
				return err
			}
		}
		if len(options) > 0 && options[0].mapsOnly {
			if err := geocodeGroundedPlaceNames(ctx, r.geocoder, resolved, annotation, options[0].sceneTexts[index], language, index); err != nil {
				return err
			}
		}
	}
	return nil
}

var capitalizedNameRE = regexp.MustCompile(`(?:[A-ZÀ-ÖØ-Þ][\p{L}\p{M}'’’-]*)(?:[ \t]+[A-ZÀ-ÖØ-Þ][\p{L}\p{M}'’’-]*){0,3}`)

// geocodeGroundedPlaceNames recovers exact proper-name spans that the semantic
// extractor can mislabel as people or omit (for example, a town named in a
// journey narration). It runs only for an explicit maps-only request and only
// accepts names that the configured geocoder classifies at a geographic level.
func geocodeGroundedPlaceNames(ctx context.Context, geocoder capabilitygeocoding.Geocoder, resolved map[string]capabilitygeocoding.Result, annotation *scriptpkg.SceneAnnotations, text, language string, sceneIndex int) error {
	if annotation == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	ignored, err := mapPlaceCandidateIgnoredWords(linguistics.DefaultLexiconOrNil(), language)
	if err != nil {
		return fmt.Errorf("load map candidate exclusion lexicon: %w", err)
	}
	seen := make(map[string]struct{})
	entityIndexes := make(map[string]int)
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...), annotation.SecondaryEntities...) {
		if entity.Geo != nil && (entity.Geo.Scope == "continent" || entity.Geo.Scope == "country" || entity.Geo.Scope == "region" || entity.Geo.Scope == "city") {
			seen[strings.ToLower(strings.TrimSpace(entity.CanonicalName))] = struct{}{}
		}
	}
	for index, entity := range annotation.PrimaryEntities {
		entityIndexes[strings.ToLower(strings.TrimSpace(entity.CanonicalName))] = index
	}
	queries := make([]string, 0, 8)
	for _, match := range capitalizedNameRE.FindAllString(text, -1) {
		name := strings.Join(strings.Fields(strings.TrimSpace(match)), " ")
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, ok := ignored[key]; ok {
			continue
		}
		// Sentence-start capitalized prose is not a place name. Multiword
		// proper names and single words after geographic cues remain eligible.
		if len(strings.Fields(name)) == 1 && len(name) < 4 {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		queries = append(queries, name)
		if len(queries) >= 8 {
			break
		}
	}
	for _, name := range queries {
		cacheKey := strings.ToLower(strings.TrimSpace(language)) + "\x00" + strings.ToLower(name)
		result, ok := resolved[cacheKey]
		if !ok {
			var err error
			result, err = geocoder.Geocode(ctx, capabilitygeocoding.Request{Query: name, Language: language})
			if errors.Is(err, capabilitygeocoding.ErrNoResult) {
				continue
			}
			if err != nil {
				return fmt.Errorf("geocode named map candidate %q in scene %d: %w", name, sceneIndex, err)
			}
			if err := result.Validate(); err != nil {
				return fmt.Errorf("geocode named map candidate %q returned invalid coordinates: %w", name, err)
			}
			resolved[cacheKey] = result
		}
		if result.Scope != "continent" && result.Scope != "country" && result.Scope != "region" && result.Scope != "city" {
			continue
		}
		geo := &scriptpkg.GeoCoordinate{Latitude: result.Latitude, Longitude: result.Longitude, DisplayName: result.DisplayName, Scope: result.Scope}
		if index, exists := entityIndexes[strings.ToLower(name)]; exists {
			annotation.PrimaryEntities[index].Geo = geo
			annotation.PrimaryEntities[index].Type = "LOCATION"
			continue
		}
		annotation.PrimaryEntities = append(annotation.PrimaryEntities, scriptpkg.AnnotatedEntity{
			ID:   "map-place-" + strings.ToLower(strings.ReplaceAll(strings.Fields(name)[0], "'", "")),
			Text: name, CanonicalName: name, Type: "LOCATION", Confidence: 0.85,
			Geo: geo,
		})
	}
	// When a map has multiple city stops, country/region mentions are context
	// for those stops rather than extra pins in the animation.
	cities := 0
	for _, entity := range annotation.PrimaryEntities {
		if entity.Geo != nil && entity.Geo.Scope == "city" {
			cities++
		}
	}
	if cities > 1 {
		kept := annotation.PrimaryEntities[:0]
		for _, entity := range annotation.PrimaryEntities {
			if entity.Geo == nil || entity.Geo.Scope == "city" {
				kept = append(kept, entity)
			}
		}
		annotation.PrimaryEntities = kept
	}
	return nil
}

// mapPlaceCandidateIgnoredWords resolves the canonical language profile used
// to reject sentence-initial function words before treating them as map places.
func mapPlaceCandidateIgnoredWords(registry *linguistics.LexiconRegistry, language string) (map[string]struct{}, error) {
	if registry == nil {
		return nil, fmt.Errorf("lexicon registry is not installed")
	}
	profile, err := registry.ResolveRequired(language)
	if err != nil {
		return nil, err
	}
	return profile.EntityBlocklist, nil
}

func geocodeAnnotatedEntity(ctx context.Context, geocoder capabilitygeocoding.Geocoder, resolved map[string]capabilitygeocoding.Result, entity *scriptpkg.AnnotatedEntity, sceneIndex int, language string) error {
	if entity == nil || !isPlaceEntityType(entity.Type) || entity.Geo != nil {
		return nil
	}
	name := strings.Join(strings.Fields(strings.TrimSpace(entity.CanonicalName)), " ")
	if name == "" {
		return nil
	}
	if isGenericPlaceLabel(name) {
		return nil
	}
	cacheKey := strings.ToLower(strings.TrimSpace(language)) + "\x00" + strings.ToLower(name)
	result, ok := resolved[cacheKey]
	if !ok {
		var err error
		result, err = geocoder.Geocode(ctx, capabilitygeocoding.Request{Query: name, Language: language})
		if err != nil {
			if errors.Is(err, capabilitygeocoding.ErrNoResult) {
				// Unresolvable or malformed generated labels stay visible as
				// ordinary entities, but cannot produce a map pin. Provider,
				// transport, cancellation, and validation errors still fail closed.
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
	return nil
}

func isGenericPlaceLabel(name string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " ")) {
	case "here", "there", "local", "nearby", "home", "workplace", "city", "town", "village", "region", "area", "location":
		return true
	default:
		return false
	}
}

func isPlaceEntityType(entityType string) bool {
	switch scriptpkg.NormalizeAnnotationType(entityType) {
	case "GPE", "LOCATION":
		return true
	default:
		return false
	}
}
