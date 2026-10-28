// Package overlay — map_backend.go: backend cartografico selezionabile.
//
// Moved from the scripts root (package hotspot forward-prevention gate;
// this subpackage is the sanctioned migration target): same behavior.
//
// Transizione: PipelineGen invia la specifica semantica di mappa; il resolver
// determina quale backend supporta la composizione richiesta:
//
//	Native Chronon — mappe già supportate dal compiler nativo.
//	Dynamic Map Renderer — il porting nativo di ChrononPlugin.
//
// Un solo renderer dinamico: il porting nativo (maps_dynamic_map_render).
// Le composizioni non ancora portate falliscono chiuso con errore tipizzato.
// La queue coordina i render cartografici come gli altri render (stesso job,
// retry, storage, pubblicazione), con classe di concorrenza separata a 1.
package overlay

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// MapBackend selects which renderer executes a map composition.
type MapBackend string

const (
	MapBackendNative  MapBackend = "native"
	MapBackendDynamic MapBackend = "dynamic"
)

// MapRenderRequirements is what ONE map composition actually needs from its
// renderer. It is resolved from the semantic map (pins, camera, composition,
// labels, per-pin timing) — not from generic booleans or from how many map
// items the plan happens to hold.
type MapRenderRequirements struct {
	// StaticPlate: one georeferenced raster with projected pins.
	StaticPlate bool
	// CameraFlyOver: a certified LOD pyramid flown between route endpoints.
	CameraFlyOver bool
	// NativeComposition: one documentary composition of the native gallery
	// (the executing renderer's own vocabulary, PortedMapCompositions) drawn
	// from grounded places alone.
	NativeComposition bool
	// DynamicCameraTour: a live tile-pyramid camera following spoken timing.
	DynamicCameraTour bool
	// LabelAnimation / EditorialComposition / MultiPinSystem: presentation
	// features only the dynamic renderer can draw.
	LabelAnimation       bool
	EditorialComposition bool
	MultiPinSystem       bool
	// PerPinTiming: the camera must activate each place when it is spoken.
	PerPinTiming bool
}

// MapBackendCapabilities is what a backend can REALLY render. The runtime
// resolves the backend from requirements ⊆ capabilities; an unmet requirement
// is a typed error, never a silent downgrade.
type MapBackendCapabilities struct {
	StaticPlates          bool
	CameraFlyOver         bool
	NativeCompositions    bool
	DynamicCameraTours    bool
	LabelAnimations       bool
	EditorialCompositions bool
	MultiPinSystems       bool
	PerPinTiming          bool
}

// NativeMapCapabilities: the Chronon MAP compiler lowers a certified plate
// (and its LOD camera) to image layers. It has no live tile pyramid, no label
// animations and no editorial scene compositor.
func NativeMapCapabilities() MapBackendCapabilities {
	return MapBackendCapabilities{
		StaticPlates:  true,
		CameraFlyOver: true,
	}
}

// NativeMapCompositions reports whether the single dynamic map renderer draws
// this composition from grounded places alone. It projects the one ported
// subset (capabilityoverlay.PortedMapCompositions), so servability can never
// be claimed twice.
func NativeMapCompositions(id string) bool {
	for _, served := range capabilityoverlay.PortedMapCompositions() {
		if served == strings.TrimSpace(id) {
			return true
		}
	}
	return false
}

// NativeMapBasemaps is the single projection of the basemap styles the native
// renderer draws: the editorial vector basemap only. The retired Python
// renderer fetched Esri raster tiles; raster styles are demolition debt and
// resolve to a typed error at the render boundary — never a silent style
// swap — so "random" draws from this subset and an explicit raster style
// fails closed by name.
func NativeMapBasemaps() map[string]bool {
	return map[string]bool{"chronon_editorial": true}
}

// DynamicMapCapabilities: the native dynamic map renderer of ChrononPlugin
// (maps_dynamic_map_render) draws the editorial vector basemap with the
// certified dive flyover and camera-aligned labels, plus its own documentary
// composition gallery from grounded places (NativeCompositions). Multi-stop
// camera tours, label animations, editorial scene compositions and per-pin
// spoken timing are not ported yet: those requirements fail closed with a
// typed error.
func DynamicMapCapabilities() MapBackendCapabilities {
	return MapBackendCapabilities{
		StaticPlates:       true,
		CameraFlyOver:      true,
		NativeCompositions: true,
	}
}

// Satisfies reports whether the capabilities cover every requirement.
func (c MapBackendCapabilities) Satisfies(r MapRenderRequirements) bool {
	return (!r.StaticPlate || c.StaticPlates) &&
		(!r.CameraFlyOver || c.CameraFlyOver) &&
		(!r.NativeComposition || c.NativeCompositions) &&
		(!r.DynamicCameraTour || c.DynamicCameraTours) &&
		(!r.LabelAnimation || c.LabelAnimations) &&
		(!r.EditorialComposition || c.EditorialCompositions) &&
		(!r.MultiPinSystem || c.MultiPinSystems) &&
		(!r.PerPinTiming || c.PerPinTiming)
}

// ResolveMapBackendFor selects the backend by REAL capabilities: the first
// backend whose capabilities cover the composition's requirements wins, and
// the reason is recorded for the map decision report. When no backend covers
// the requirements the resolution fails closed.
func ResolveMapBackendFor(r MapRenderRequirements) (MapBackend, string, error) {
	for _, candidate := range []struct {
		backend MapBackend
		caps    MapBackendCapabilities
	}{
		// The dynamic renderer first; the native compiler is the certified
		// static/LOD plate path.
		{MapBackendDynamic, DynamicMapCapabilities()},
		{MapBackendNative, NativeMapCapabilities()},
	} {
		if candidate.caps.Satisfies(r) {
			return candidate.backend, "capabilities cover all map render requirements", nil
		}
	}
	return "", "", fmt.Errorf("no map backend covers the requested render requirements (%s): %s", unmetMapRequirements(r), unmetMapRequirementsHint(r))
}

// unmetMapRequirements names the requirements no backend covers, so a failed
// map resolution reads as "which presentation feature is not ported" instead
// of a struct dump.
func unmetMapRequirements(r MapRenderRequirements) string {
	var names []string
	for _, candidate := range []struct {
		name  string
		met   bool
		claim bool
	}{
		{"StaticPlate", NativeMapCapabilities().StaticPlates, r.StaticPlate},
		{"CameraFlyOver", NativeMapCapabilities().CameraFlyOver, r.CameraFlyOver},
		{"NativeComposition", DynamicMapCapabilities().NativeCompositions, r.NativeComposition},
		{"DynamicCameraTour", DynamicMapCapabilities().DynamicCameraTours, r.DynamicCameraTour},
		{"LabelAnimation", DynamicMapCapabilities().LabelAnimations, r.LabelAnimation},
		{"EditorialComposition", DynamicMapCapabilities().EditorialCompositions, r.EditorialComposition},
		{"MultiPinSystem", DynamicMapCapabilities().MultiPinSystems, r.MultiPinSystem},
		{"PerPinTiming", DynamicMapCapabilities().PerPinTiming, r.PerPinTiming},
	} {
		if candidate.claim && !candidate.met {
			names = append(names, candidate.name)
		}
	}
	return strings.Join(names, ", ")
}

// unmetMapRequirementsHint names the demolition-debt families behind the
// unmet requirements (TICKET-MAPS-NATIVE-PORT): each one must land its
// bit-exact parity gate before it may be served.
func unmetMapRequirementsHint(r MapRenderRequirements) string {
	parts := []string{}
	if r.DynamicCameraTour || r.MultiPinSystem {
		parts = append(parts, "composed scenes/tours are demolition debt (parity gate pending) — request map_animation empty/random for the native gallery or the base flyover")
	}
	if r.EditorialComposition {
		parts = append(parts, "editorial scene compositions are demolition debt (parity gate pending)")
	}
	if r.LabelAnimation {
		parts = append(parts, "label animations are demolition debt (parity gate pending)")
	}
	if r.PerPinTiming {
		parts = append(parts, "per-pin spoken camera activation is demolition debt (parity gate pending)")
	}
	return strings.Join(parts, "; ")
}

// ResolveMapBackend conservatively selects the backend for a composition
// declared by (camera, label-composition) features. It is the compatibility
// projection of ResolveMapBackendFor over the classic two-flag input.
func ResolveMapBackend(hasCamera, hasLabelComposition bool) MapBackend {
	if hasCamera || hasLabelComposition {
		return MapBackendDynamic
	}
	return MapBackendNative
}

// MapRenderRequirementsFor derives the REAL render requirements of one map
// item from its semantic content: plate/camera geometry, the requested
// composition family, label animations and per-pin spoken timing. The backend
// is then resolved per item from these requirements — never from how many map
// items the plan happens to hold.
func MapRenderRequirementsFor(item capabilityoverlay.OverlayItem) MapRenderRequirements {
	req := MapRenderRequirements{}
	if item.Map == nil {
		return req
	}
	req.StaticPlate = true
	if item.Map.CameraMove != nil && len(item.Map.LODs) >= 2 {
		req.CameraFlyOver = true
	}
	if composition, _ := item.Params["map_animation"].(string); !MapAnimationPlaceholder(composition) {
		switch {
		case NativeMapCompositions(composition):
			req.NativeComposition = true
		case strings.HasPrefix(strings.TrimSpace(composition), "ed_"):
			req.EditorialComposition = true
		default:
			req.DynamicCameraTour = true
		}
	}
	if label, _ := item.Params["map_label_animation"].(string); strings.TrimSpace(label) != "" && strings.TrimSpace(label) != "none" {
		req.LabelAnimation = true
	}
	if _, ok := item.Params["map_pin_timing"]; ok {
		req.PerPinTiming = true
	}
	if _, ok := item.Params["map_pin_extras"]; ok {
		req.DynamicCameraTour = true
	}
	return req
}

// MapAnimationPlaceholder reports whether a map_animation value carries no
// presentation requirement of its own: empty, "random" and "auto" are draw
// placeholders resolved by the seeded sampler at the render boundary (the
// draw only ever selects compositions the executing renderer serves), so
// they demand no composition capability and are never written to the plan
// params as if they were concrete ids. A CONCRETE id is an explicit request
// and its requirements travel to the backend resolution.
func MapAnimationPlaceholder(composition string) bool {
	trimmed := strings.TrimSpace(composition)
	return trimmed == "" || strings.EqualFold(trimmed, "random") || strings.EqualFold(trimmed, "auto")
}

// MapExecutionPolicy makes the production preference explicit instead of
// disguising it as a capability draw. production_dynamic is the certified
// cartographic presentation; diagnostic_native routes only static content the
// native compiler genuinely covers; native_after_visual_certification is
// reserved for the future parity gate and currently behaves as production.
type MapExecutionPolicy string

const (
	MapExecutionProductionDynamic        MapExecutionPolicy = "production_dynamic"
	MapExecutionDiagnosticNative         MapExecutionPolicy = "diagnostic_native"
	MapExecutionNativeAfterCertification MapExecutionPolicy = "native_after_visual_certification"
)

// MapExecutionPolicyFromEnv resolves the operator intent. PIPELINEGEN_MAP_NATIVE_RENDER=1
// selects the diagnostic native path; PIPELINEGEN_MAP_EXECUTION_POLICY overrides
// it explicitly when set. Default is production_dynamic.
func MapExecutionPolicyFromEnv(nativeDiagnostic bool, override string) MapExecutionPolicy {
	switch MapExecutionPolicy(override) {
	case MapExecutionDiagnosticNative, MapExecutionProductionDynamic, MapExecutionNativeAfterCertification:
		if override == string(MapExecutionDiagnosticNative) {
			return MapExecutionDiagnosticNative
		}
		if override == string(MapExecutionNativeAfterCertification) {
			return MapExecutionNativeAfterCertification
		}
		return MapExecutionProductionDynamic
	}
	if nativeDiagnostic {
		return MapExecutionDiagnosticNative
	}
	return MapExecutionProductionDynamic
}

// ResolveMapItemBackendWithPolicy is the per-map backend decision used at the
// render boundary: the item's REAL requirements, resolved against backend
// capabilities under an explicit execution policy. It is a pure function of
// the item — the same map item decides identically whether the plan holds one
// map or several (the P0 rule: the backend is never chosen from the plan's
// item count).
func ResolveMapItemBackendWithPolicy(item capabilityoverlay.OverlayItem, policy MapExecutionPolicy) (MapBackend, string, error) {
	req := MapRenderRequirementsFor(item)
	if policy == MapExecutionDiagnosticNative && NativeMapCapabilities().Satisfies(req) {
		return MapBackendNative, "policy=diagnostic_native: native compiler covers all map render requirements", nil
	}
	backend, _, err := ResolveMapBackendFor(req)
	if err == nil {
		return backend, "policy=" + string(policy) + ": capabilities cover all map render requirements", nil
	}
	// PerPinTiming is the one DECLINABLE requirement: it marks grounded
	// per-pin spoken timing DATA riding on every planned map, and the native
	// base flyover presentation has no camera-activation concept (every place
	// stays presented, so nothing is dropped). The decline is declared — the
	// reason travels to the map decision report and the render payload — and
	// it applies only when timing is the single unmet requirement; any other
	// unmet requirement keeps failing closed with its typed error.
	if req.PerPinTiming {
		reduced := req
		reduced.PerPinTiming = false
		if reducedBackend, _, reducedErr := ResolveMapBackendFor(reduced); reducedErr == nil {
			declined := "policy=" + string(policy) + ": per-pin spoken timing declined (native base flyover presents every place continuously; camera-activation families await their parity gates)"
			return reducedBackend, declined, nil
		}
	}
	return "", "", err
}

// ResolveMapItemBackend is the per-map backend decision used at the render
// boundary: the item's REAL requirements, resolved against backend
// capabilities. It is a pure function of the item — the same map item decides
// identically whether the plan holds one map or several (the P0 rule: the
// backend is never chosen from the plan's item count).
//
// Production policy: the dynamic tile-pyramid renderer is the certified
// cartographic presentation for map overlays, so its capabilities are required
// unless the operator explicitly opted into the native compiler diagnostic
// (PIPELINEGEN_MAP_NATIVE_RENDER=1). Even then, a composition that needs
// dynamic-only capabilities still resolves to the dynamic backend: native
// visual parity is not established, so the diagnostic can only ever route
// static content the native compiler genuinely covers — never force dynamic
// content onto it.
func ResolveMapItemBackend(item capabilityoverlay.OverlayItem, nativeDiagnostic bool) (MapBackend, string, error) {
	return ResolveMapItemBackendWithPolicy(item, MapExecutionPolicyFromEnv(nativeDiagnostic, ""))
}
