package capabilities

import (
	"context"
	"fmt"

	assembly "github.com/Marcuss-ops/PipelineGen/internal/application/assembly"
	kernelassembly "github.com/Marcuss-ops/PipelineGen/internal/kernel/assembly"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type assemblyClipReader interface {
	ResolveByMediaAssetID(context.Context, string) (*asset.Asset, error)
}
type assemblyAssetResolver struct{ clips assemblyClipReader }

func newAssemblyAssetResolver(clips assemblyClipReader) assembly.AssetResolver {
	return &assemblyAssetResolver{clips: clips}
}
func (r *assemblyAssetResolver) Resolve(ctx context.Context, ids []string) ([]kernelassembly.AssetRequirement, error) {
	if r == nil || r.clips == nil {
		return nil, fmt.Errorf("assembly asset resolver is not configured")
	}
	out := make([]kernelassembly.AssetRequirement, 0, len(ids))
	for _, id := range ids {
		a, err := r.clips.ResolveByMediaAssetID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("resolve clip %q: %w", id, err)
		}
		if a == nil {
			return nil, fmt.Errorf("resolve clip %q: not found", id)
		}
		location := a.DownloadLink()
		if location == "" {
			location = a.DriveLink()
		}
		if location == "" {
			location = a.LocalPath()
		}
		sha := a.GetMetadataString("sha256")
		out = append(out, kernelassembly.AssetRequirement{AssetID: a.ID, Kind: "source_clip", SHA256: sha, Location: location, Availability: kernelassembly.AvailabilityKnown, Required: true})
	}
	return out, nil
}
