package contract

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
	// Pipeline is the explicit retrieval→binding ledger. It is entity-level:
	// the audit owns one row per selected image promise, not the raw provider
	// result set. The per-candidate detail remains in segments[].assets.
	Pipeline *OverlayAuditImagePipeline `json:"pipeline,omitempty"`
}

// OverlayAuditImagePipeline is the report contract operators use to see where
// image promises stop. Retrieved means a discovery candidate exists; verified
// means a usable visual verdict; selected means the planner emitted an image;
// materialized means a durable Drive/content-addressed asset exists; bound
// means both selected and verified; skipped means an image promise had no
// planned image.
type OverlayAuditImagePipeline struct {
	Retrieved    int `json:"retrieved"`
	Verified     int `json:"verified"`
	Selected     int `json:"selected"`
	Materialized int `json:"materialized"`
	Bound        int `json:"bound"`
	Skipped      int `json:"skipped"`
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
// Covered + Unresolved. Covered means the same canonical identity carries a
// card somewhere else in the run (the portrait is reused across scenes), so
// the intent is resolved even though its own scene has no timed item. An
// unresolved intent is a proposal, not a render instruction.
type OverlayAuditOverlayTiming struct {
	Authored        int                      `json:"authored_intents"`
	Frozen          int                      `json:"frozen_intents"`
	Covered         int                      `json:"covered_intents"`
	Unresolved      int                      `json:"unresolved_intents"`
	BySource        map[string]int           `json:"by_source,omitempty"`
	Reasons         map[string]int           `json:"reasons,omitempty"`
	UnresolvedItems []OverlayAuditTimingItem `json:"unresolved_items,omitempty"`
}

// OverlayAuditTimingItem is one unresolved intent, kept readable so an operator
// can tie it to a scene without loading the whole intent surface.
type OverlayAuditTimingItem struct {
	IntentID          string `json:"intent_id"`
	SceneID           string `json:"scene_id,omitempty"`
	Source            string `json:"source,omitempty"`
	Text              string `json:"text,omitempty"`
	Entity            string `json:"entity,omitempty"`
	EntityID          string `json:"entity_id,omitempty"`
	CanonicalEntityID string `json:"canonical_entity_id,omitempty"`
	EntityType        string `json:"entity_type,omitempty"`
	Reason            string `json:"reason"`
}
