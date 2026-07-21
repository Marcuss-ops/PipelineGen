// Package mediamemory (api) — dto.go is the canonical SSOT for the
// MediaMemory wire shapes.
//
// godlike/06 SSOT (typed-DTO discipline): every HTTP request and
// response uses a typed struct (NOT a gin.H map). The drift pin:
// the JSON marshalling contract is verifiable verbatim by a
// future module test; clients in other languages can codegen from
// these structs when consuming the API.
//
// godlike/07 NO-FAKE-AVAILABILITY: required fields use the
// `binding:"required"` validator tag (gin) so missing fields surface
// as 400 BEFORE the service is invoked; surface-level validation
// never lies.
package mediamemory

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
)

// bindingCreateRequest is the POST /api/media-memory/bindings body.
//
// godlike/06 SSOT (canonical field set): every field maps 1:1 to a
// column on media_bindings. The service is responsible for
// defaulting Origin ("manual") and ApprovalStatus ("approved")
// when the client omits them; the API does NOT silently
// invent values that the ranker might later mis-classify.
type bindingCreateRequest struct {
	ConceptID      string   `json:"concept_id" binding:"required"`
	AssetID        string   `json:"asset_id" binding:"required"`
	SlotKind       string   `json:"slot_kind" binding:"required"`
	StartMs        int64    `json:"start_ms,omitempty"`
	EndMs          int64    `json:"end_ms,omitempty"`
	Origin         string   `json:"origin,omitempty"`
	ApprovalStatus string   `json:"approval_status,omitempty"`
	ManualScore    *float64 `json:"manual_score,omitempty"`
	SemanticScore  *float64 `json:"semantic_score,omitempty"`
	QualityScore   *float64 `json:"quality_score,omitempty"`
}

// toMediaBinding translates the wire DTO into the canonical
// mediamemory.MediaBinding. Empty origin/approval are left empty
// so the service applyDefaults() (which mirrors godlike/06 SSOT)
// fills them in at the canonical defaults.
//
// pointer-valued scores are dereferenced; nil → 0.
func (r bindingCreateRequest) toMediaBinding() mediamemory.MediaBinding {
	out := mediamemory.MediaBinding{
		ConceptID:      r.ConceptID,
		AssetID:        r.AssetID,
		SlotKind:       mediamemory.SlotKind(r.SlotKind),
		StartMs:        r.StartMs,
		EndMs:          r.EndMs,
		Origin:         mediamemory.Origin(r.Origin),
		ApprovalStatus: mediamemory.ApprovalStatus(r.ApprovalStatus),
	}
	if r.ManualScore != nil {
		out.ManualScore = *r.ManualScore
	}
	if r.SemanticScore != nil {
		out.SemanticScore = *r.SemanticScore
	}
	if r.QualityScore != nil {
		out.QualityScore = *r.QualityScore
	}
	return out
}

// bindingListRequest extracts GET /api/media-memory/bindings
// query params. concept_id is required; the dashboard's diff view
// would otherwise return every binding in the system (godlike/07).
type bindingListRequest struct {
	ConceptID string `form:"concept_id" binding:"required"`
}

// bindingDTO is the wire shape of one persisted MediaBinding. We
// do NOT expose server-internal fields (LocalPath, DriveLink)
// directly; the ranker resolver produces them on-demand via
// AssetDeliveryService (forward-pointer clipresolve.AssetMapping).
type bindingDTO struct {
	ID             string  `json:"id"`
	ConceptID      string  `json:"concept_id"`
	AssetID        string  `json:"asset_id"`
	SlotKind       string  `json:"slot_kind"`
	StartMs        int64   `json:"start_ms,omitempty"`
	EndMs          int64   `json:"end_ms,omitempty"`
	Origin         string  `json:"origin"`
	ApprovalStatus string  `json:"approval_status"`
	ManualScore    float64 `json:"manual_score"`
	SemanticScore  float64 `json:"semantic_score"`
	QualityScore   float64 `json:"quality_score"`
	SuccessScore   float64 `json:"success_score"`
	UsageCount     int     `json:"usage_count"`
	LastUsedAt     string  `json:"last_used_at,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// toBindingDTO projects a canonical MediaBinding to its wire shape.
func toBindingDTO(b mediamemory.MediaBinding) bindingDTO {
	out := bindingDTO{
		ID:             b.ID,
		ConceptID:      b.ConceptID,
		AssetID:        b.AssetID,
		SlotKind:       string(b.SlotKind),
		StartMs:        b.StartMs,
		EndMs:          b.EndMs,
		Origin:         string(b.Origin),
		ApprovalStatus: string(b.ApprovalStatus),
		ManualScore:    b.ManualScore,
		SemanticScore:  b.SemanticScore,
		QualityScore:   b.QualityScore,
		SuccessScore:   b.SuccessScore,
		UsageCount:     b.UsageCount,
		CreatedAt:      b.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:      b.UpdatedAt.Format(time.RFC3339Nano),
	}
	if b.LastUsedAt != nil {
		out.LastUsedAt = b.LastUsedAt.Format(time.RFC3339Nano)
	}
	return out
}

// feedbackRequest is the POST /api/media-memory/feedback body.
type feedbackRequest struct {
	ProjectID string `json:"project_id" binding:"required"`
	SceneID   string `json:"scene_id" binding:"required"`
	BindingID string `json:"binding_id" binding:"required"`
	Action    string `json:"action" binding:"required"`
	Reason    string `json:"reason,omitempty"`
}

// toFeedbackInput projects the wire DTO into the canonical input.
// The clock.Now() default for OccurredAt is applied by the service
// (godlike/06 SSOT: OccurredAt always means "feedback event time",
// never "client reported time" — clients may lie).
func (r feedbackRequest) toFeedbackInput() mediamemory.FeedbackInput {
	return mediamemory.FeedbackInput{
		ProjectID: r.ProjectID,
		SceneID:   r.SceneID,
		BindingID: r.BindingID,
		Action:    mediamemory.FeedbackAction(r.Action),
		Reason:    r.Reason,
	}
}

// usageEventDTO is the wire shape returned for POST /feedback.
// We expose the FK columns (ConceptID, AssetID, BindingID,
// ProjectID, SceneID) but NOT server-internal IDs.
type usageEventDTO struct {
	ID               string `json:"id"`
	ProjectID        string `json:"project_id"`
	SceneID          string `json:"scene_id"`
	ConceptID        string `json:"concept_id"`
	AssetID          string `json:"asset_id"`
	BindingID        string `json:"binding_id"`
	SlotKind         string `json:"slot_kind"`
	Selected         bool   `json:"selected"`
	ManuallySelected bool   `json:"manually_selected"`
	Rejected         bool   `json:"rejected"`
	RenderCompleted  bool   `json:"render_completed"`
	CreatedAt        string `json:"created_at"`
}

// toUsageEventDTO projects a canonical UsageEvent into wire shape.
func toUsageEventDTO(e mediamemory.UsageEvent) usageEventDTO {
	return usageEventDTO{
		ID:               e.ID,
		ProjectID:        e.ProjectID,
		SceneID:          e.SceneID,
		ConceptID:        e.ConceptID,
		AssetID:          e.AssetID,
		BindingID:        e.BindingID,
		SlotKind:         string(e.SlotKind),
		Selected:         e.Selected,
		ManuallySelected: e.ManuallySelected,
		Rejected:         e.Rejected,
		RenderCompleted:  e.RenderCompleted,
		CreatedAt:        e.CreatedAt.Format(time.RFC3339Nano),
	}
}

// errorEnvelope is the canonical wire shape for 400/404/409
// responses. The code field is machine-readable (branchable); the
// message field is human-readable.
//
// godlike/07 NO-FAKE-AVAILABILITY: the code field is non-empty
// exactly when a typed-sentinel was matched (no generic "internal
// error" codes are leaked to the client without logging them).
type errorEnvelope struct {
	OK        bool   `json:"ok"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// bindingListResponse is the GET /bindings response shape.
type bindingListResponse struct {
	OK        bool         `json:"ok"`
	Bindings  []bindingDTO `json:"bindings"`
	Timestamp string       `json:"timestamp"`
}

// okEnvelope is the canonical 2xx success body for routes whose
// response is solely time-stamped (no payload beyond a counter).
type okEnvelope struct {
	OK        bool   `json:"ok"`
	Timestamp string `json:"timestamp"`
}

// ── Resolve (POST /api/media-memory/resolve) ──────────────────────

// resolveCreateRequest is the POST /api/media-memory/resolve body.
//
// godlike/06 SSOT (canonical field set): every field maps 1:1 to a
// canonical column on mediamemory/sceneVisualPlan. ProjectID +
// Language are project-shared; Scenes carry per-scene text,
// duration, and slot requirements.
type resolveCreateRequest struct {
	ProjectID string                `json:"project_id" binding:"required"`
	Language  string                `json:"language" binding:"required"`
	Scenes    []resolveSceneRequest `json:"scenes" binding:"required"`
	Policy    *resolvePolicyRequest `json:"policy,omitempty"`
}

// resolveSceneRequest is one scene entry. godlike/06 SSOT: the
// canonical ID is supplied by the caller (the splitter may assign
// scene-1..scene-N); we don't mint IDs on the wire side.
type resolveSceneRequest struct {
	ID         string   `json:"id" binding:"required"`
	Text       string   `json:"text" binding:"required"`
	DurationMs int64    `json:"duration_ms" binding:"min=0"`
	Slots      []string `json:"slots,omitempty"`
	Language   string   `json:"language,omitempty"`
}

// resolvePolicyRequest is the optional client-supplied override.
// godlike/06 SSOT: defaults are conservative (PreferApprovedBindings
// = true, AllowExternalSearch = false) — the dashboard preview
// path is sandboxed and does NOT want to fan out to live providers.
type resolvePolicyRequest struct {
	PreferApprovedBindings bool `json:"prefer_approved_bindings"`
	AllowExternalSearch    bool `json:"allow_external_search"`
	MaxCandidatesPerSlot   int  `json:"max_candidates_per_slot"`
	AvoidRecentAssets      bool `json:"avoid_recent_assets"`
}

// toResolveRequest projects the wire DTO into the canonical
// mediamemory.ResolveRequest. Empty meaningful fields are filled
// by the canonical defaults below.
func (r resolveCreateRequest) toResolveRequest() mediamemory.ResolveRequest {
	policy := mediamemory.ResolvePolicy{
		PreferApprovedBindings: defaultPreferApprovedBindings,
		AllowExternalSearch:    defaultAllowExternalSearch,
		MaxCandidatesPerSlot:   defaultMaxCandidatesPerSlot,
		AvoidRecentAssets:      defaultAvoidRecentAssets,
	}
	if r.Policy != nil {
		policy.PreferApprovedBindings = r.Policy.PreferApprovedBindings || policy.PreferApprovedBindings
		policy.AllowExternalSearch = r.Policy.AllowExternalSearch || policy.AllowExternalSearch
		if r.Policy.MaxCandidatesPerSlot > 0 {
			policy.MaxCandidatesPerSlot = r.Policy.MaxCandidatesPerSlot
		}
		policy.AvoidRecentAssets = r.Policy.AvoidRecentAssets || policy.AvoidRecentAssets
	}

	scenes := make([]mediamemory.SceneSpec, 0, len(r.Scenes))
	for _, s := range r.Scenes {
		lang := s.Language
		if lang == "" {
			lang = r.Language
		}
		slotKinds := make([]mediamemory.SlotKind, 0, len(s.Slots))
		for _, slotStr := range s.Slots {
			if slotStr == "" {
				continue
			}
			slotKinds = append(slotKinds, mediamemory.SlotKind(slotStr))
		}
		scenes = append(scenes, mediamemory.SceneSpec{
			ID:         s.ID,
			Text:       s.Text,
			DurationMs: s.DurationMs,
			Slots:      slotKinds,
			Language:   lang,
		})
	}
	return mediamemory.ResolveRequest{
		ProjectID: r.ProjectID,
		Language:  r.Language,
		Scenes:    scenes,
		Policy:    policy,
	}
}

// resolveLayerDTO is one Layer entry in the response.
type resolveLayerDTO struct {
	Slot           string  `json:"slot"`
	AssetID        string  `json:"asset_id"`
	BindingID      string  `json:"binding_id,omitempty"`
	StartMs        int64   `json:"start_ms,omitempty"`
	EndMs          int64   `json:"end_ms,omitempty"`
	Layout         string  `json:"layout,omitempty"`
	CandidateScore float64 `json:"candidate_score"`
}

// resolvePlanDTO is the per-scene plan projection.
type resolvePlanDTO struct {
	ProjectID  string            `json:"project_id"`
	SceneID    string            `json:"scene_id"`
	Text       string            `json:"text"`
	Language   string            `json:"language"`
	DurationMs int64             `json:"duration_ms"`
	Layers     []resolveLayerDTO `json:"layers"`
	Source     string            `json:"source"`
}

// resolveResponse is the canonical 2xx response envelope.
type resolveResponse struct {
	OK        bool             `json:"ok"`
	Plans     []resolvePlanDTO `json:"plans"`
	Warnings  []string         `json:"warnings,omitempty"`
	Timestamp string           `json:"timestamp"`
}

// toResolvePlanDTO projects a canonical SceneVisualPlan to its
// wire shape. godlike/06 SSOT: empty Layers is a legitimate
// "fall-through exhausted" response — clients can branch on
// len(Layers) == 0 to render an "asset unavailable" notice.
func toResolvePlanDTO(p mediamemory.SceneVisualPlan) resolvePlanDTO {
	layers := make([]resolveLayerDTO, 0, len(p.Layers))
	for _, l := range p.Layers {
		layers = append(layers, resolveLayerDTO{
			Slot:           string(l.Slot),
			AssetID:        l.AssetID,
			BindingID:      l.BindingID,
			StartMs:        l.StartMs,
			EndMs:          l.EndMs,
			Layout:         l.Layout,
			CandidateScore: l.CandidateScore,
		})
	}
	return resolvePlanDTO{
		ProjectID:  p.ProjectID,
		SceneID:    p.SceneID,
		Text:       p.Text,
		Language:   p.Language,
		DurationMs: p.DurationMs,
		Layers:     layers,
		Source:     p.Source,
	}
}

// ── Canonical defaults (godlike/06 SSOT — composition pin) ──────

const (
	defaultPreferApprovedBindings = true
	defaultAllowExternalSearch    = false
	defaultMaxCandidatesPerSlot   = 10
	defaultAvoidRecentAssets      = false
)
