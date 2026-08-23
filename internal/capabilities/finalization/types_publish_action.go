// types/types_publish_action.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// PublishAction describes what the publisher actually did on the remote
// storage backend (Drive, object storage, etc.).
//
// The zero value is the empty string so that a zero-valued
// PublishedArtifact is distinguishable from a real publish outcome.
// Consumers that branch on Action MUST default the empty branch to a
// conservative no-op.
//
// Consolidation note (July 2026): delivery.PublishAction in
// internal/capabilities/delivery/types.go mirrors these four
// constants. FASE 5 (Drive Publisher-only) will make that package
// alias this one — no new duplication after the cutover.
type PublishAction string

const (
	PublishCreated PublishAction = "created"
	PublishUpdated PublishAction = "updated"
	PublishSkipped PublishAction = "skipped"
	PublishRenamed PublishAction = "renamed"
)

// Valid returns true if a is one of the four canonical actions.
func (a PublishAction) Valid() bool {
	switch a {
	case PublishCreated, PublishUpdated, PublishSkipped, PublishRenamed:
		return true
	}
	return false
}
