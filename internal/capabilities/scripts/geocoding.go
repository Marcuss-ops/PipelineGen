package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// geocodePlaceAnnotations enriches the prepare branch's grounded scene
// annotations with validated WGS84 coordinates. It is called only after the
// request explicitly sets provider_policy.geocoding=enabled. Stable scene and
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
	cacheKey := strings.ToLower(strings.TrimSpace(language)) + "\x00" + strings.ToLower(name)
	result, ok := resolved[cacheKey]
	if !ok {
		var err error
		result, err = geocoder.Geocode(ctx, capabilitygeocoding.Request{Query: name, Language: language})
		if err != nil {
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

func isPlaceEntityType(entityType string) bool {
	switch scriptpkg.NormalizeAnnotationType(entityType) {
	case "GPE", "LOCATION":
		return true
	default:
		return false
	}
}
