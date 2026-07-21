// Package mediamemory — types_test.go pins the canonical closed
// sets exposed via IsKnownXxx predicates in types.go.
//
// godlike/06 SSOT (one canonical owner per fact): every enum has
// exactly one IsKnownXxx validator defined here. Tests pin them
// so future drift (e.g. renaming ConceptEmotion to ConceptFeeling)
// surfaces as a CI failure on the closed-set membership check
// before it breaks the ranker's switch statement.
//
// godlike/07 NO-FAKE-AVAILABILITY: typed-sentinel errors are
// tested for non-empty content (parallel to clipresolve.ErrXxx
// pattern). A silent zero-string sentinel would let callers
// branch incorrectly via errors.Is.
package mediamemory

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ── SlotKind ────────────────────────────────────────────────────

func TestIsKnownSlotKindAcceptsCanonical(t *testing.T) {
	for _, k := range []SlotKind{
		SlotPrimaryVideo, SlotSecondaryImage, SlotEvidenceOverlay,
		SlotMap, SlotPortrait, SlotDocument, SlotBackground,
	} {
		assert.Truef(t, IsKnownSlotKind(k),
			"canonical SlotKind %q MUST be accepted", k)
	}
}

func TestIsKnownSlotKindRejectsDrift(t *testing.T) {
	for _, k := range []SlotKind{
		"", "primaryvideo", "PRIMARY_VIDEO", "primary_video_typo",
		"third_layer", "video_primary",
	} {
		assert.Falsef(t, IsKnownSlotKind(k),
			"uncanonical SlotKind %q MUST be rejected", k)
	}
}

// ── ConceptType ───────────────────────────────────────────────

func TestIsKnownConceptTypeAcceptsCanonical(t *testing.T) {
	for _, c := range []ConceptType{
		ConceptPhrase, ConceptEntity, ConceptPerson, ConceptLocation,
		ConceptEvent, ConceptAction, ConceptObject, ConceptTopic, ConceptEmotion,
	} {
		assert.Truef(t, IsKnownConceptType(c),
			"canonical ConceptType %q MUST be accepted", c)
	}
}

func TestIsKnownConceptTypeRejectsDrift(t *testing.T) {
	for _, c := range []ConceptType{
		"", "PHRASE", "phrases", "EMOJI", "concept",
	} {
		assert.Falsef(t, IsKnownConceptType(c),
			"uncanonical ConceptType %q MUST be rejected", c)
	}
}

// ── ApprovalStatus ────────────────────────────────────────────

func TestIsKnownApprovalStatusAcceptsCanonical(t *testing.T) {
	for _, s := range []ApprovalStatus{
		ApprovalPending, ApprovalApproved, ApprovalRejected,
	} {
		assert.Truef(t, IsKnownApprovalStatus(s),
			"canonical ApprovalStatus %q MUST be accepted", s)
	}
}

func TestIsKnownApprovalStatusRejectsDrift(t *testing.T) {
	for _, s := range []ApprovalStatus{
		"", "APPROVED", "pendng", "review",
	} {
		assert.Falsef(t, IsKnownApprovalStatus(s),
			"uncanonical ApprovalStatus %q MUST be rejected", s)
	}
}

// ── Origin ─────────────────────────────────────────────────────

func TestIsKnownOriginAcceptsCanonical(t *testing.T) {
	for _, o := range []Origin{
		OriginManual, OriginAutoLink, OriginPhraseEq, OriginSemantic,
	} {
		assert.Truef(t, IsKnownOrigin(o),
			"canonical Origin %q MUST be accepted", o)
	}
}

func TestIsKnownOriginRejectsDrift(t *testing.T) {
	for _, o := range []Origin{
		"", "MANUAL", "maual", "discovery",
	} {
		assert.Falsef(t, IsKnownOrigin(o),
			"uncanonical Origin %q MUST be rejected", o)
	}
}

// ── DiscoveryStatus ───────────────────────────────────────────

func TestIsKnownDiscoveryStatusAcceptsCanonical(t *testing.T) {
	for _, s := range []DiscoveryStatus{
		DiscoveryQueued, DiscoverySearched, DiscoveryAnalyzed,
		DiscoveryIndexed, DiscoveryFailed, DiscoveryMaterialized,
	} {
		assert.Truef(t, IsKnownDiscoveryStatus(s),
			"canonical DiscoveryStatus %q MUST be accepted", s)
	}
}

func TestIsKnownDiscoveryStatusRejectsDrift(t *testing.T) {
	for _, s := range []DiscoveryStatus{
		"", "QUEUED", "queue", "discoveried",
	} {
		assert.Falsef(t, IsKnownDiscoveryStatus(s),
			"uncanonical DiscoveryStatus %q MUST be rejected", s)
	}
}

// ── MaterializationStatus (hot/warm/cold SSOT tier) ────────────

func TestIsKnownMaterializationStatusAcceptsCanonical(t *testing.T) {
	for _, s := range []MaterializationStatus{
		MaterializationCold, MaterializationWarm, MaterializationHot,
		MaterializationFailed,
	} {
		assert.Truef(t, IsKnownMaterializationStatus(s),
			"canonical MaterializationStatus %q MUST be accepted", s)
	}
}

func TestIsKnownMaterializationStatusRejectsDrift(t *testing.T) {
	for _, s := range []MaterializationStatus{
		"", "COLD", "warming", "archive",
	} {
		assert.Falsef(t, IsKnownMaterializationStatus(s),
			"uncanonical MaterializationStatus %q MUST be rejected", s)
	}
}

// ── BatchState ────────────────────────────────────────────────

func TestIsKnownBatchStateAcceptsCanonical(t *testing.T) {
	for _, s := range []BatchState{
		BatchPending, BatchReconciling, BatchCompleted, BatchFailed,
	} {
		assert.Truef(t, IsKnownBatchState(s),
			"canonical BatchState %q MUST be accepted", s)
	}
}

func TestIsKnownBatchStateRejectsDrift(t *testing.T) {
	for _, s := range []BatchState{
		"", "PENDING", "complete", "abort",
	} {
		assert.Falsef(t, IsKnownBatchState(s),
			"uncanonical BatchState %q MUST be rejected", s)
	}
}

// ── RightsStatus ──────────────────────────────────────────────

func TestIsKnownRightsStatusAcceptsCanonical(t *testing.T) {
	for _, r := range []RightsStatus{
		RightsVerified, RightsUnknown, RightsDenied, RightsExpired,
	} {
		assert.Truef(t, IsKnownRightsStatus(r),
			"canonical RightsStatus %q MUST be accepted", r)
	}
}

func TestIsKnownRightsStatusRejectsDrift(t *testing.T) {
	for _, r := range []RightsStatus{
		"", "VERIFIED", "approve", "expire",
	} {
		assert.Falsef(t, IsKnownRightsStatus(r),
			"uncanonical RightsStatus %q MUST be rejected", r)
	}
}

// ── FeedbackAction ────────────────────────────────────────────

func TestIsKnownFeedbackActionAcceptsCanonical(t *testing.T) {
	for _, a := range []FeedbackAction{
		FeedbackAccepted, FeedbackRejected, FeedbackReplaced,
		FeedbackTrimmed, FeedbackUsedSuccessful,
	} {
		assert.Truef(t, IsKnownFeedbackAction(a),
			"canonical FeedbackAction %q MUST be accepted", a)
	}
}

func TestIsKnownFeedbackActionRejectsDrift(t *testing.T) {
	for _, a := range []FeedbackAction{
		"", "ACCEPTED", "ignore", "queue", "BURN",
	} {
		assert.Falsef(t, IsKnownFeedbackAction(a),
			"uncanonical FeedbackAction %q MUST be rejected", a)
	}
}

// ── Typed-sentinel envelope sanity ────────────────────────────

// TestErrorSentinelsAreDistinctAndNonEmpty guards against
// accidental sentinel collisions or empty-string sentinels
// (which would let errors.Is always return true).
func TestErrorSentinelsAreDistinctAndNonEmpty(t *testing.T) {
	sentinels := map[string]error{
		"ErrInvalidPhrase":                  ErrInvalidPhrase,
		"ErrConceptNotFound":                ErrConceptNotFound,
		"ErrBindingNotFound":                ErrBindingNotFound,
		"ErrDuplicateBinding":               ErrDuplicateBinding,
		"ErrInvalidSlotKind":                ErrInvalidSlotKind,
		"ErrApprovalRequired":               ErrApprovalRequired,
		"ErrCandidateMaterializationFailed": ErrCandidateMaterializationFailed,
		"ErrBatchNotFound":                  ErrBatchNotFound,
		"ErrBatchNotReconcilable":           ErrBatchNotReconcilable,
		"ErrInvalidFeedbackAction":          ErrInvalidFeedbackAction,
		"ErrCandidateNotFound":              ErrCandidateNotFound,
	}
	seen := make(map[string]string, len(sentinels))
	for name, e := range sentinels {
		// Non-empty messages
		assert.NotEmptyf(t, e.Error(),
			"sentinel %s must carry a non-empty canonical message", name)
		// No two sentinels share a message (would let errors.Is mis-route).
		if other, exists := seen[e.Error()]; exists {
			t.Errorf("sentinel collision: %s and %s share message %q", other, name, e.Error())
		}
		seen[e.Error()] = name
	}
}

// TestErrCandidateNotFoundIsTypedSentinel: a wrapped miss returns
// true via errors.Is. Pinned per Phase 1.2 review.
func TestErrCandidateNotFoundIsTypedSentinel(t *testing.T) {
	wrapped := wrap("test", ErrCandidateNotFound)
	assert.True(t, errors.Is(wrapped, ErrCandidateNotFound),
		"errors.Is must resolve ErrCandidateNotFound for wrapped misses")
}

// wrap is a tiny helper local to this test file (the production
// package's helpers.go has a more elaborate pattern; this local
// helper is enough to verify the typed-sentinel envelope).
func wrap(reason string, base error) error {
	return errors.Join(base, errors.New(reason))
}

// godlike/06 SSOT: every enum constant pinned below is referenced
// at least once in tests. Removing an enum with no compile-time
// "use" can silently drop it — these references are the
// compile-time pin that catches future drift.
var _ SlotKind = SlotPrimaryVideo // primary_video slot
var _ ConceptType = ConceptPhrase // phrase-typed concept
var _ ApprovalStatus = ApprovalApproved
var _ Origin = OriginManual
var _ DiscoveryStatus = DiscoverySearched
var _ MaterializationStatus = MaterializationHot // canonical hot-tier pin
var _ BatchState = BatchCompleted
var _ RightsStatus = RightsVerified
var _ FeedbackAction = FeedbackAccepted
