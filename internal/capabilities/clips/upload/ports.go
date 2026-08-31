// Package upload — typed ports scoped to the clip-upload use case.
//
// Wave 14 step 1 (June 2026): the 13-step orchestration previously inlined
// in internal/capabilities/assets/clips/clip_upload.go is extracted into this package.
// To keep AGENTS.md Pattern 0 compliant, only typed ports are declared here
// — concrete adapters live in internal/app/clips_adapters_*.go with
// `var _ <Port> = (*<Adapter>)(nil)` compile-time assertions.
//
// Canonical ports reused (declared in parent internal/capabilities/clips/ports.go):
//   - ClipDriveUploaderPort     — Drive folder/upload/list operations
//   - ClipIndexDispatcherPort   — atomic UPSERT + outbox event
//   - ClipConfigPort            — typed config accessor (clips/TempPath/etc.)
//   - ClipTreeBuilderPort       — asset-tree UpsertFromAsset
//
// New upload-scoped port:
//   - ArtifactServicePort       — content-addressed staging (CreateAndVerify, LocalPath)
//     The concrete *artifacts.Service lives at
//     internal/capabilities/assets/artifacts; the adapter wraps it so
//     the use case has zero infra-shaped imports.
package clips

import (
	"context"
	"io"

	clips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
)

// ── Reused canonical ports (compile-time type aliases, NOT new interfaces) ──
// Type aliases keep the use case readable while honouring the DRY rule:
// the canonical port signature lives once in parent ports.go.

// DriveUploader is the canonical narrow Drive surface reused from
// internal/capabilities/clips.ClipDriveUploaderPort. Adapter:
// clipsDriveAdapter (internal/app/clips_adapters_drive.go).
type DriveUploader = clips.ClipDriveUploaderPort

// IndexDispatcher is the canonical atomic UPSERT + outbox port reused
// from internal/capabilities/clips.ClipIndexDispatcherPort. Adapter:
// clipsDispatcherAdapter (internal/app/clips_dispatcher_adapter.go).
type IndexDispatcher = clips.ClipIndexDispatcherPort

// Config is the canonical typed-config surface reused from
// internal/capabilities/clips.ClipConfigPort. Adapter:
// clipsCfgAdapter (internal/app/clips_adapters_cfg.go).
type Config = clips.ClipConfigPort

// TreeBuilder is the canonical asset-tree builder reused from
// internal/capabilities/clips.ClipTreeBuilderPort. Adapter:
// clipsAssetTreeAdapter (internal/app/clips_adapters_index.go).
type TreeBuilder = clips.ClipTreeBuilderPort

// Publisher is the canonical Drive publisher port reused from
// internal/capabilities/clips.ClipPublisherPort. Adapter wraps
// delivery.Publisher at the composition root.
type Publisher = clips.ClipPublisherPort

// ── New upload-scoped port ───────────────────────────────────────────────────

// ArtifactCreateInput is the narrowed input shape for
// ArtifactServicePort.CreateAndVerify. Named with the Artifact prefix
// to avoid shadowing internal/capabilities/assets/artifacts.CreateInput
// (both happen to carry the same 4 fields). The canonical
// artifacts.CreateInput is the source-of-truth; this struct is the
// port-side projection the upload use case consumes — extra fields
// would force the handler to fabricate values it doesn't have.
type ArtifactCreateInput struct {
	ID       string
	Kind     string
	MimeType string
	Reader   io.Reader
}

// ArtifactRef is the narrowed result shape. Only the 3 fields the upload
// pipeline actually reads (ID for LocalPath round-trip, SHA256 for
// dedup-by-content + re-derived clipID, SizeBytes for telemetry).
// DownloadLink / WebViewLink / MD5Checksum / etc. live on DriveUploadResult
// (the Drive port) — keep them separate so the artifact port isn't asked
// to grow as Drive's DTO grows.
type ArtifactRef struct {
	ID        string
	SHA256    string
	SizeBytes int64
}

// ArtifactServicePort is the narrow port for content-addressed staging.
// Replaces the direct *artifacts.Service reach-through from the old
// clip_upload.go handler — keeps AGENTS.md Pattern 8 ("api/ = thin
// transport only") intact.
type ArtifactServicePort interface {
	CreateAndVerify(ctx context.Context, in ArtifactCreateInput) (*ArtifactRef, error)
	LocalPath(ctx context.Context, id string) (string, error)
}
