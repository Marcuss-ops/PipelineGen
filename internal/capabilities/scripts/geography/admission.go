package geography

import (
	"context"
	"sort"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// GeoRejectionReason names why a candidate place was not admitted to the
// lookup. It is diagnostic only: the caller needs the decision, not the prose.
type GeoRejectionReason string

const (
	GeoAdmitted               GeoRejectionReason = ""
	GeoRejectedNotPlace       GeoRejectionReason = "not_a_place"
	GeoRejectedIgnoredName    GeoRejectionReason = "ignored_candidate"
	GeoRejectedPersonName     GeoRejectionReason = "person_name_fragment"
	GeoRejectedUngroundedName GeoRejectionReason = "name_not_grounded_in_text"
)

// GeoAdmissionDecision is the single answer every admission path consumes:
// the per-entity pass, the retry pass and the maps-only prefilter all ask the
// same policy. ClearGeo means the candidate must lose coordinates already
// attached by an earlier surface; Eligible means it may be looked up now;
// Retry means it is still worth a context-aware second attempt.
type GeoAdmissionDecision struct {
	Eligible bool
	Reason   GeoRejectionReason
	ClearGeo bool
	Retry    bool
}

// sceneAdmission is the per-scene admission policy. It is built once per scene
// so the rules — exclusion lexicon, person-name evidence, grounding in the
// scene text and the run-scoped refusals — cannot drift between the primary
// list, the secondary list and the retry frames.
type sceneAdmission struct {
	mapsOnly     bool
	language     string
	level        string
	sceneText    string
	annotation   *scriptpkg.SceneAnnotations
	exclusions   *MapCandidateExclusions
	personTokens map[string]struct{}
	state        *enrichState
}

// decide applies the one admission policy. requirePlace keeps the historical
// distinction between the first pass (which looks at every annotation) and the
// retry pass (which only retries place-typed entities).
func (p sceneAdmission) decide(entity scriptpkg.AnnotatedEntity, requirePlace bool) GeoAdmissionDecision {
	if requirePlace && !isPlaceEntityType(entity.Type) {
		return GeoAdmissionDecision{Reason: GeoRejectedNotPlace}
	}
	if p.mapsOnly {
		if IgnoredMapCandidate(p.exclusions, entity.CanonicalName, p.language) {
			return GeoAdmissionDecision{Reason: GeoRejectedIgnoredName, ClearGeo: true}
		}
		if personNameFragment(p.annotation, entity.CanonicalName) ||
			personNameToken(p.personTokens, entity.CanonicalName) ||
			capitalizedNameFragment(p.sceneText, entity.CanonicalName) {
			return GeoAdmissionDecision{Reason: GeoRejectedPersonName, ClearGeo: true}
		}
		if !containsGroundedPlaceName(p.sceneText, entity.CanonicalName) {
			return GeoAdmissionDecision{Reason: GeoRejectedUngroundedName}
		}
	}
	return GeoAdmissionDecision{Eligible: true, Retry: p.retryable(entity)}
}

// retryable is the second half of the decision: an unresolved place that was
// never terminally refused by the run may still resolve under the scene's
// grounded frames.
func (p sceneAdmission) retryable(entity scriptpkg.AnnotatedEntity) bool {
	if entity.Geo != nil {
		return false
	}
	return !p.state.nameRefused(entity.CanonicalName)
}

// prefilterOutOfLevel drops coordinates that an earlier surface already
// attached at the wrong cartographic level. A scope is certified evidence, so
// the refusal is terminal for the run instead of a lookup to retry.
func (p sceneAdmission) prefilterOutOfLevel(annotation *scriptpkg.SceneAnnotations) {
	if annotation == nil || !p.mapsOnly {
		return
	}
	filter := func(entities []scriptpkg.AnnotatedEntity) {
		for index := range entities {
			entity := &entities[index]
			if !isPlaceEntityType(entity.Type) {
				continue
			}
			if isGenericPlaceLabel(entity.CanonicalName, p.language) {
				entity.Geo = nil
				continue
			}
			if outOfMapLevel(entity.Geo, p.level) {
				p.state.refuseName(entity.CanonicalName)
				p.state.refuseName(normalizeGeocodePlaceQuery(entity.CanonicalName, p.language))
				entity.Geo = nil
			}
		}
	}
	filter(annotation.PrimaryEntities)
	filter(annotation.SecondaryEntities)
}

// enrichRequest carries the run-scoped inputs every admission path needs.
type enrichRequest struct {
	geocoder     capabilitygeocoding.Geocoder
	language     string
	level        string
	mapsOnly     bool
	sceneTexts   map[int]string
	exclusions   *MapCandidateExclusions
	personTokens map[string]struct{}
}

func sortedSceneIndexes(annotations map[int]*scriptpkg.SceneAnnotations) []int {
	indices := make([]int, 0, len(annotations))
	for index := range annotations {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	return indices
}

func (r enrichRequest) policy(state *enrichState, annotation *scriptpkg.SceneAnnotations, sceneIndex int) sceneAdmission {
	return sceneAdmission{
		mapsOnly:     r.mapsOnly,
		language:     r.language,
		level:        r.level,
		sceneText:    r.sceneTexts[sceneIndex],
		annotation:   annotation,
		exclusions:   r.exclusions,
		personTokens: r.personTokens,
		state:        state,
	}
}

// enrichEntities is the first pass: one policy decision per entity, then one
// lookup. Clearing coordinates is the decision's job, not the loop's.
func (r enrichRequest) enrichEntities(ctx context.Context, state *enrichState, policy sceneAdmission, entities []scriptpkg.AnnotatedEntity, sceneIndex int, geoContext *capabilitygeocoding.Context) error {
	for index := range entities {
		decision := policy.decide(entities[index], false)
		if decision.ClearGeo {
			entities[index].Geo = nil
		}
		if !decision.Eligible {
			continue
		}
		if err := geocodeAnnotatedEntity(ctx, r.geocoder, state, &entities[index], sceneIndex, r.language, r.level, geoContext); err != nil {
			return err
		}
	}
	return nil
}

// retryEntities is the second pass: same policy, place-typed unresolved
// candidates only, through the scene's grounded frames.
func (r enrichRequest) retryEntities(ctx context.Context, state *enrichState, policy sceneAdmission, entities []scriptpkg.AnnotatedEntity, sceneIndex int, frames []capabilitygeocoding.Context, geoContext *capabilitygeocoding.Context) error {
	for index := range entities {
		decision := policy.decide(entities[index], true)
		if !decision.Eligible || !decision.Retry {
			continue
		}
		if err := geocodeWithSceneFrames(ctx, r.geocoder, state, &entities[index], sceneIndex, r.language, r.level, frames, geoContext); err != nil {
			return err
		}
	}
	return nil
}
