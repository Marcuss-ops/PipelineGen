package transport

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// WireRegistry tracks which API capabilities are mounted in the router.
//
// Surface area for the /ready "wire" field — operators can detect a 404'd
// capability (e.g. `POST /api/stock-pipeline/run` returns 404) without
// grepping server logs. WireRegistry is built once at composition root
// after the gin engine has all routes registered; the /ready handler
// reads it on every request.
//
// godlike/06 SSOT (one canonical owner per fact): the wire map is
// derived from the engine's route table (the canonical source of truth
// for "is this capability mounted?"). A future agent cannot redefine
// the wire surface in a sibling file — the engine.Routes() iteration
// is the only place where mounted prefixes are observed.
//
// godlike/07 NO-FAKE-AVAILABILITY: nil registry reports all capabilities
// as NOT_MOUNTED (fail-loud). A stale binary that loses the wire field
// (e.g. a downgrade that strips the transport package) surfaces every
// capability as NOT_MOUNTED rather than silently passing.
type WireRegistry struct {
	mounted map[string]bool
}

// knownCapabilities is the canonical list of capability names tracked
// by the wire surface. Adding a new capability is a 2-line change:
// append to this list + ensure the routes are registered with the
// matching prefix. The wire surface is intentionally narrow — we only
// track /api/* prefixes that operators may probe for drift detection.
//
// Path matching is HasPrefix-based, so "/api/stock" matches both
// /api/stock-pipeline/run and /api/stock/search-and-run. The convention
// is to use the SHORTEST stable prefix per capability so a future
// sub-route variant doesn't accidentally map to a different capability.
var knownCapabilities = []struct {
	name   string
	prefix string
}{
	{name: "stock", prefix: "/api/stock-pipeline"},
	{name: "artlist", prefix: "/api/artlist"},
	// voiceover mounts via internal/app/wire_assets.go::WireAssets which
	// wraps the Assets module under prefix "/media" (assetsRouteMod) +
	// the voiceover capability's own prefix "/voiceover"
	// (internal/api/assets/voiceover/module.go::Build), all beneath
	// routes.go's `api := engine.Group("/api")`. The resulting URL
	// is `/api/media/voiceover/*` — the wire scanner must scan this
	// prefix (NOT `/api/voiceover`, which would only match a
	// dedicated registerVoiceover-style future re-wiring).
	// godlike/06 SSOT (one canonical owner per fact): the wire
	// prefix string is owned by this list; the assets aggregate
	// prefix `/media` is owned by wire_assets.go; both lock together.
	{name: "voiceover", prefix: "/api/media/voiceover"},
	// youtube (legacy YouTube clip handler) mounts under /api/clips/*
	// (see internal/api/assets/youtube/youtube_handlers.go).
	{name: "youtube", prefix: "/api/clips"},
	{name: "register", prefix: "/api/register"},
	{name: "storage", prefix: "/api/storage"},
	{name: "mediasearch", prefix: "/internal/v1/media"},
	{name: "qdrant_health", prefix: "/qdrant/"},
	{name: "admin", prefix: "/api/drive"},
	// clips (canonical clips capability) mounts under /api/media/clips/*
	// via the assets module (see internal/app/wire_assets.go and
	// internal/api/assets/clips/module.go). This includes the new
	// POST /api/media/clips/ingest/ai-stock endpoint.
	{name: "clips", prefix: "/api/media/clips"},
	// script mounts under /api/script/* (ScriptFlow module, prefix
	// "/script" beneath routes.go's `api := engine.Group("/api")`).
	{name: "script", prefix: "/api/script"},
}

// RouteInfo is the minimal projection of a gin RouteInfo the
// WireRegistry needs to detect mounted capabilities. It is gin-free
// so the pure registry constructor is testable without a gin engine.
type RouteInfo struct {
	Method string
	Path   string
}

// NewWireRegistry builds a WireRegistry by scanning the supplied route
// paths for the known capability prefixes. A capability is MOUNTED when
// at least one route (any method) matches its prefix.
//
// Routes with the canonical HTTP methods (GET/POST/PUT/DELETE) all
// count; HEAD/OPTIONS/WS are NOT excluded (they also represent
// "the engine has this route registered"). nil routes returns a
// zero-value registry (all capabilities NOT_MOUNTED).
//
// Prefix matching enforces a `/` boundary so a route like
// /api/stockpiler does NOT misclassify as stock. The check is:
// `route.Path == cap.prefix || strings.HasPrefix(route.Path, cap.prefix+"/")`.
// This prevents false positives from sibling capabilities that share
// a leading substring (e.g. `/api/stock` vs `/api/storage`).
func NewWireRegistry(routes []RouteInfo) *WireRegistry {
	r := &WireRegistry{mounted: make(map[string]bool)}
	for _, route := range routes {
		for _, cap := range knownCapabilities {
			if matchesCapabilityPrefix(route.Path, cap.prefix) {
				r.mounted[cap.name] = true
			}
		}
	}
	return r
}

// matchesCapabilityPrefix returns true when path matches the capability
// prefix at a `/` boundary. This prevents HasPrefix-style false
// positives (e.g. /api/stockpiler being misclassified as stock when
// the stock prefix is /api/stock-pipeline).
//
// The prefix's trailing `/` is normalised away before the boundary
// check so both /qdrant/ + /qdrant/live and /api/stock-pipeline +
// /api/stock-pipeline/run are matched consistently. Without this
// normalisation, a prefix with a trailing `/` would build
// `prefix+"/"` = `/qdrant//` which never matches any real route.
//
// Extracted to keep NewWireRegistry readable and the boundary
// contract testable in isolation.
func matchesCapabilityPrefix(path, prefix string) bool {
	canonicalPrefix := strings.TrimSuffix(prefix, "/")
	return path == canonicalPrefix || strings.HasPrefix(path, canonicalPrefix+"/")
}

// NewWireRegistryFromEngine is the gin-aware adapter that builds a
// WireRegistry from a fully-constructed gin engine. The caller is
// expected to pass the engine AFTER all routes have been registered
// (i.e. after Setup() has run); otherwise the registry will report
// every capability as NOT_MOUNTED.
func NewWireRegistryFromEngine(engine *gin.Engine) *WireRegistry {
	if engine == nil {
		return NewWireRegistry(nil)
	}
	routes := engine.Routes()
	info := make([]RouteInfo, 0, len(routes))
	for _, r := range routes {
		info = append(info, RouteInfo{Method: r.Method, Path: r.Path})
	}
	return NewWireRegistry(info)
}

// All returns the wire map for /ready. Keys are capability names
// (e.g. "stock", "artlist"); values are the canonical wire literals:
//
//	"MOUNTED"      — at least one route matches the capability prefix
//	"NOT_MOUNTED"  — no route matches the capability prefix
//
// The map is nil-safe: a nil receiver returns all-NOT_MOUNTED so the
// /ready response is always well-formed. The map length is always
// len(knownCapabilities); callers can rely on the keys being present.
func (r *WireRegistry) All() map[string]string {
	out := make(map[string]string, len(knownCapabilities))
	if r == nil {
		// Nil-safe: all NOT_MOUNTED so /ready JSON shape is stable
		// even when the wire field is uninitialised.
		for _, cap := range knownCapabilities {
			out[cap.name] = WireNotMounted
		}
		return out
	}
	for _, cap := range knownCapabilities {
		if r.mounted[cap.name] {
			out[cap.name] = WireMounted
		} else {
			out[cap.name] = WireNotMounted
		}
	}
	return out
}

// IsMounted returns whether the given capability is currently mounted.
// nil-safe: a nil receiver returns false. Used by the /ready JSON
// renderer and by ad-hoc operator probes.
func (r *WireRegistry) IsMounted(capability string) bool {
	if r == nil {
		return false
	}
	return r.mounted[capability]
}

// WireMounted is the canonical wire literal for a mounted capability.
// Exported so tests in other packages can assert on the wire response
// shape without redefining the literal.
const WireMounted = "MOUNTED"

// WireNotMounted is the canonical wire literal for an absent capability.
// Exported for the same reason as WireMounted.
const WireNotMounted = "NOT_MOUNTED"
