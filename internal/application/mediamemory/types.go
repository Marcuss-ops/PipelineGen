// Package mediamemory — types.go is the canonical SSOT for the
// MediaMemory capability wire shapes.
//
// godlike/06 SSOT (one canonical owner per fact): every visual-memory
// entity (concept, binding, candidate, plan layer) is owned by this
// file. Future capabilities that need to read or mutate visual-memory
// state import the types from here, not a parallel fork.
//
// godlike/06 SSOT (sister to search.Candidate): MediaCandidate mirrors
// the canonical search.Candidate projection: NO LocalPath, NO
// DriveLink, NO server-internal locator in the wire shape. The
// binder layer reads AssetDeliveryService to mint short-lived URLs
// at the HTTP boundary. This package only owns the binding surface
// (concept → asset_id → slot → score), not delivery URLs.
//
// godlike/07 NO-FAKE-AVAILABILITY (typed fail-closed boundary):
// the typed sentinel errors live canonically in types_sentinels.go
// (sent per-domain). Each sentinel is wrapped with %w in the service
// methods so callers probe via errors.Is, not string-match.
//
// Phase 1.1 (skeleton): only types and sentinels are declared here.
// No business logic — phase-specific implementations live in
// resolver.go / binding_service.go / ranker.go (siblings).
//
// File split (godlike/06 single canonical home per layer):
//   - types.go               : package doc + SlotKind alias  ← this file (slim SSOT anchor)
//   - types_enums.go         : 9 enums + their constants + 9 IsKnown predicates + Provider tag constants + IsKnownProvider
//   - types_entities.go      : MediaConcept + MediaBinding + MediaCandidate + BatchSpec + Batch + BatchChild + UsageEvent
//   - types_resolver.go      : VisualIntent + SceneSpec + Layer + CandidateOption + SceneIntent + SceneBackendCall + SceneResolutionTrace + SceneVisualPlan + ResolvePolicy + OptionalResolvePolicy + ResolveRequest + ResolveResult
//   - types_linker.go        : LinkerRequest + LinkerResult + EncodingChannels + MediaEmbedding + TranscriptSegment + Keyframe
//   - types_sentinels.go     : 19 sentinel errors (14 phase 1.x + 5 ErrLinker*)
package mediamemory

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// SlotKind is an alias for the canonical media.SlotKind. It is kept
// for backward compatibility until all callers are migrated to
// media.SlotKind directly.
type SlotKind = media.SlotKind
