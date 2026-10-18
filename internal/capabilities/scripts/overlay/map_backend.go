// Package overlay — map_backend.go: backend cartografico selezionabile.
//
// Moved from the scripts root (package hotspot forward-prevention gate;
// this subpackage is the sanctioned migration target): same behavior.
//
// Transizione: PipelineGen invia la specifica semantica di mappa; il resolver
// determina quale backend supporta la composizione richiesta:
//
//	Native Chronon — mappe già supportate dal compiler nativo.
//	Python Map Backend — composizioni avanzate non ancora portate.
//
// Da conservare: il backend Python in ChrononTemplate finché non c'è parità.
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
	MapBackendNative MapBackend = "native"
	MapBackendPython MapBackend = "python"
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

// PythonMapCapabilities: ChrononTemplate's dynamic tile-pyramid renderer is
// the only backend for live camera tours, camera flyovers, label animations,
// editorial compositions, multi-pin systems and per-pin spoken timing.
func PythonMapCapabilities() MapBackendCapabilities {
	return MapBackendCapabilities{
		StaticPlates:          true,
		CameraFlyOver:         true,
		DynamicCameraTours:    true,
		LabelAnimations:       true,
		EditorialCompositions: true,
		MultiPinSystems:       true,
		PerPinTiming:          true,
	}
}

// Satisfies reports whether the capabilities cover every requirement.
func (c MapBackendCapabilities) Satisfies(r MapRenderRequirements) bool {
	return (!r.StaticPlate || c.StaticPlates) &&
		(!r.CameraFlyOver || c.CameraFlyOver) &&
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
		// Python first: the dynamic renderer covers every requirement, while
		// the native compiler is the certified static/LOD path.
		{MapBackendPython, PythonMapCapabilities()},
		{MapBackendNative, NativeMapCapabilities()},
	} {
		if candidate.caps.Satisfies(r) {
			return candidate.backend, "capabilities cover all map render requirements", nil
		}
	}
	return "", "", fmt.Errorf("no map backend covers the requested render requirements: %+v", r)
}

// ResolveMapBackend conservatively selects the backend for a composition
// declared by (camera, label-composition) features. It is the compatibility
// projection of ResolveMapBackendFor over the classic two-flag input.
func ResolveMapBackend(hasCamera, hasLabelComposition bool) MapBackend {
	if hasCamera || hasLabelComposition {
		return MapBackendPython
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
	if composition, _ := item.Params["map_animation"].(string); strings.TrimSpace(composition) != "" {
		if strings.HasPrefix(strings.TrimSpace(composition), "ed_") {
			req.EditorialComposition = true
		} else {
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
	req := MapRenderRequirementsFor(item)
	if !nativeDiagnostic && item.Map != nil {
		req.DynamicCameraTour = true
	}
	if nativeDiagnostic && NativeMapCapabilities().Satisfies(req) {
		return MapBackendNative, "native compiler covers all map render requirements (diagnostic opt-in)", nil
	}
	return ResolveMapBackendFor(req)
}

// AnnotateMapBackend registra sul piano quale backend deve eseguire la mappa.
// Il worker legge params.map_backend; assente = comportamento storico.
// Every map item receives its own annotation: the backend is a PER-MAP
// capability decision, so one video holding several maps keeps the same
// decision rules as a single-map plan.
func AnnotateMapBackend(plan *capabilityoverlay.OverlayPlan, backend MapBackend) {
	if plan == nil || len(plan.Items) == 0 {
		return
	}
	for i := range plan.Items {
		if plan.Items[i].Map == nil && i != 0 {
			continue
		}
		if plan.Items[i].Params == nil {
			plan.Items[i].Params = map[string]any{}
		}
		plan.Items[i].Params["map_backend"] = string(backend)
	}
}
