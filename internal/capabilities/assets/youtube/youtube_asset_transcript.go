package youtube

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// AssetTranscriptSource resolves a transcript for a canonical media asset
// (asset_id) when the request is not a YouTube URL. The implementation is
// wired by the composition root; nil answers 503, never a silent empty 200.
//
// found=false means "no transcript exists" (no READY text track and ASR found
// no speech); an acquisition/materialization failure is returned as an error.
type AssetTranscriptSource interface {
	FetchAssetTranscript(ctx context.Context, assetID, language string, startSec, endSec int) (*detail.ResolvedTextBundle, bool, error)
}
