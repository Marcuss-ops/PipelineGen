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

	assetssearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// WorkspaceContext is the per-request authorisation envelope extracted
// from the API middleware. The service treats WorkspaceID as REQUIRED:
// an empty value is a programming error (the middleware should have
// rejected it).
//
// DEPRECATION-DEADLINE 2026-08-01: this type is superseded by the
// canonical search.Actor (godlike/06 SSOT — the search capability
// owns its own workspace enforcement contract). The
// PrincipalID/WorkspaceID/IsAdmin field set maps one-to-one; the
// only field removed is ProjectID (the canonical search.Actor does
// not carry it). After 2026-08-01, callers MUST migrate to
// search.Actor and this type is removed (godlike/07
// zero-baseline rule, godlike/06 SSOT migration).
type WorkspaceContext struct {
	WorkspaceID string
	ProjectID   string
	PrincipalID string // optional, propagated for audit logging
	IsAdmin     bool   // Admin principals may pick arbitrary workspaces
}

// ErrMissingWorkspace is returned when the service is invoked without
// a workspace context. Handler maps this to HTTP 403.
//
// Commit 2 BACKFILL/CUTOVER (July 2026): re-aliased as a Go-level
// pointer-identical deprecation of the canonical
// search.ErrMissingWorkspace. Two-way identity preserves
// `errors.Is(err, search.ErrMissingWorkspace)` for legacy callers
// that still hit this package's sentinel. The canonical source of
// truth is now search.ErrMissingWorkspace — new code MUST use the
// canonical (godlike/06 SSOT — the search capability owns its own
// workspace enforcement contract).
var ErrMissingWorkspace = search.ErrMissingWorkspace

// ErrHybridRequiresSparse is the legacy deprecation alias of
// canonical search.ErrHybridRequiresSparse. Re-exported via
// Go-level pointer identity so errors.Is traverses transparently
// regardless of which package the caller imported.
//
// Commit 2 BACKFILL/CUTOVER (July 2026): pointer-identical alias.
// New code MUST use search.ErrHybridRequiresSparse — only legacy
// callers should reference the alias.
//
// Pre-promotion history (kept for audit): this sentinel was paired
// with qdrant.ErrSparseRequired (infrastructure-level sibling) per
// QDRANT-004 PR1 (June 2026). The "orchestrator must NEVER silently
// degrade a hybrid request to ANN" invariant is preserved — only
// the owner package moved from mediasearch → search.
var ErrHybridRequiresSparse = search.ErrHybridRequiresSparse

// ErrNoBackendAvailable is the legacy deprecation alias of canonical
// search.ErrNoBackendAvailable. PR-AGENTE2-ERRORS (Agente 2, Azione 4)
// was the origin; Commit 2 BACKFILL/CUTOVER (July 2026) promoted it.
var ErrNoBackendAvailable = search.ErrNoBackendAvailable

// ErrAllBackendsFailed is the legacy deprecation alias of canonical
// search.ErrAllBackendsFailed. PR-AGENTE2-ERRORS (Agente 2, Azione 4)
// was the origin; Commit 2 BACKFILL/CUTOVER (July 2026) promoted it.
var ErrAllBackendsFailed = search.ErrAllBackendsFailed

// SearchableLifecycleStates is the canonical allowlist of
// lifecycle_state values that survive the hydration phase (PR 1 —
// Lifecycle state SSOT, June 2026). The single value is ACTIVE;
// pre-PR1 the list was {"active", "searchable"} (both legacy
// lowercase values pruned by migration 101). Anything outside this
// set — STAGING, PROCESSING, DELETE_PENDING, DELETED, ERROR — MUST
// be filtered both in SQL (primary) and in the post-query guard
// (defence-in-depth). The orchestrator (mediasearch.Service) sends
// this list to MediaReadRepository.GetMany as the default
// allowStates argument unless an explicit caller override is
// supplied via MediaSearchFilter.States.
var SearchableLifecycleStates = []string{"ACTIVE"}

// AllNonSearchableLifecycleStates is the explicit deny-list used
// for the post-query defence layer. If the SQL filter ever drifts
// to allow one of these (a real risk after migrations), the
// post-query guard catches it before the row exits the seam.
//
// PR 1 (June 2026) rewrite: every value is the canonical UPPERCASE
// shape from asset.LifecycleState (no lowercase counterparts —
// production no longer emits them post-101). The pre-PR1 list
// carried legacy mixed-case values; post-101 those values cannot
// appear in the column, so they cannot leak through the seam.
var AllNonSearchableLifecycleStates = []string{
	"STAGING",
	"PROCESSING",
	"DELETE_PENDING",
	"DELETED",
	"ERROR",
}

// MediaReadRepository fetches canonical asset metadata from SQLite.
//
// QDRANT-004 PR3 (June 2026): the interface now takes an
// allowStates []string argument so hydration can apply the
// lifecycle_state allowlist at the SQL layer (primary defence).
// The post-query guard in mediasearch.Service layers a defence-
// in-depth filter on top, so a SQL drift or a test adapter that
// ignores allowStates still does not leak deleted/archived/pending
// rows into SearchHit responses.
//
// The implementation MUST:
//   - accept the workspace_id from the auth context (defence-in-depth
//     against cross-tenant reads; future-proof for the
//     media_assets.workspace_id column from QDRANT-001);
//   - batch by IDs (no N+1 — single SQL statement per call);
//   - filter by lifecycle_state IN (allowStates) when allowStates
//     is non-empty (forward-compatible — if the concrete adapter is
//     not yet wired to honour this argument, the post-query guard
//     still enforces the same allowlist);
//   - return rows in the same order as the input where possible.
//
// The service is the only owner of the local-path field; it MUST NOT
// surface local_path to callers (see Service.toSafeHit).
type MediaReadRepository interface {
	GetMany(
		ctx context.Context,
		workspace WorkspaceContext,
		assetIDs []string,
		allowStates []string,
	) ([]MediaAsset, error)
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
//
// Deprecated: migration target is the canonical Fase 6 split:
//
//   - EmbedTextForVector → search.QueryEmbedder (Embed(ctx, text))
//     (defined at internal/application/search/ports.go::QueryEmbedder;
//     production concrete: ollama embedder via qdrant.TextEmbedder;
//     wiring: internal/app/adapters_infra.go::searchEmbedAdapter).
//
//   - VectorStore() → assets/search.VectorStorePort (unchanged; this
//     is already the canonical ANN/hybrid store surface — Qdrant-004).
//
// Migration deadline: 2026-08-15 (BACKFILL of architecture/deprecations.yaml
// #SEARCH-VECTORSEARCHPORT-MERGE). Removal will follow once every caller
// (e2e regression tests + composition wiring routes) is migrated; the
// EXPAND→BACKFILL→CUTOVER sequence per godlike/07 §Zero-Legacy Policy.
type VectorSearchPort interface {
	EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error)
	VectorStore() assetssearch.VectorStorePort
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

// SearchMode lives in types.go as a Go-level alias of the canonical
// search.SearchMode (Wave 21 PR 8). The constants below still
// compile because aliases are bidirectional type identity — the
// underlying type is `string`, so constants of the alias work.
//
// This block is preserved (constants live here, type lives in
// types.go) to keep the file-by-file dependency lattice simple:
// ports.go owns the request/response sentinels (VectorSearchPort,
// MediaReadRepository, AssetDeliveryService) and the channel-name
// constants; types.go owns the data DTOs and the Mode enum.

const (
	SearchModeANN    SearchMode = "ann"
	SearchModeHybrid SearchMode = "hybrid"
)

// MediaAsset is the canonical asset shape served to clients. It
// deliberately does NOT carry LocalPath or any other server-internal
// locator — those fields are runtime-only and never serialised.
//
// QDRANT-004 PR3 (June 2026): LifecycleState is added so the
// post-query hydration guard can drop rows whose state is not in
// the canonical allowlist. It is json:"-" because clients have no
// business knowing internal lifecycle semantics — if a row reaches
// SearchHit, it is by definition searchable.
type MediaAsset struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Source         string   `json:"source"`
	MediaType      string   `json:"media_type"`
	Category       string   `json:"category"`
	Tags           []string `json:"tags,omitempty"`
	Language       string   `json:"language,omitempty"`
	DurationMs     int      `json:"duration_ms,omitempty"`
	Width          int      `json:"width,omitempty"`
	Height         int      `json:"height,omitempty"`
	SearchText     string   `json:"search_text,omitempty"`
	LifecycleState string   `json:"-"`
}
