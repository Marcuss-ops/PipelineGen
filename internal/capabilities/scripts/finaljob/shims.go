// shims.go — i nomi che nel root erano alias di package per la foglia.
//
// Il final-job è stato scritto contro il vocabolario del root
// (GenerateResult, Scene, Stage, ...), che nel root sono alias dei veri
// proprietari: contract per i modelli, runstate per il vocabolario di
// stadio, renderunits per la proiezione delle unità di render. La foglia
// ripete quegli alias in modo esplicito, una volta sola, invece di
// riscrivere centinaia di firme.
package finaljob

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/renderunits"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runstate"

	"os"
)

type (
	GenerateRequest          = contract.GenerateRequest
	GenerateResult           = contract.GenerateResult
	Scene                    = contract.Scene
	ClipReference            = contract.ClipReference
	Language                 = contract.Language
	RenderReference          = contract.RenderReference
	RemoteFinalJobResult     = contract.RemoteFinalJobResult
	SemanticProductionPlan   = contract.SemanticProductionPlan
	OverlayAuditEntityImages = contract.OverlayAuditEntityImages
	capmodelItem             = capmodel.OverlayItem

	Stage     = runstate.Stage
	RunStatus = runstate.RunStatus

	SceneRenderUnit = renderunits.SceneRenderUnit
)

const (
	StageCompilingAudio = runstate.StageCompilingAudio

	// EntityImageReasonBound is the success case of the entity-image audit:
	// the plan carries at least one bound image for the entity.
	EntityImageReasonBound = "bound"
)

var (
	StageIndex = runstate.StageIndex

	RenderUnitsForScene          = renderunits.RenderUnitsForScene
	sceneVisualClipsAreStockOnly = renderunits.SceneVisualClipsAreStockOnly
	sceneStockClipIDs            = renderunits.SceneStockClipIDs
	sceneClips                   = renderunits.SceneClips
)

// OverlayItemCandidate is one renderable overlay item together with its
// position in the sealed plan. It is local to the overlay manifest
// projection: the render queue declares its own copy because the two
// packages never exchange values.
type OverlayItemCandidate struct {
	Index  int
	Source capmodelItem
}

// usableLocalAssetPath reports whether a path points at a real local file the
// Master can upload.
func usableLocalAssetPath(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
