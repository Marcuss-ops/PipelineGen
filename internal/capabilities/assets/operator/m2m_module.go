package operator

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apimw "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

// M2MModule is the M2M (machine-to-machine) media-read surface — a tiny
// route module that exposes ONLY the read routes a remote submitter
// (PipelineGen / Agent / second PC) needs to SEE the elements the Master
// has in its databases, so it can build a job payload from real rows
// instead of reading the media SSOT out of band (which is what
// ops/jobs/remote/inventory.sh does today, with direct `docker exec psql`
// access a remote computer does not have):
//
//	GET  /api/v1/media/assets      → List  (scope: media.read)
//	GET  /api/v1/media/assets/:id  → Get   (scope: media.read)
//	GET  /api/v1/media/assets/:id/content → Content (scope: media.read; CR-19)
//	GET  /api/v1/media/assets/:id/download → unified download (scope: media.read)
//	GET  /api/v1/media/facets      → Facets(scope: media.read)
//	POST /api/v1/media/search      → semantic/hybrid search (scope: media.read)
//	GET  /api/v1/media/clips/:source/{folders,tree,breadcrumb,...}
//	                               → folder read projection (scope: media.read)
//	POST /api/v1/media/clips/:source/clips/:id/download
//	                               → DownloadClip (scope: media.read)
//
// Every route is a read: the two POSTs stream bytes or run a search query and
// never mutate the catalog. The optional routes are mounted only when the
// composition root supplies the corresponding handler, so a deployment
// without that surface stays smaller rather than 404-ing lazily.
//
// It is deliberately distinct from the admin operator console
// (module.go / handler_assets.go), which registers the SAME three read
// routes under the admin-gated /api/assets/operator prefix PLUS the
// mutating routes (reindex, bulk, operations). Those
// administrative and mutation surfaces stay admin-only; the M2M submitter
// must NOT reach them (the remote computer should be able to read the
// catalog, not enqueue mutations or drive the console).
//
// The module is constructed from the canonical AssetInventoryReader (the
// PostgreSQL media SSOT read model) but it is registered on a SEPARATE
// RouterGroup that carries JobClientAuthMiddleware + the per-route
// RequireScope. The composition root mounts the group at Setup time; this
// module never imports gin's engine directly.
//
// The reader is the SAME projection the console renders (lifecycle_state,
// derived asset_state, index_state, content hash, outbox pending events,
// Drive/local presence) — so the remote submitter sees exactly the rows
// the Master considers authoritative, including the control-plane state
// that used to require a psql session.
type M2MModule struct {
	handler  *Handler
	enabled  func() bool
	download gin.HandlerFunc
	// content streams an asset's committed bytes (object-store delivery,
	// CR-19). Optional: a deployment without the proxy stays GET-only.
	content gin.HandlerFunc
	// assetDownload is the unified any-media-type download (local -> object
	// storage -> drive). Optional: nil keeps the surface without it.
	assetDownload gin.HandlerFunc
	// search is the canonical POST /api/media/search transport (hybrid/ANN
	// Aggregator). Optional: nil keeps the surface without it.
	search gin.HandlerFunc
	// folders is the read-only clip folder/tree surface. Optional: nil keeps
	// the surface without it (media-tree-disabled deployments).
	folders FolderReadSurface
}

// FolderReadSurface is the narrow folder-read contract the M2M media surface
// mounts. It mirrors the catalog clip routes without importing the clips
// capability: the concrete *clips.OpsHandler is supplied by the composition
// root, and a nil surface simply does not mount the routes.
type FolderReadSurface interface {
	ListFolders(c *gin.Context)
	FolderStatus(c *gin.Context)
	GetFolderChildren(c *gin.Context)
	GetTree(c *gin.Context)
	GetBreadcrumb(c *gin.Context)
}

// ScopeMediaRead gates the M2M media-read surface. The admin key-creation
// endpoint (POST /api/v1/admin/m2m/keys) grants scope strings verbatim;
// the string MUST match this const for the grant to be useful on this
// surface. Renaming it silently revokes every previously-issued key that
// used the old string — do not rename without an operator-visible
// migration.
const ScopeMediaRead = "media.read"

// NewM2MModule constructs the M2M media-read surface from the canonical
// media SSOT read model. readModel is the PostgreSQL-backed
// AssetInventoryReader; a nil reader keeps the handlers' 503 fail-closed
// path (godlike/07: never serve a divergent catalog) rather than
// panicking at construction. enabled is the closure that decides whether
// the routes exist at all — the real auth gate stays inside
// JobClientAuthMiddleware (EnableM2M).
func NewM2MModule(readModel AssetInventoryReader, log *zap.Logger, enabled func() bool) *M2MModule {
	return &M2MModule{
		handler: NewHandler(Dependencies{ReadModel: readModel}, log),
		enabled: enabled,
	}
}

// SetDownloadHandler mounts the canonical clip download handler on the M2M
// surface. Optional: when it is not set (tests, media-disabled deployments,
// or a composition without the clips capability) the module stays GET-only.
func (m *M2MModule) SetDownloadHandler(h gin.HandlerFunc) {
	if m != nil {
		m.download = h
	}
}

// SetContentHandler mounts the authenticated asset-content route used by
// object-store delivery (GET /assets/:id/content). It streams with Range and
// never redirects to the internal store URL.
func (m *M2MModule) SetContentHandler(h gin.HandlerFunc) {
	if m != nil {
		m.content = h
	}
}

// SetAssetDownloadHandler mounts the unified download route
// (GET /assets/:id/download). It serves every ACTIVE asset type, unlike the
// clip download route which is keyed by source and limited to media.
func (m *M2MModule) SetAssetDownloadHandler(h gin.HandlerFunc) {
	if m != nil {
		m.assetDownload = h
	}
}

// SetSearchHandler mounts POST /search on the M2M media surface. It is the
// SAME transport the admin /api/media/search route uses, so the wire shape
// and the Aggregator (hybrid / ANN / transcript-backed search_text) cannot
// drift between the two principals.
func (m *M2MModule) SetSearchHandler(h gin.HandlerFunc) {
	if m != nil {
		m.search = h
	}
}

// SetFolderReadHandler mounts the read-only folder/tree routes under the M2M
// media prefix (/api/v1/media/clips/:source/...).
func (m *M2MModule) SetFolderReadHandler(h FolderReadSurface) {
	if m != nil {
		m.folders = h
	}
}

// Name returns the module name. Distinct from "operator" (the admin
// console module) so the WireRegistry / route-inventory surface reports
// the M2M media surface as a separate capability mount point.
func (m *M2MModule) Name() string { return "media-m2m" }

// Enabled forwards to the construction-time closure.
func (m *M2MModule) Enabled() bool {
	if m == nil || m.enabled == nil {
		return false
	}
	return m.enabled()
}

// RegisterRoutes mounts the read routes on the supplied group. The group is
// expected to already carry JobClientAuthMiddleware (the composition root
// mounts it on the group before calling this). The per-route RequireScope is
// applied HERE (not on the group) so the scope gate sits immediately before
// the handler, matching the M2M job surface:
//
//	mediaAPI.GET("/assets",     apimw.RequireScope(ScopeMediaRead), m.handler.handleListAssets)
//	mediaAPI.GET("/assets/:id", apimw.RequireScope(ScopeMediaRead), m.handler.handleGetAsset)
//	mediaAPI.GET("/facets",     apimw.RequireScope(ScopeMediaRead), m.handler.handleFacets)
//	…optional handlers get the same RequireScope, plus M2MDownloadConcurrency
//	for the two byte-streaming download routes.
//
// The prefix is intentionally empty ("" or "/assets") — the composition
// root mounts this module on a group already rooted at /api/v1/media, so
// the routes resolve to GET /api/v1/media/assets etc.
//
// The handlers are the SAME methods the admin console uses, so the wire
// shape (filters, pagination, the AssetInspection detail view) stays
// single-implementation — no second projection can drift.
func (m *M2MModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m == nil || m.handler == nil {
		return
	}
	rg.GET("/assets", apimw.RequireScope(ScopeMediaRead), m.handler.handleListAssets)
	rg.GET("/assets/:id", apimw.RequireScope(ScopeMediaRead), m.handler.handleGetAsset)
	rg.GET("/facets", apimw.RequireScope(ScopeMediaRead), m.handler.handleFacets)
	if m.search != nil {
		rg.POST("/search", apimw.RequireScope(ScopeMediaRead), m.search)
	}
	if m.content != nil {
		rg.GET("/assets/:id/content", apimw.RequireScope(ScopeMediaRead), apimw.M2MDownloadConcurrency(2), m.content)
	}
	if m.assetDownload != nil {
		rg.GET("/assets/:id/download", apimw.RequireScope(ScopeMediaRead), apimw.M2MDownloadConcurrency(2), m.assetDownload)
	}
	if m.folders != nil {
		// Folder/tree projection. Read-only and scope-gated exactly like the
		// asset routes; the admin console mounts the same handlers.
		rg.GET("/clips/:source/folders", apimw.RequireScope(ScopeMediaRead), m.folders.ListFolders)
		rg.GET("/clips/:source/folders/:id", apimw.RequireScope(ScopeMediaRead), m.folders.FolderStatus)
		rg.GET("/clips/:source/folders/:id/children", apimw.RequireScope(ScopeMediaRead), m.folders.GetFolderChildren)
		rg.GET("/clips/:source/tree", apimw.RequireScope(ScopeMediaRead), m.folders.GetTree)
		rg.GET("/clips/:source/breadcrumb", apimw.RequireScope(ScopeMediaRead), m.folders.GetBreadcrumb)
	}
	if m.download != nil {
		rg.POST(
			"/clips/:source/clips/:id/download",
			apimw.RequireScope(ScopeMediaRead),
			apimw.M2MDownloadConcurrency(2),
			m.download,
		)
	}
}

// Compile-time assertion: M2MModule satisfies the minimal route-module
// interface the composition root expects (the same interface
// jobs.M2MJobsModule satisfies).
var _ interface {
	Name() string
	Enabled() bool
	RegisterRoutes(*gin.RouterGroup)
} = (*M2MModule)(nil)
