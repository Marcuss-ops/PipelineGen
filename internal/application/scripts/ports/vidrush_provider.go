package ports

import (
	"context"
	"encoding/json"
	"errors"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

var (
	ErrVidRushProviderNotFound       = errors.New("vidrush provider not registered")
	ErrVidRushProviderDuplicate      = errors.New("vidrush provider already registered")
	ErrVidRushProviderRegistryFrozen = errors.New("vidrush provider registry is frozen")
)

// VidRushSearchRequest is shared by all visual providers. Provider-specific
// transport details stay behind the provider implementation.
type VidRushSearchRequest struct {
	SegmentID string
	SceneID   string
	TextHash  string
	Text      string
	Query     string
	Limit     int
}

// LocalArtifact is the provider-to-verifier handoff. Providers may resolve a
// stream URL and download into LocalPath, but they do not write media_assets.
type LocalArtifact struct {
	Candidate     scriptpkg.SegmentAssetCandidate
	LocalPath     string
	MIMEType      string
	SizeBytes     int64
	LegacyFileMD5 string
	Manifest      *job.ArtifactManifest
}

// VerifiedArtifact is the only artifact shape accepted by the common
// finalization path. Technical verification and rights policy are explicit.
type VerifiedArtifact struct {
	Candidate        scriptpkg.SegmentAssetCandidate
	LocalPath        string
	MIMEType         string
	SizeBytes        int64
	LegacyFileMD5    string
	DurationMs       int64
	Width            int
	Height           int
	RightsStatus     string
	VerificationNote string
	Manifest         *job.ArtifactManifest
}

// VidRushAssetProvider is the common application port for Artlist, web-image
// retrieval and image generation. Persistence remains outside this interface.
type VidRushAssetProvider interface {
	Name() string
	Search(context.Context, VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error)
	Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (LocalArtifact, error)
	Verify(context.Context, LocalArtifact) (VerifiedArtifact, error)
}

// VidRushArtifactFinalizer is the common persistence boundary. Concrete
// providers stop at VerifiedArtifact; this port owns the canonical
// VerifiedArtifact -> Drive -> SQLite/media_assets -> outbox -> Qdrant path.
type VidRushArtifactFinalizer interface {
	Finalize(context.Context, VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error)
}

// VidRushCachePort is the durable L2 cache for provider discovery and
// materialization. Implementations belong to infrastructure; processors only
// exchange JSON payloads through this typed port so warm replay survives a
// process restart without coupling application code to SQLite.
type VidRushCachePort interface {
	Get(context.Context, string, string) ([]byte, bool, error)
	Put(context.Context, string, string, []byte) error
}

// ValidateVidRushCachePayload keeps cache writes deterministic and prevents a
// malformed value from becoming a durable successful hit.
func ValidateVidRushCachePayload(payload []byte) error {
	var value json.RawMessage
	if err := json.Unmarshal(payload, &value); err != nil {
		return err
	}
	if len(value) == 0 || string(value) == "null" {
		return errors.New("empty or null cache payload")
	}
	return nil
}
