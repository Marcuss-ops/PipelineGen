package scriptgeneration

// overlay_audit_images.go owns the two questions the r20minuti artifact could
// not answer:
//
//  1. the image budget was 3, zero images were planned, and no surface said
//     why a selected entity ended up as a text card;
//  2. 39 intents were authored, 31 stayed PENDING, and nothing distinguished
//     "the entity was never planned" from "the text was rewritten".
//
// Both are projections of facts the plan and the bundle already carry, so they
// live beside overlay_audit.go instead of inside it (the file is at the strict
// LOC cap) and they are computed once, from the artifact that is persisted.

import (
	"errors"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"os"
	"sort"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// Reasons an entity selected for the video has no bound image. They are the
// contract the gate reports, so they are constants, not free text.
const (
	// EntityImageReasonBound is the success case: the plan carries at least
	// one image for this entity.
	EntityImageReasonBound = "bound"
	// EntityImageReasonNotSelected distinguishes a detected eligible entity
	// that never entered the video plan from an acquisition or binding failure.
	EntityImageReasonNotSelected = "entity_not_selected"
	// EntityImageReasonNoVerifiedAsset means the resolver left no verified
	// content-addressed asset for this entity: discovery, download or the
	// visual verdict did not produce one.
	EntityImageReasonNoVerifiedAsset  = "no_verified_asset"
	EntityImageReasonUploadFailed     = "UPLOAD_FAILED"
	EntityImageReasonSearchEmpty      = "SEARCH_EMPTY"
	EntityImageReasonDownloadFailed   = "DOWNLOAD_FAILED"
	EntityImageReasonUnverified       = "UNVERIFIED_IMAGE"
	EntityImageReasonIdentityMismatch = "IDENTITY_MISMATCH"
	EntityImageReasonBudgetExhausted  = "BUDGET_EXHAUSTED"
	EntityImageReasonBindingMissing   = "BINDING_MISSING"
	EntityImageReasonSegmentMissing   = "SEGMENT_MISSING"
	EntityImageReasonNotMaterialized  = "NOT_MATERIALIZED"
	// EntityImageReasonVerifiedNotBound means a verified asset EXISTS in the
	// bundle but the plan did not bind it to this entity. That is a planner
	// bug, not a media miss.
	EntityImageReasonVerifiedNotBound = "verified_asset_not_bound"
	// EntityImageReasonUnverifiedBound means the plan renders an image whose
	// bytes are not certified as a verified entity asset.
	EntityImageReasonUnverifiedBound = "unverified_asset_bound"
)

// Reasons an authored overlay intent never became a timed plan item.
const (
	// OverlayIntentReasonEntityNotPlanned means no item in the intent's scene
	// references that entity: it lost admission (budget, group limit) or was
	// never eligible.
	OverlayIntentReasonEntityNotPlanned = "entity_not_planned"
	// OverlayIntentReasonSourceTextNotInPlan means the plan carries no item
	// with the intent's source text: the planner rewrote or shortened it.
	OverlayIntentReasonSourceTextNotInPlan = "source_text_not_in_plan"
	// OverlayIntentReasonTemplateNotMaterialized means the intent DID match a
	// plan item but its timing could not be frozen. It is a lowering defect,
	// never a normal outcome.
	OverlayIntentReasonTemplateNotMaterialized = "template_not_materialized"
)

// ErrEntityImageGate is the typed failure of the opt-in strict entity-image
// contract (VELOX_REQUIRE_ENTITY_IMAGES). It exists so a run can fail loudly
// instead of delivering a video with text-only cards where the policy asked
// for portraits.
var ErrEntityImageGate = errors.New("entity image gate")

const (
	requireEntityImagesEnv = "VELOX_REQUIRE_ENTITY_IMAGES"
	// maxAuditMissingEntities bounds the human-readable list; the counts stay
	// complete.
	maxAuditMissingEntities = 20
	maxAuditUnresolvedItems = 100
)

// OverlayAuditEntityImages is the entity-image admission summary.
type OverlayAuditEntityImages struct {
	// Required echoes the policy: true only when the strict gate is on.
	Required bool `json:"required"`
	// Budget is the plan's image-overlay budget; Used is the number of
	// planned items carrying image bytes, Unused the remainder.
	Budget       int `json:"budget"`
	BudgetUsed   int `json:"budget_used"`
	BudgetUnused int `json:"budget_unused"`
	// Selected is the number of entities selected for the video that are
	// expected to carry an image (person/organization, or any entity whose
	// planned kinds include an entity card/image).
	Selected int `json:"selected_entities"`
	Bound    int `json:"bound"`
	Missing  int `json:"missing"`
	// Reasons counts the outcome per entity, including "bound".
	Reasons         map[string]int             `json:"reasons,omitempty"`
	MissingEntities []OverlayAuditMissingImage `json:"missing_entities,omitempty"`
}

// OverlayAuditMissingImage names one selected entity whose image is absent and
// the reason it is absent.
type OverlayAuditMissingImage struct {
	EntityID   string `json:"entity_id,omitempty"`
	Entity     string `json:"entity"`
	EntityType string `json:"entity_type,omitempty"`
	SceneID    string `json:"scene_id,omitempty"`
	Reason     string `json:"reason"`
}

// OverlayAuditOverlayTiming is the intent→plan lineage. Authored = Frozen +
// Unresolved; an unresolved intent is a proposal, not a render instruction.
type OverlayAuditOverlayTiming struct {
	Authored        int                      `json:"authored_intents"`
	Frozen          int                      `json:"frozen_intents"`
	Unresolved      int                      `json:"unresolved_intents"`
	BySource        map[string]int           `json:"by_source,omitempty"`
	Reasons         map[string]int           `json:"reasons,omitempty"`
	UnresolvedItems []OverlayAuditTimingItem `json:"unresolved_items,omitempty"`
}

// OverlayAuditTimingItem is one unresolved intent, kept readable so an operator
// can tie it to a scene without loading the whole intent surface.
type OverlayAuditTimingItem struct {
	IntentID string `json:"intent_id"`
	SceneID  string `json:"scene_id,omitempty"`
	Source   string `json:"source,omitempty"`
	Text     string `json:"text,omitempty"`
	Entity   string `json:"entity,omitempty"`
	Reason   string `json:"reason"`
}

// buildOverlayAuditEntityImages joins the plan's image items to the entities
// that were selected for the video. It never invents an image: "bound" means
// the plan carries image bytes for that entity.
func buildOverlayAuditEntityImages(audit *OverlayAudit, bundle *capabilityoverlay.SemanticRenderBundleV1, plan *capmodel.OverlayPlan) *OverlayAuditEntityImages {
	if audit == nil {
		return nil
	}
	info := &OverlayAuditEntityImages{
		Required: entityImageGateRequired(),
		Reasons:  make(map[string]int),
	}
	if plan != nil && plan.Intake != nil {
		info.Budget = plan.Intake.Budget.ImageOverlays
	}
	for _, item := range audit.PlannedOverlays {
		if item.ImageCount > 0 {
			info.BudgetUsed++
		}
	}
	if info.Budget > info.BudgetUsed {
		info.BudgetUnused = info.Budget - info.BudgetUsed
	}
	verified := make(map[string]bool, len(audit.DetectedEntities))
	if bundle != nil {
		for _, asset := range bundle.Assets {
			if asset.Verified && strings.TrimSpace(asset.EntityID) != "" {
				verified[asset.EntityID] = true
			}
		}
	}
	// Audit one planned identity, not every spoken occurrence of it. The
	// planner works per canonical identity (or per scene occurrence when the
	// per-scene contract is on); counting all 18 mentions of five people as 18
	// image promises made the gate impossible even with perfect media.
	groups := make(map[string][]OverlayAuditEntity)
	order := make([]string, 0, len(audit.DetectedEntities))
	for _, entity := range audit.DetectedEntities {
		if !entityExpectsImage(entity) {
			continue
		}
		key := auditEntityImageGroupKey(entity)
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], entity)
	}
	for _, key := range order {
		entity := bestAuditImageEntity(groups[key])
		info.Selected++
		reason := entityImageOutcome(entity, verified)
		info.Reasons[reason]++
		if reason == EntityImageReasonBound {
			info.Bound++
			continue
		}
		info.Missing++
		if len(info.MissingEntities) < maxAuditMissingEntities {
			info.MissingEntities = append(info.MissingEntities, OverlayAuditMissingImage{
				EntityID:   entity.ID,
				Entity:     firstNonEmptyAuditValue(entity.Text, entity.CanonicalText),
				EntityType: entity.Type,
				SceneID:    entity.SceneID,
				Reason:     reason,
			})
		}
	}
	sort.SliceStable(info.MissingEntities, func(i, j int) bool {
		left, right := info.MissingEntities[i], info.MissingEntities[j]
		if left.Entity != right.Entity {
			return left.Entity < right.Entity
		}
		return left.SceneID < right.SceneID
	})
	return info
}

// entityExpectsImage reports whether the compiled plan actually assigns this
// entity an image-bearing overlay. Text entity cards and location/map overlays
// are not image promises: counting them here made final jobs fail even when
// every planned entity-image overlay had a verified asset.
func entityExpectsImage(entity OverlayAuditEntity) bool {
	// A planned image is a promise even when it is nested in a chain: the
	// top-level item has no EntityRef to set SelectedForVideo, but the plan
	// still carries this entity's bytes.
	if strings.EqualFold(strings.TrimSpace(entity.ImageStatus), "planned") {
		return true
	}
	if !entity.SelectedForVideo {
		return false
	}
	if entity.ImageRequired {
		return true
	}
	for _, kind := range entity.PlannedKinds {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if strings.Contains(kind, "image") {
			return true
		}
	}
	return false
}

// auditEntityImageGroupKey is the planner's coverage unit: one scene-scoped
// occurrence in per-scene mode, otherwise one canonical identity. The empty
// OccurrenceID is the run-level contract (one card per identity).
func auditEntityImageGroupKey(entity OverlayAuditEntity) string {
	if occurrence := strings.TrimSpace(entity.OccurrenceID); occurrence != "" {
		return occurrence
	}
	if canonical := strings.TrimSpace(entity.CanonicalEntityID); canonical != "" {
		return canonical
	}
	return strings.TrimSpace(entity.ID)
}

func bestAuditImageEntity(entities []OverlayAuditEntity) OverlayAuditEntity {
	best := entities[0]
	for _, candidate := range entities[1:] {
		if auditImageEntityRank(candidate) > auditImageEntityRank(best) {
			best = candidate
		}
	}
	return best
}

func auditImageEntityRank(entity OverlayAuditEntity) int {
	switch strings.ToLower(strings.TrimSpace(entity.ImageStatus)) {
	case "planned":
		if entity.HasVerifiedImage {
			return 4
		}
		return 3
	case "verified_asset_not_planned":
		return 2
	case "planned_without_verified_asset":
		return 1
	default:
		return 0
	}
}

// auditEntityHasPlannedImageCard reports whether the planner emitted an
// image-bearing card for this identity. A text-only entity card is the
// fallback the planner chose when no image was available, so it is not an
// image promise and must never fail the strict image gate. Only a planned
// image (top-level or nested in a chain) is a promise to bind verified bytes.
func auditEntityHasPlannedImageCard(entity OverlayAuditEntity) bool {
	if strings.EqualFold(strings.TrimSpace(entity.ImageStatus), "planned") {
		return true
	}
	for _, kind := range entity.PlannedKinds {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "entity_image" || strings.Contains(kind, "image") {
			return true
		}
	}
	return false
}

// applyRequestedEntityImageContract derives the audit denominator from the
// request's eligible entity types, not from overlays that happened to survive
// planning. It preserves SelectedForVideo separately so a missing selection
// remains distinguishable from a failed search or binding.
func applyRequestedEntityImageContract(audit *OverlayAudit, bundle *capabilityoverlay.SemanticRenderBundleV1, plan *capmodel.OverlayPlan, required, requireDrive bool, types []string, admittedSets ...map[string]struct{}) {
	if audit == nil || !required {
		return
	}
	wanted := make(map[string]struct{}, len(types))
	for _, typ := range types {
		wanted[strings.ToUpper(strings.TrimSpace(typ))] = struct{}{}
	}
	if len(wanted) == 0 {
		for _, typ := range []string{"PERSON", "ORG", "GPE", "LOC", "CITY", "LANDMARK", "LOGO"} {
			wanted[typ] = struct{}{}
		}
	}
	var admitted map[string]struct{}
	if len(admittedSets) > 0 {
		admitted = admittedSets[0]
	}
	for i := range audit.DetectedEntities {
		entity := &audit.DetectedEntities[i]
		entityType := strings.ToUpper(strings.TrimSpace(entity.Type))
		_, imageRequired := wanted[entityType]
		// Only an image card the planner actually emitted is an image promise.
		// A text card is the deliberate degradation path, not a missing image;
		// counting it here made the gate demand bytes for a fallback the plan
		// chose and turned every unverified identity into a false failure.
		if imageRequired && !auditEntityHasPlannedImageCard(*entity) {
			imageRequired = false
		}
		// The semantic production plan is the run-level admission decision for
		// identity imagery. When it deliberately excluded an identity from
		// media, the resolver drops its image card and the audit must not turn
		// that policy into a missing-image failure. A planned image is still
		// audited: once the plan carries bytes, the Drive contract must hold.
		if imageRequired && len(admitted) > 0 && !strings.EqualFold(strings.TrimSpace(entity.ImageStatus), "planned") {
			surface := normalizeIdentitySurface(firstNonEmptyAuditValue(entity.Text, entity.CanonicalText))
			if _, ok := admitted[surface]; !ok {
				imageRequired = false
			}
		}
		entity.ImageRequired = imageRequired
		// upload_to_drive applies to acquired assets. A reviewed local registry
		// asset is already producer-local and content-addressed; its empty Drive
		// id is not an upload failure.
		entity.ImageDriveRequired = requireDrive && imageRequired && !entity.ImageLocalVerified
	}
	audit.EntityImages = buildOverlayAuditEntityImages(audit, bundle, plan)
}

// entityImageOutcome maps the audit's image_status onto the gate vocabulary.
// The status already distinguishes "no asset" from "verified but not planned";
// this keeps that distinction in the reason the gate reports.
func entityImageOutcome(entity OverlayAuditEntity, verified map[string]bool) string {
	switch strings.ToUpper(strings.TrimSpace(entity.ImageFailureReason)) {
	case EntityImageReasonSearchEmpty, EntityImageReasonDownloadFailed, EntityImageReasonUnverified,
		EntityImageReasonUploadFailed, EntityImageReasonIdentityMismatch, EntityImageReasonBudgetExhausted,
		EntityImageReasonBindingMissing, EntityImageReasonSegmentMissing, EntityImageReasonNotMaterialized:
		return strings.ToUpper(strings.TrimSpace(entity.ImageFailureReason))
	}
	if entity.ImageDriveRequired && entity.ImageStatus == "planned" && strings.TrimSpace(entity.ImageDriveFileID) == "" && !entity.ImageLocalVerified {
		return EntityImageReasonUploadFailed
	}
	if entity.ImageRequired && !entity.SelectedForVideo && entity.ImageStatus == "missing" {
		return EntityImageReasonNotSelected
	}
	if !verified[entity.ID] && entity.ImageStatus == "planned" && !entity.ImageLocalVerified {
		return EntityImageReasonUnverifiedBound
	}
	switch entity.ImageStatus {
	case "planned":
		return EntityImageReasonBound
	case "planned_without_verified_asset":
		return EntityImageReasonUnverified
	case "verified_asset_not_planned":
		// The asset exists and is verified; the planner simply did not
		// select this occurrence for a card. That is an editorial choice,
		// not a missing binding, so it must never fail the strict gate.
		return EntityImageReasonNotSelected
	}
	if verified[entity.ID] {
		return EntityImageReasonBindingMissing
	}
	return EntityImageReasonSearchEmpty
}

// buildOverlayAuditOverlayTiming projects the intent lineage. A nil lineage
// (older call sites, tests of the plan projection alone) yields nil so the
// audit never claims a lineage it did not compute.
func buildOverlayAuditOverlayTiming(lineage *overlayIntentLineage) *OverlayAuditOverlayTiming {
	if lineage == nil {
		return nil
	}
	info := &OverlayAuditOverlayTiming{
		Authored:   lineage.Authored,
		Frozen:     lineage.Frozen,
		Unresolved: lineage.Unresolved,
		BySource:   lineage.BySource,
		Reasons:    lineage.Reasons,
	}
	for _, item := range lineage.Items {
		info.UnresolvedItems = append(info.UnresolvedItems, OverlayAuditTimingItem{
			IntentID: item.IntentID,
			SceneID:  item.SceneID,
			Source:   item.Source,
			Text:     item.Text,
			Entity:   item.Entity,
			Reason:   item.Reason,
		})
	}
	return info
}

// entityImageGateRequired reports the strict contract. Off by default: the
// audit is always emitted, the failure is opt-in so an existing run that
// accepts text-only cards keeps working.
func entityImageGateRequired() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(requireEntityImagesEnv))) {
	case "1", "true", "on", "enabled", "yes", "strict":
		return true
	default:
		return false
	}
}

// enforceEntityImageGate fails the plan when the operator asked for images and
// a selected entity has none. The message names the counts, the reasons and a
// few entities, so the failure is actionable without opening the artifact.
func enforceEntityImageGate(audit *OverlayAudit) error {
	if audit == nil || audit.EntityImages == nil || !audit.EntityImages.Required {
		return nil
	}
	info := audit.EntityImages
	if info.Missing == 0 {
		return nil
	}
	names := make([]string, 0, 3)
	for _, item := range info.MissingEntities {
		if name := strings.TrimSpace(item.Entity); name != "" {
			names = append(names, name)
		}
		if len(names) == 3 {
			break
		}
	}
	reasons := make([]string, 0, len(info.Reasons))
	for reason, count := range info.Reasons {
		if reason == EntityImageReasonBound {
			continue
		}
		reasons = append(reasons, fmt.Sprintf("%s=%d", reason, count))
	}
	sort.Strings(reasons)
	return fmt.Errorf("%w: %d of %d planned entity-image promises have no bound image (%s); reasons: %s",
		ErrEntityImageGate, info.Missing, info.Selected,
		strings.Join(names, ", "), strings.Join(reasons, ", "))
}
