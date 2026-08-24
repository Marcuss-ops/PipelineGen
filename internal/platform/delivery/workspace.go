// Package delivery — canonical WorkspaceContext shim (Commit 3-A, July 2026).
//
// godlike/06 SSOT (one owner per fact): the canonical tenant identity
// envelope lives at `search.Actor` in
// `internal/application/search/types.go`. This file declares a
// Go-level type alias so the delivery infrastructure consumes the
// canonical shape by its preferred name without re-declaring the
// struct.
//
// godlike/07 (zero-extra-types discipline): `type WorkspaceContext
// = search.Actor` makes the two names pointer-identical at the
// type-system level — no zero-value copy, no struct conversion,
// and `errors.Is` chains traverse transparently. The production
// concrete (*Signer, signer.go) satisfies `search.AssetDeliveryService`
// once it is migrated from `mediasearch.WorkspaceContext` to
// `delivery.WorkspaceContext` in Commit 3-B.
//
// Cross-package layering note (per Wave 19 + AGENTS.md): infrastructure
// CAN import application/search via this file (the import direction
// is infra → application, which is the canonical GateDirection);
// the reverse (application importing this file) would violate the
// rule and is intentionally NOT done.
package delivery

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// WorkspaceContext is the canonical tenant-identity envelope
// consumed by the asset-delivery signer + Verify helpers. The type
// alias preserves pointer-identity with `search.Actor` so future
// migrations from `mediasearch.WorkspaceContext` are byte-stable:
// callers that already hold a `search.Actor` value can pass it
// directly where a `delivery.WorkspaceContext` is expected, and
// the underlying struct value is shared between the two names.
type WorkspaceContext = search.Actor
