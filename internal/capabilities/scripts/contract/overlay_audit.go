package contract

// OverlayAudit is a compact user-facing projection of the semantic source
// entities and the overlays selected for rendering. It distinguishes what was
// detected from what survived planning, and describes phrase length and image
// or map payloads explicitly.
type OverlayAudit struct {
	SchemaVersion       string               `json:"schema_version"`
	DetectedEntityCount int                  `json:"detected_entity_count"`
	UniqueEntityCount   int                  `json:"unique_entity_count"`
	DetectedByType      map[string]int       `json:"detected_by_type"`
	DetectedEntities    []OverlayAuditEntity `json:"detected_entities,omitempty"`
	PlannedOverlayCount int                  `json:"planned_overlay_count"`
	PlannedByKind       map[string]int       `json:"planned_by_kind"`
	PlannedOverlays     []OverlayAuditItem   `json:"planned_overlays,omitempty"`
	ImageBindings       []OverlayAuditImage  `json:"image_bindings,omitempty"`
	Intake              *OverlayAuditIntake  `json:"intake,omitempty"`
	// EntityImages answers the question the r20minuti report could not: the
	// image budget was 3, zero images were planned, and nothing said why. It
	// separates a selected entity that got its image from one that did not,
	// with a reason derived from the plan and the verified-asset bundle.
	EntityImages *OverlayAuditEntityImages `json:"entity_images,omitempty"`
	// OverlayTiming records the intent→plan lineage: how many authored
	// intents were frozen into a timed item and how many stayed PENDING. A
	// PENDING intent is not a render instruction, and hiding it inside a
	// 39-element array made it look like one.
	OverlayTiming *OverlayAuditOverlayTiming `json:"overlay_timing,omitempty"`
	// EntityConcordance proves the image contract and the intent lineage
	// describe the same identity (TICKET-49). A planned portrait plus an
	// entity_not_planned intent for that identity is a measurement
	// contradiction, not an editorial policy outcome.
	EntityConcordance *OverlayAuditEntityConcordance `json:"entity_concordance,omitempty"`
	// PlanSHA256 is the canonical digest of the sealed plan. Two runs that
	// claim the same plan identity must publish the same digest; a difference
	// is evidence of non-determinism, not a benchmark result.
	PlanSHA256 string `json:"plan_sha256,omitempty"`
	PlanID     string `json:"plan_id,omitempty"`
	VideoID    string `json:"video_id,omitempty"`
}

// OverlayAuditIntake exposes admission limits and drop reasons beside the final plan.
type OverlayAuditIntake struct {
	CandidateCount   int            `json:"candidate_count"`
	CandidatesByKind map[string]int `json:"candidates_by_kind,omitempty"`
	DroppedByReason  map[string]int `json:"dropped_by_reason,omitempty"`
	PhraseBudget     int            `json:"phrase_budget"`
	ImageBudget      int            `json:"image_budget"`
	MapBudget        int            `json:"map_budget"`
}

type OverlayAuditEntity struct {
	ID                     string   `json:"id"`
	CanonicalEntityID      string   `json:"canonical_entity_id,omitempty"`
	OccurrenceID           string   `json:"occurrence_id,omitempty"`
	Type                   string   `json:"type"`
	Text                   string   `json:"text"`
	CanonicalText          string   `json:"canonical_text,omitempty"`
	SceneID                string   `json:"scene_id"`
	Confidence             float64  `json:"confidence"`
	SelectedForVideo       bool     `json:"selected_for_video"`
	ImageRequired          bool     `json:"image_required"`
	ImageDriveRequired     bool     `json:"image_drive_required,omitempty"`
	ImageAcquisitionStatus string   `json:"image_acquisition_status,omitempty"`
	HasVerifiedImage       bool     `json:"has_verified_image"`
	ImageStatus            string   `json:"image_status,omitempty"`
	ImageAssetIDs          []string `json:"image_asset_ids,omitempty"`
	PlannedKinds           []string `json:"planned_kinds,omitempty"`
	ImageQuery             string   `json:"image_query,omitempty"`
	ImageCandidateID       string   `json:"image_candidate_id,omitempty"`
	ImageSelectionReason   string   `json:"image_selection_reason,omitempty"`
	ImageFailureReason     string   `json:"image_failure_reason,omitempty"`
	ImageSHA256            string   `json:"image_sha256,omitempty"`
	ImageDriveFileID       string   `json:"image_drive_file_id,omitempty"`
	OverlayItemID          string   `json:"overlay_item_id,omitempty"`
}

// OverlayAuditImage makes the image decision auditable at the plan boundary:
// which entity the item names, which bytes it would render, and whether the
// bundle already carried a verified asset. It deliberately does not claim that
// a frame was visible; that is a render-audit fact, not a plan fact.
type OverlayAuditImage struct {
	EntityID    string   `json:"entity_id,omitempty"`
	Entity      string   `json:"entity,omitempty"`
	EntityType  string   `json:"entity_type,omitempty"`
	SceneID     string   `json:"scene_id,omitempty"`
	OverlayID   string   `json:"overlay_id"`
	AssetIDs    []string `json:"asset_ids,omitempty"`
	Status      string   `json:"status"`
	HasVerified bool     `json:"has_verified_asset"`
}

// OverlayAuditEntityConcordance is the run-level agreement check between the
// two entity projections. Checked counts the identities the image contract
// reports as planned; Mismatched counts those the lineage still calls
// unresolved. The acceptance bar is Mismatched == 0.
type OverlayAuditEntityConcordance struct {
	Checked    int                          `json:"checked"`
	Mismatched int                          `json:"mismatched"`
	Mismatches []OverlayAuditEntityConflict `json:"mismatches,omitempty"`
}

// OverlayAuditEntityConflict names one identity whose image and intent
// projections disagree.
type OverlayAuditEntityConflict struct {
	EntityID string `json:"entity_id"`
	Entity   string `json:"entity,omitempty"`
	SceneID  string `json:"scene_id,omitempty"`
	Reason   string `json:"reason"`
}

type OverlayAuditItem struct {
	ID         string   `json:"id"`
	SceneID    string   `json:"scene_id,omitempty"`
	Kind       string   `json:"kind"`
	Class      string   `json:"class"`
	Entity     string   `json:"entity,omitempty"`
	EntityType string   `json:"entity_type,omitempty"`
	Text       string   `json:"text,omitempty"`
	WordCount  int      `json:"word_count,omitempty"`
	AssetIDs   []string `json:"asset_ids,omitempty"`
	ImageCount int      `json:"image_count,omitempty"`
	HasMap     bool     `json:"has_map,omitempty"`
	StartMS    int64    `json:"start_ms"`
	EndMS      int64    `json:"end_ms"`
}
