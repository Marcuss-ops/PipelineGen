package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
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
func (r *Runner) geocodePlaceAnnotations(ctx context.Context, annotations map[int]*scriptpkg.SceneAnnotations, language string) error {
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
	}
	return nil
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
