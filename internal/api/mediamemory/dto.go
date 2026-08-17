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
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
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
		SlotKind:       media.SlotKind(r.SlotKind),
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
//
// Pointer bools let callers explicitly override the conservative
// defaults; otherwise a JSON `false` would be merged with the default
// and lost.
type resolvePolicyRequest struct {
	PreferApprovedBindings *bool    `json:"prefer_approved_bindings,omitempty"`
	AllowExternalSearch    *bool    `json:"allow_external_search,omitempty"`
	MaxCandidatesPerSlot   int      `json:"max_candidates_per_slot,omitempty"`
	AvoidRecentAssets      *bool    `json:"avoid_recent_assets,omitempty"`
	Mode                   string   `json:"mode,omitempty"`
	AllowedProviders       []string `json:"allowed_providers,omitempty"`
	CacheRead              *bool    `json:"cache_read,omitempty"`
}

// toResolveRequest projects the wire DTO into the canonical
// mediamemory.ResolveRequest. Policy defaults are NOT applied
// here; the handler passes the optional policy to
// ResolutionPolicyResolver.Resolve() in the application layer.
func (r resolveCreateRequest) toResolveRequest(policy mediamemory.ResolvePolicy) mediamemory.ResolveRequest {
	scenes := make([]mediamemory.SceneSpec, 0, len(r.Scenes))
	for _, s := range r.Scenes {
		lang := s.Language
		if lang == "" {
			lang = r.Language
		}
		slotKinds := make([]media.SlotKind, 0, len(s.Slots))
		for _, slotStr := range s.Slots {
			if slotStr == "" {
				continue
			}
			slotKinds = append(slotKinds, media.SlotKind(slotStr))
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

// toOptionalPolicy projects the optional wire policy fields into
// the application-layer OptionalResolvePolicy. It performs no
// defaulting — defaults are owned by ResolutionPolicyResolver.
func (r resolveCreateRequest) toOptionalPolicy() mediamemory.OptionalResolvePolicy {
	if r.Policy == nil {
		return mediamemory.OptionalResolvePolicy{}
	}
	return mediamemory.OptionalResolvePolicy{
		PreferApprovedBindings: r.Policy.PreferApprovedBindings,
		AllowExternalSearch:    r.Policy.AllowExternalSearch,
		MaxCandidatesPerSlot:   r.Policy.MaxCandidatesPerSlot,
		AvoidRecentAssets:      r.Policy.AvoidRecentAssets,
		Mode:                   r.Policy.Mode,
		AllowedProviders:       append([]string(nil), r.Policy.AllowedProviders...),
		CacheRead:              r.Policy.CacheRead,
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

// resolveIntentDTO exposes what the brain understood about a scene.
type resolveIntentDTO struct {
	Entities []string `json:"entities,omitempty"`
	Concepts []string `json:"concepts,omitempty"`
	Actions  []string `json:"actions,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
}

// resolveBackendCallDTO records one backend invocation performed
// by the brain for a scene.
type resolveBackendCallDTO struct {
	Backend string `json:"backend"`
	Hits    int    `json:"hits"`
	Error   string `json:"error,omitempty"`
}

// resolveTraceDTO exposes the brain's decision trace for a scene.
type resolveTraceDTO struct {
	NormalizedText string                  `json:"normalized_text,omitempty"`
	BackendCalls   []resolveBackendCallDTO `json:"backend_calls,omitempty"`
	Reasons        []string                `json:"reasons,omitempty"`
}

// resolvePlanDTO is the per-scene plan projection.
type resolvePlanDTO struct {
	ProjectID           string            `json:"project_id"`
	SceneID             string            `json:"scene_id"`
	Text                string            `json:"text"`
	Language            string            `json:"language"`
	DurationMs          int64             `json:"duration_ms"`
	Layers              []resolveLayerDTO `json:"layers"`
	Source              string            `json:"source"`
	Intent              *resolveIntentDTO `json:"intent,omitempty"`
	Trace               *resolveTraceDTO  `json:"trace,omitempty"`
	DecisionFingerprint string            `json:"decision_fingerprint,omitempty"`
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
		ProjectID:           p.ProjectID,
		SceneID:             p.SceneID,
		Text:                p.Text,
		Language:            p.Language,
		DurationMs:          p.DurationMs,
		Layers:              layers,
		Source:              p.Source,
		Intent:              intentToDTO(p.Intent),
		Trace:               traceToDTO(p.Trace),
		DecisionFingerprint: p.DecisionFingerprint,
	}
}

// intentToDTO returns a pointer only when the intent carries
// information. This keeps the JSON response backward-compatible:
// legacy callers see no `intent` key when the brain left it empty.
func intentToDTO(in mediamemory.SceneIntent) *resolveIntentDTO {
	if len(in.Entities) == 0 && len(in.Concepts) == 0 &&
		len(in.Actions) == 0 && len(in.Keywords) == 0 {
		return nil
	}
	return &resolveIntentDTO{
		Entities: in.Entities,
		Concepts: in.Concepts,
		Actions:  in.Actions,
		Keywords: in.Keywords,
	}
}

// traceToDTO returns a pointer only when the trace carries
// information. This keeps the JSON response backward-compatible:
// legacy callers see no `trace` key when the brain left it empty.
func traceToDTO(in mediamemory.SceneResolutionTrace) *resolveTraceDTO {
	hasBackendCall := len(in.BackendCalls) > 0
	hasReasons := len(in.Reasons) > 0
	if in.NormalizedText == "" && !hasBackendCall && !hasReasons {
		return nil
	}
	backendCalls := make([]resolveBackendCallDTO, 0, len(in.BackendCalls))
	for _, call := range in.BackendCalls {
		backendCalls = append(backendCalls, resolveBackendCallDTO{
			Backend: call.Backend,
			Hits:    call.Hits,
			Error:   call.Error,
		})
	}
	return &resolveTraceDTO{
		NormalizedText: in.NormalizedText,
		BackendCalls:   backendCalls,
		Reasons:        in.Reasons,
	}
}
