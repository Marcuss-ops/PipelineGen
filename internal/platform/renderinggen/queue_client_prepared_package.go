package renderinggen

import (
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

func toQueuePreparedRenderPackage(ref *scriptgen.RenderQueuePreparedPackageRef) *queueclient.PreparedRenderPackageRef {
	if ref == nil {
		return nil
	}
	return &queueclient.PreparedRenderPackageRef{
		PackageID: ref.PackageID, PlanSHA256: ref.PlanSHA256,
		AssetManifestSHA: ref.AssetManifestSHA, TotalFrames: ref.TotalFrames, CompilerVersion: ref.CompilerVersion,
		ProfileID: ref.ProfileID,
	}
}

func fromQueuePreparedRenderPackage(ref *queueclient.PreparedRenderPackageRef) *scriptgen.RenderQueuePreparedPackageRef {
	if ref == nil {
		return nil
	}
	return &scriptgen.RenderQueuePreparedPackageRef{
		PackageID: ref.PackageID, PlanSHA256: ref.PlanSHA256,
		AssetManifestSHA: ref.AssetManifestSHA, TotalFrames: ref.TotalFrames, CompilerVersion: ref.CompilerVersion,
		ProfileID: ref.ProfileID,
	}
}
