// Package mediasearch provides the application-layer use case for the
// single private media-search API (QDRANT-004):
//
//	POST /internal/v1/media/search
//
// Clients submit semantic queries and app-level filters only; the
// service owns embedding generation, vector-store interaction, SQLite
// hydration (canonical metadata), workspace enforcement, and signed
// delivery URL minting. Clients never see collection names, vector
// names, raw drive_file_ids, or local filesystem paths.
//
// The package contains three ports (interfaces) and their domain DTOs:
//   - VectorSearchPort: existing application/assets/search port (re-used, not redefined).
//   - MediaReadRepository: batched SQLite read by asset IDs.
//   - AssetDeliveryService: short-TTL signed URL minter.
//
// Cross-cutting types (WorkspaceContext, MediaSearchRequest,
// MediaSearchResponse, SearchHit) live in types.go.
//
// QDRANT-004 BLOCKER NOTE: this package depends on the schema
// versioning / transactional outbox landing in QDRANT-001..003. Until
// those ship, the workspace isolation enforcement is auth-context
// only; SQL-level cross-tenant filtering is deferred to QDRANT-001
// (the media_assets.workspace_id column does not exist yet).
package mediasearch

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// WorkspaceContext is the per-request authorisation envelope extracted
// from the API middleware. The service treats WorkspaceID as REQUIRED:
// an empty value is a programming error (the middleware should have
// rejected it).
type WorkspaceContext struct {
	WorkspaceID string
	ProjectID   string
	PrincipalID string // optional, propagated for audit logging
	IsAdmin     bool   // Admin principals may pick arbitrary workspaces
}

// ErrMissingWorkspace is returned when the service is invoked without
// a workspace context. Handler maps this to HTTP 403.
var ErrMissingWorkspace = errors.New("mediasearch: workspace context required")

// MediaReadRepository fetches canonical asset metadata from SQLite.
//
// The implementation MUST:
//   - accept the workspace_id from the auth context (defence-in-depth
//     against cross-tenant reads; future-proof for the
//     media_assets.workspace_id column from QDRANT-001);
//   - batch by IDs (no N+1 — single SQL statement per call);
//   - exclude soft-deleted rows (lifecycle_state == 'deleted');
//   - return rows in the same order as the input where possible.
//
// The service is the only owner of the local-path field; it MUST NOT
// surface local_path to callers (see Service.toSafeHit).
type MediaReadRepository interface {
	GetMany(ctx context.Context, workspace WorkspaceContext, assetIDs []string) ([]MediaAsset, error)
}

// AssetDeliveryService mints short-lived signed URLs that authorise a
// client to download or stream an asset's bytes for a bounded TTL.
//
// Signatures MUST be HMAC-SHA256 with a server-side secret of at least
// 32 bytes (the same rotation policy as pkg/hmacsign — see
// delivery/signer.go for the canonical ergonomic helpers).
type AssetDeliveryService interface {
	BuildAuthorizedURL(ctx context.Context, workspace WorkspaceContext, assetID string) (string, error)
}

// VectorSearchPort is the local port the orchestrator depends on.  It
// combines embedding generation (EmbedTextForVector) with vector-store
// access (VectorStore) so the service doesn't need to manage two
// separate dependencies.  The production concrete is wired at the
// composition root; test stubs implement both methods.
type VectorSearchPort interface {
	EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error)
	VectorStore() search.VectorStorePort
}

// ── Re-exports ───────────────────────────────────────────────────────────
//
// The MediaSearch service consumes the existing
// application/assets/search ports (HybridSearchRequest, etc.) directly
// — no local re-declaration. The only NEW shape needed is below.

const (
	ChannelDense      = "dense_text"
	ChannelSparseBM25 = "bm25"
	// ChannelTranscript is intentionally absent: the orchestrator
	// does NOT pass a transcript-channel vector to VectorStore.HybridSearch
	// today — passing the same dense vector would silently inflate
	// qdrant.fuseSearchResults, and a dedicated transcript embedder is
	// the right fix (QDRANT-005 follow-up territory). Reintroduce the
	// constant when the channel returns to the wire format.
)

// SearchMode toggles ANN vs. hybrid (dense + sparse + transcript)
// retrieval. Default is Hybrid (matches QDRANT-004's "real hybrid search"
// acceptance criterion: dense+sparse actually fused, not just relabeled).
type SearchMode string

const (
	SearchModeANN    SearchMode = "ann"
	SearchModeHybrid SearchMode = "hybrid"
)

// MediaAsset is the canonical asset shape served to clients. It
// deliberately does NOT carry LocalPath or any other server-internal
// locator — those fields are runtime-only and never serialised.
type MediaAsset struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Source     string   `json:"source"`
	MediaType  string   `json:"media_type"`
	Category   string   `json:"category"`
	Tags       []string `json:"tags,omitempty"`
	Language   string   `json:"language,omitempty"`
	DurationMs int      `json:"duration_ms,omitempty"`
	Width      int      `json:"width,omitempty"`
	Height     int      `json:"height,omitempty"`
	SearchText string   `json:"search_text,omitempty"`
}
