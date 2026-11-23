package media

// deps.go owns the typed input contracts of the media module.
//
// The preflight constructor used to take five positional arguments, two of
// which are usually nil and have different types (AudioAssetSource,
// ClipAudioAssetSource): a caller that swapped the two nils still compiled.
// Named fields make the omission explicit and self-documenting. The committer
// factory takes the same shape for symmetry with the rest of the module
// (BON-03 step B).

import (
	"database/sql"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"go.uber.org/zap"
)

// PreflightDeps is the input contract of NewPreflight. Assets is the canonical
// asset-details lookup the clip prober reads through; the two audio sources and
// the two probes are optional surfaces (nil keeps that check a no-op).
type PreflightDeps struct {
	Assets               AssetDetailsLookup
	AudioAssetSource     scriptgen.AudioAssetSource
	ClipAudioAssetSource scriptgen.ClipAudioAssetSource
	ImageProviderHealth  scriptgen.ImageProviderHealthProbe
	ProviderAvailability scriptgen.VidRushProviderAvailabilityProbe
}

// EditorialAssetsDeps is the input contract of EnsureEditorialAssets. Bytes is
// the canonical Drive materializer; Options is the already-validated plate
// selection. ctx stays an explicit first argument because it is a call-scoped
// deadline, not a dependency.
type EditorialAssetsDeps struct {
	DB      *sql.DB
	Bytes   EditorialAssetBytesSource
	Options EditorialAssetsOptions
	Log     *zap.Logger
}

// CommitterDeps is the input contract of NewPostgresMediaCommitter. DB is the
// single media Postgres handle the outbox repository and the registry ledger
// are both derived from, so the committer's transaction stays on one engine.
type CommitterDeps struct {
	DB  *sql.DB
	Log *zap.Logger
}
