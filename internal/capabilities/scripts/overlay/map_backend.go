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
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// MapBackend selects which renderer executes a map composition.
type MapBackend string

const (
	MapBackendNative MapBackend = "native"
	MapBackendPython MapBackend = "python"
)

// ResolveMapBackend sceglie il backend per una composizione mappa.
// Euristica conservativa: pin/route/georeferenziate pure → native;
// camera/label/composition editoriali Python → python finché non portate.
func ResolveMapBackend(hasCamera, hasLabelComposition bool) MapBackend {
	if hasCamera || hasLabelComposition {
		return MapBackendPython
	}
	return MapBackendNative
}

// AnnotateMapBackend registra sul piano quale backend deve eseguire la mappa.
// Il worker legge params.map_backend; assente = comportamento storico.
func AnnotateMapBackend(plan *capabilityoverlay.OverlayPlan, backend MapBackend) {
	if plan == nil || len(plan.Items) == 0 {
		return
	}
	if plan.Items[0].Params == nil {
		plan.Items[0].Params = map[string]any{}
	}
	plan.Items[0].Params["map_backend"] = string(backend)
}
