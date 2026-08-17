package entities

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// entityOverlayKind maps the canonical NLP entity type onto the canonical
// overlay kind (the registry key). PERSON → entity_card, ORG → organization,
// GPE/LOCATION → location, NUMBER → number, QUOTE → quote, PRODUCT → product,
// LOGO → logo, everything else → concept. The kind→template and
// kind→renderer mapping lives in ChrononOverlayRegistry (the single owner);
// this package only translates the NLP type vocabulary into the registry's
// kind vocabulary.
//
// PRODUCT and LOGO are asset-driven kinds: an occurrence resolved from the
// timeline carries no asset refs, so its item compiles only after a caller
// attaches the image (the planner is the surface that does that); the
// registry contract fails closed without an asset.
func entityOverlayKind(entityType string) capabilityoverlay.OverlayKind {
	switch strings.ToUpper(strings.TrimSpace(entityType)) {
	case "PERSON":
		return capabilityoverlay.KindEntityCard
	case "ORG", "ORGANIZATION":
		return capabilityoverlay.KindOrganization
	case "GPE", "PLACE", "LOCATION", "CITY", "COUNTRY":
		return capabilityoverlay.KindLocation
	case "NUMBER", "NUM":
		return capabilityoverlay.KindNumber
	case "QUOTE":
		return capabilityoverlay.KindQuote
	case "PRODUCT":
		return capabilityoverlay.KindProduct
	case "LOGO":
		return capabilityoverlay.KindLogo
	default:
		return capabilityoverlay.KindConcept
	}
}

// ResolveEntityOverlayPlan is the OverlayResolver: it turns the canonical
// EntityTimeline into the semantic OverlayPlan the rendering layer consumes.
// Every entity occurrence becomes one entity_card item whose start/end are
// the occurrence's certified global audio positions — the resolver never
// guesses WHEN to show a person, an organization or a place; it shows them
// exactly while they are being spoken.
//
// Times cross the microsecond→millisecond boundary deterministically: the
// start is floor(us/1000) and the end is ceil(us/1000), so the millisecond
// item strictly covers the certified microsecond span. The plan is sealed
// (render keys + fingerprint) by its own Validate, so the caller can enqueue
// it directly.
func ResolveEntityOverlayPlan(timeline EntityTimeline, planID, videoID, projectID string, width, height, fps int) (capabilityoverlay.OverlayPlan, error) {
	if err := timeline.Validate(); err != nil {
		return capabilityoverlay.OverlayPlan{}, err
	}
	if strings.TrimSpace(planID) == "" || strings.TrimSpace(videoID) == "" {
		return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: plan_id and video_id are required")
	}
	if width <= 0 || height <= 0 || fps <= 0 {
		return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: width, height and fps must be positive")
	}

	var items []capabilityoverlay.OverlayItem
	for _, scene := range timeline.Scenes {
		for _, occurrence := range scene.Entities {
			kind := entityOverlayKind(occurrence.Type)
			entry, err := capabilityoverlay.DefaultChrononOverlayRegistry.Resolve(string(kind))
			if err != nil {
				return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: %w", err)
			}
			items = append(items, capabilityoverlay.OverlayItem{
				ID:         overlayItemID(occurrence),
				SceneID:    occurrence.SceneID,
				EntityID:   occurrence.EntityID,
				Kind:       string(kind),
				StartMs:    occurrence.AudioStartUS / 1000,
				EndMs:      (occurrence.AudioEndUS + 999) / 1000,
				TemplateID: entry.Template,
				Text:       occurrence.Name,
			})
		}
	}
	if len(items) == 0 {
		return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: timeline carries no entity occurrences")
	}

	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan,
		PlanID:        planID,
		VideoID:       videoID,
		ProjectID:     strings.TrimSpace(projectID),
		Width:         width,
		Height:        height,
		FPS:           fps,
		Items:         items,
	}
	if err := plan.Validate(); err != nil {
		return capabilityoverlay.OverlayPlan{}, fmt.Errorf("entity overlay resolver: %w", err)
	}
	return plan, nil
}

// overlayItemID derives the deterministic, collision-free overlay id for one
// occurrence: "overlay-" + scene id + "-" + entity id (e.g.
// "overlay-scene-3-tom-hanks").
func overlayItemID(o EntityOccurrence) string {
	scene := strings.TrimSpace(o.SceneID)
	entity := strings.TrimSpace(o.EntityID)
	if scene == "" {
		return "overlay-" + entity
	}
	return "overlay-" + scene + "-" + entity
}
