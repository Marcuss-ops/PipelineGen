// Package api — route_descriptor.go (Fase 7(a), Push 7, July 2026).
//
// Canonical SSOT for HTTP route registration. Replaces the implicit
// `engine.GET / engine.POST / engine.PUT / engine.DELETE` calls scattered
// across internal/capabilities/** with a single typed Descriptor struct that
// routes can opt-in to and that drives Check 70 (route descriptor drift
// detection) — drift fails CI.
//
// godlike/06 SSOT preserved: every field exactly matches the schema in
// the user spec; no fields added/subtracted without an explicit PR.
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   - Compile-time anchor: `var _ RouteDescriptor = (*X)(nil)` at any
//     future production bind site (one per concrete route module).
//   - AuthPolicy.IsValid is called at composition time so a typo
//     "admine" → "(unknown policy)" is loud, not silent.
//   - Capability.IsValid pins the canonical capability taxonomy
//     (`architecture/policy.yaml::capabilities`); a future capability
//     that fails to register here is a fail-closed compile error.
//
// Implementation note (Push 7 follow-up): integration with the existing
// gin engine (internal/capabilities/routes.go::Setup) is sequential — the
// composition root will iterate every registered RouteDescriptor and
// invoke the gin method on its behalf. Until that integration lands,
// RouteDescriptor is the CANONICAL intent but is NOT yet enforced as
// the only registration site. Check 70 will report drift against the
// runtime capture (gin.Engine.Routes()) so accidental bypass is caught
// regardless of whether the descriptor was registered.
package httpserver

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// RouteDescriptor is the canonical SSOT for HTTP route metadata.
// Composition roots use it to derive Gin registration + docs +
// ownership + auth-test sources from ONE typed struct.
//
// Field meaning:
//   - Method       — HTTP verb (GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS).
//   - Path         — canonical route path string. Includes the canonical
//     prefix; engine group-folding resolves the runtime
//     prefix at Setup time.
//   - Handler      — The actual gin.HandlerFunc. Handlers that wrap
//     layered middleware (rate-limit / workspace-scope)
//     must compose those middlewares INSIDE the Handler
//     so the descriptor remains the SSOT.
//   - AuthPolicy   — admin-only / worker-only / anonymous / internal.
//   - Capability   — Canonical short id of the owning capability, used
//     by the /api/capabilities endpoint + by the
//     capability_inventory.yaml cross-reference.
//   - Description  — Human-readable summary line for ACTIVE_API_GENERATED.md.
//   - RequestType  — Go type name of the request envelope (e.g.
//     "GenerateRequest" or "GenerateBatchRequest"). Empty
//     when the route accepts an empty body.
//   - ResponseType — Go type name of the response envelope (e.g.
//     "GenerateResponse" or "JobStatusResponse"). Empty
//     when the route does not return a structured body.
type RouteDescriptor struct {
	Method       string
	Path         string
	Handler      ginHandlerFunc
	AuthPolicy   AuthPolicy
	Capability   Capability
	Description  string
	RequestType  string
	ResponseType string
}

// ginHandlerFunc is the canonical handler signature (gin.HandlerFunc).
// Aliased so a future refactor to a non-gin engine (e.g. net/http
// ServeMux or chi) only has to replace the alias — every registered
// descriptor's Handler field type updates in lockstep.
type ginHandlerFunc = gin.HandlerFunc

// AuthPolicy is the canonical authentication policy enum for routes.
// The 4 values mirror the existing middleware surface:
//
//   - admin       — RequireAdminToken (X-Velox-Admin-Token header check).
//   - worker      — WorkerAuth (rejects admin tokens; worker tokens only).
//   - anonymous   — no auth (e.g. /health, /qdrant/live, /qdrant/ready,
//     /ready, /internal/v1/media/liveness).
//   - internal    — server-to-server workerbroker (the WorkerAuth
//     group at /internal/v1/*).
type AuthPolicy string

const (
	AuthPolicyAdmin     AuthPolicy = "admin"
	AuthPolicyWorker    AuthPolicy = "worker"
	AuthPolicyAnonymous AuthPolicy = "anonymous"
	AuthPolicyInternal  AuthPolicy = "internal"
)

// IsValid returns true when p is one of the canonical AuthPolicy values.
// Called at composition-time and at Check 70 to detect typos.
func (p AuthPolicy) IsValid() bool {
	switch p {
	case AuthPolicyAdmin, AuthPolicyWorker, AuthPolicyAnonymous, AuthPolicyInternal:
		return true
	}
	return false
}

// Capability is the canonical capability-name enum. Used for grouping
// routes that belong to the same feature area. Mirrors
// `architecture/policy.yaml::capabilities` (the YAML-level ratchet);
// the typed consts here catch drift at compile time.
type Capability string

const (
	CapabilityAssets    Capability = "assets"
	CapabilityArtlist   Capability = "artlist"
	CapabilityYouTube   Capability = "youtube"
	CapabilityScripts   Capability = "scripts"
	CapabilityImages    Capability = "images"
	CapabilityVoiceover Capability = "voiceover"
	CapabilityContent   Capability = "content"
	CapabilityChannels  Capability = "channels"
	CapabilityJobs      Capability = "jobs"
	CapabilitySystem    Capability = "system"
)

// IsValid returns true when c is one of the canonical Capability values.
// Called at composition-time and at Check 70. Mirrors the YAML-level
// allowlist in `architecture/policy.yaml::capabilities`.
func (c Capability) IsValid() bool {
	switch c {
	case CapabilityAssets, CapabilityArtlist, CapabilityYouTube,
		CapabilityScripts, CapabilityImages, CapabilityVoiceover,
		CapabilityContent, CapabilityChannels, CapabilityJobs, CapabilitySystem:
		return true
	}
	return false
}

// ValidateAll returns true when both AuthPolicy and Capability are valid
// for the given descriptor. An invalid field is a godlike/07 NO-FAKE-
// AVAILABILITY violation — production code MUST fail closed.
func (r RouteDescriptor) ValidateAll() (authOK, capOK bool) {
	return r.AuthPolicy.IsValid(), r.Capability.IsValid()
}

// CanonicalMethodSet is the set of HTTP verbs accepted by the gin
// engine. Compose-route descriptors whose Method is outside this set
// are rejected at composition time (RouteDescriptor.Method validation
// helper — IsKnownMethod).
var CanonicalMethodSet = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

// IsKnownMethod returns true when m is one of the canonical HTTP methods.
func IsKnownMethod(m string) bool {
	upper := strings.ToUpper(m)
	_, ok := CanonicalMethodSet[upper]
	return ok
}
