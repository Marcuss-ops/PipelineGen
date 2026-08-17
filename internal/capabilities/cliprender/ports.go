package cliprender

// ports.go defines the narrow, technology-independent ports the parallel
// preparation phase consumes. Every adapter is wired at the composition root
// (internal/app) from concrete platform/application implementations; the
// capability never imports infrastructure, Drive, Whisper, or SQLite.
//
// Verdetto invariant (mirror of internal/capabilities/scripts/ports.go): no
// port returns a technology-specific type — every return value is a
// capability-owned type from this package.

import (
	"context"
)

// AssetResolver resolves a canonical asset_id to its registry identity.
// The concrete adapter reads the canonical asset registry (SQLite) and maps
// the row to an AssetRef. Fail-closed: an unknown asset_id is a typed error,
// never a silent empty ref.
type AssetResolver interface {
	ResolveAsset(ctx context.Context, assetID string) (*AssetRef, error)
}

// AssetMaterializer ensures an asset's bytes are available locally and
// returns the verified local artifact (path + sha256). It is idempotent:
// an already-local copy is returned without a download (FromCache=true).
// Fail-closed: an asset with neither a usable local copy nor a Drive source
// is a typed error, never a silent no-op path.
type AssetMaterializer interface {
	Materialize(ctx context.Context, ref AssetRef) (*MaterializedAsset, error)
}

// TranscriptResolver owns the canonical transcript mechanics: reuse the
// existing READY canonical text track (Lookup) or generate one from the
// materialized source audio (Generate). The capability owns the policy
// (reuse vs generate vs reuse_or_generate); the resolver owns the mechanics.
//
// Lookup returns (result, true, nil) when a READY track exists, (nil, false,
// nil) when none exists, and (nil, false, err) on a repository failure.
type TranscriptResolver interface {
	Lookup(ctx context.Context, in TranscriptInput) (*TranscriptResult, bool, error)
	Generate(ctx context.Context, in TranscriptInput, source *MaterializedAsset) (*TranscriptResult, error)
}

// ContractResolver resolves the output contract selected by the request into
// the fully-specified VeloxEditing contract. The canonical implementation is
// pure (contract.go); the port exists so tests and future contracts can
// inject alternatives.
type ContractResolver interface {
	Resolve(ctx context.Context, req *RenderRequest) (*ResolvedContract, error)
}
