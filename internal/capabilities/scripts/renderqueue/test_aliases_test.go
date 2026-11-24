package renderqueue

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	finaljob "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/finaljob"
	kernrenderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

type RenderArtifact = contract.RenderArtifact
type OverlayPublicationSpec = kernrenderqueue.OverlayPublicationSpec

func overlayRenderableCandidates(plan capmodel.OverlayPlan) []finaljob.OverlayItemCandidate {
	return finaljob.OverlayRenderableCandidates(plan)
}
