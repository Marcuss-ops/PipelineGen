package renderinggen

import (
	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

func toQueuePreparedRenderPackage(ref *renderqueue.RenderQueuePreparedPackageRef) *queueclient.PreparedRenderPackageRef {
	if ref == nil {
		return nil
	}
	return &queueclient.PreparedRenderPackageRef{
		PackageID: ref.PackageID, PlanSHA256: ref.PlanSHA256,
		AssetManifestSHA: ref.AssetManifestSHA, TotalFrames: ref.TotalFrames, CompilerVersion: ref.CompilerVersion,
		ProfileID: ref.ProfileID,
	}
}

func fromQueuePreparedRenderPackage(ref *queueclient.PreparedRenderPackageRef) *renderqueue.RenderQueuePreparedPackageRef {
	if ref == nil {
		return nil
	}
	return &renderqueue.RenderQueuePreparedPackageRef{
		PackageID: ref.PackageID, PlanSHA256: ref.PlanSHA256,
		AssetManifestSHA: ref.AssetManifestSHA, TotalFrames: ref.TotalFrames, CompilerVersion: ref.CompilerVersion,
		ProfileID: ref.ProfileID,
	}
}
