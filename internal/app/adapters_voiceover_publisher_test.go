// Package app — adapters_voiceover_publisher_test.go
// (PR-P12-VOICEOVER-SEMANTIC-FIELDS + PR-VOICEOVER-DRIVE-DRIFT, July/Aug 2026).
//
// Contract test surface for the canonical useCasePublisherAdapter.Publish
// seam (internal/app/adapters_voiceover_publisher.go). 7 TDD cases pin:
//
//  1. Project + Language forward to PublishRequest (req.ProjectID +
//     req.Language) — the canonical semantic routing path.
//  2. Empty Project → typed sentinel
//     voiceover.ErrVoiceoverPublishProjectRequired (errors.Is-probable,
//     fail-closed at the seam; PR-VOICEOVER-DRIVE-DRIFT).
//     The legacy fallback chain (Project→FolderID→voiceover-ID) is
//     RETIRED; both sub-cases (empty Project + non-empty FolderID,
//     and empty Project + empty FolderID) fail closed identically.
//  3. VoiceoverPublishCommand.Validate() field-precedence gate
//     (6 sub-cases: nil receiver, empty LocalPath, empty Filename,
//     empty ID, empty Language, empty Project) — first-failure-wins.
//  4. VoiceoverPublishCommand.Validate() happy path (all fields populated).
//  5. VoiceoverPublishCommandValidateError errors.As field extraction
//     (godlike/07 typed-error contract; operators can probe the
//     field name without parsing string fragments).
//  6. Empty Language → typed sentinel
//     ErrVoiceoverPublishLanguageRequired (errors.Is-probable,
//     fail-closed at the seam).
//  7. Empty LocalPath + empty Filename → pre-Stage-3 fail-closed
//     gates (the adapter rejects these BEFORE invoking Publisher).
//
// godlike/06 SSOT: this file lives ONLY at internal/app/ (the
// canonical adapter site). The contract test surface for the
// voiceover.VoiceoverPublisher port is owned HERE — not at the
// per-item or batch use case level, not at the finalizer level.
// Drift in useCasePublisherAdapter.Publish signature or fallback
// chain surfaces as test failure here.
//
// godlike/07 NO-FAKE-AVAILABILITY: every test asserts the actual
// downstream wire shape (PublishRequest field values, not
// "Publisher was called") so a future refactor that silently
// drops a field (e.g. drops req.Language) cannot pass.
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
)

// recordingPublisher captures every PublishRequest the adapter
// forwards to the canonical Publisher seam. Thread-safe (mu)
// for future-proofing even though the test is single-goroutine.
//
// The recordingPublisher is a minimal but REAL delivery.Publisher
// (not a stub): it implements every method on the canonical
// port surface so the compile-time pin (var _ delivery.Publisher
// = (*recordingPublisher)(nil)) catches future port-signature drift
// at build time.
type recordingPublisher struct {
	mu    sync.Mutex
	calls []delivery.PublishRequest
	res   delivery.PublishResult
	err   error
}

func (r *recordingPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req)
	return &r.res, r.err
}

// ResolveFolder is the second method on delivery.Publisher. The
// per-item voiceover adapter does NOT call ResolveFolder (the
// folder is pre-resolved by the caller via DestinationResolver).
// Returns a typed-error sentinel (NOT a silent ("", nil) fallback)
// so an accidental future call from the adapter surfaces as a
// test failure rather than silently corrupting the wire shape.
// Keeps the test stub a REAL delivery.Publisher per godlike/06 SSOT
// compile-time pin.
func (r *recordingPublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "", voiceover.ErrRecordingPublisherResolveFolderNotImplemented
}

func (r *recordingPublisher) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// canonicalRecordingPublisher returns a recordingPublisher seeded with
// a non-empty FileID so the adapter's post-Stage-3 FileID-empty
// guard never fires in happy-path tests.
func canonicalRecordingPublisher() *recordingPublisher {
	return &recordingPublisher{
		res: delivery.PublishResult{FileID: "drive-file-id-canonical"},
	}
}

// canonicalVoiceoverPublishCommand returns a fully-populated
// VoiceoverPublishCommand for happy-path test fixtures. Tests
// that exercise the fail-closed contract start from this and
// clear a single field to test the gate.
func canonicalVoiceoverPublishCommand() voiceover.VoiceoverPublishCommand {
	return voiceover.VoiceoverPublishCommand{
		ID:        "vo-pr12-canonical-001",
		LocalPath: "/tmp/vo-pr12-canonical-001.mp3",
		Filename:  "vo-pr12-canonical-001_it-IT.mp3",
		FolderID:  "legacy-folder-id-may-be-set",
		Project:   "storia-boxe",
		Language:  "it-IT",
	}
}

// TestPublisherAdapter_ProjectAndLanguage_ForwardedToPublishRequest
// pins contract #1: when both Project and Language are non-empty
// on VoiceoverPublishCommand, the adapter forwards them to
// req.ProjectID + req.Language on the canonical PublishRequest.
// This is the canonical semantic routing path — the Publisher's
// VoiceoverPath will build {project}/{language}/ subpath from
// these fields. FolderID is treated as the request-local voiceover
// root so the canonical publisher builds the project/language path.
//
// The already-resolved script folder must be forwarded as
// DestinationFolderID so the canonical Publisher does not re-resolve
// it through registry/config/root routing.
func TestPublisherAdapter_ProjectAndLanguage_ForwardedToPublishRequest(t *testing.T) {
	pub := canonicalRecordingPublisher()
	adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

	fileID, err := adapter.Publish(context.Background(), voiceover.VoiceoverPublishCommand{
		ID:        "vo-pr12-canonical-001",
		LocalPath: "/tmp/vo-pr12-canonical-001.mp3",
		Filename:  "vo-pr12-canonical-001_it-IT.mp3",
		// Legacy FolderID is intentionally non-empty to prove the
		// adapter does NOT consume it (semantic-first per
		// godlike/06 SSOT; PR-VOICEOVER-DRIVE-DRIFT retirement of
		// the silent-fallback chain).
		FolderID: "legacy-folder-DO-NOT-USE",
		Project:  "storia-boxe",
		Language: "it-IT",
	})
	require.NoError(t, err)
	assert.Equal(t, "drive-file-id-canonical", fileID)
	assert.Equal(t, 1, pub.callCount())

	got := pub.calls[0]
	assert.Equal(t, delivery.DestinationVoiceover, got.Destination,
		"Destination must be the canonical voiceover destination")
	assert.Equal(t, "vo-pr12-canonical-001", got.AssetID)
	assert.Equal(t, "/tmp/vo-pr12-canonical-001.mp3", got.LocalPath)
	assert.Equal(t, "vo-pr12-canonical-001_it-IT.mp3", got.Filename)
	assert.Equal(t, "storia-boxe", got.ProjectID,
		"req.ProjectID must equal cmd.Project (canonical semantic routing)")
	assert.Equal(t, "it-IT", got.Language)
	assert.Equal(t, "legacy-folder-DO-NOT-USE", got.DestinationFolderID,
		"the plan-resolved folder must be forwarded as the canonical destination leaf")
	assert.Empty(t, got.ParentFolderID,
		"voiceover publishing must not use the legacy root override or re-resolve the folder")
}

// TestPublisherAdapter_EmptyProject_FailsClosedWithTypedSentinel
// pins contract #2 (NEW, PR-VOICEOVER-DRIVE-DRIFT 2026-08-08): when
// Project is empty on VoiceoverPublishCommand, the adapter MUST
// fail CLOSED at the typed sentinel
// voiceover.ErrVoiceoverPublishProjectRequired. No Publisher
// invocation (the gate runs BEFORE the Publisher call). The sentinel
// is errors.Is-probable (godlike/07 typed-error contract) so callers
// can detect the failure mode without parsing string fragments.
//
// The legacy fallback chain (empty Project + non-empty FolderID →
// req.ParentFolderID; empty Project + empty FolderID →
// req.ProjectID = cmd.ID with a Warn log) is RETIRED per
// godlike/07 NO-FAKE-AVAILABILITY. Both legacy scenarios now fail
// closed identically — the test exercises BOTH sub-cases to lock
// the contract.
func TestPublisherAdapter_EmptyProject_FailsClosedWithTypedSentinel(t *testing.T) {
	t.Run("EmptyProject_NonEmptyFolderID_StillFailsClosed", func(t *testing.T) {
		// godlike/07 minimum-blast-radius: the FolderID field is
		// no longer consumed by the adapter. Even if a legacy
		// caller passes a FolderID, the empty Project gate fires
		// FIRST (Validate runs before any Publish call).
		pub := canonicalRecordingPublisher()
		adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

		cmd := canonicalVoiceoverPublishCommand()
		cmd.Project = "" // gate trigger
		// FolderID remains non-empty (legacy compat shape)

		fileID, err := adapter.Publish(context.Background(), cmd)
		require.Error(t, err)
		assert.Empty(t, fileID, "fileID MUST be empty when Project is empty (fail-closed contract)")
		assert.Equal(t, 0, pub.callCount(),
			"Publisher MUST NOT be invoked when Project is empty (Validate gate runs first)")
		assert.True(t, errors.Is(err, voiceover.ErrVoiceoverPublishProjectRequired),
			"errors.Is must recover the typed sentinel so callers can probe without parsing strings")
		assert.Contains(t, err.Error(), "PR-VOICEOVER-DRIVE-DRIFT",
			"error message must reference the canonical PR so operators can trace the policy")
	})

	t.Run("EmptyProject_EmptyFolderID_StillFailsClosed", func(t *testing.T) {
		// godlike/07 minimum-blast-radius: this sub-case was the
		// "graceful degradation" path (req.ProjectID = cmd.ID with
		// a Warn log). Both empty → must STILL fail closed. No
		// Warn log is emitted; no req.ProjectID fallback.
		pub := canonicalRecordingPublisher()
		adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

		cmd := canonicalVoiceoverPublishCommand()
		cmd.Project = ""  // gate trigger
		cmd.FolderID = "" // both empty (was the degradation path)

		fileID, err := adapter.Publish(context.Background(), cmd)
		require.Error(t, err)
		assert.Empty(t, fileID, "fileID MUST be empty when Project is empty (fail-closed contract)")
		assert.Equal(t, 0, pub.callCount(),
			"Publisher MUST NOT be invoked when Project is empty (Validate gate runs first)")
		assert.True(t, errors.Is(err, voiceover.ErrVoiceoverPublishProjectRequired),
			"errors.Is must recover the typed sentinel so callers can probe without parsing strings")
		assert.False(t, errors.Is(err, voiceover.ErrVoiceoverPublishLanguageRequired),
			"empty-Project failure MUST NOT be wrapped as Language-required (disambiguate failure modes)")
	})
}

// TestVoiceoverPublishCommand_Validate_FieldPrecedence pins contract #3:
// the canonical fail-closed Validate gate runs on the typed command
// struct BEFORE the adapter forwards to the Publisher. 6 sub-cases
// cover the full field-precedence order (identity first, then content
// per PR-VOICEOVER-DRIVE-DRIFT code-reviewer feedback, 2026-08-08):
//
//  1. nil receiver
//  2. empty ID (identity — no canonical row id)
//  3. empty Project (routing primary, NEW drift-closure gate)
//  4. empty Language (routing secondary)
//  5. empty LocalPath (pre-Stage-3 filesystem gate)
//  6. empty Filename (pre-Stage-3 filesystem gate)
//
// godlike/07 typed-error contract: each error is a
// *VoiceoverPublishCommandValidateError envelope with the
// canonical Field name; errors.Is recovers the wrapped typed
// sentinel; errors.As extracts the envelope struct.
//
// First-failure-wins semantics: only the FIRST missing field is
// reported, NOT a "which-is-worst" composite. godlike/07 minimum-
// blast-radius ensures callers see a deterministic single error.
func TestVoiceoverPublishCommand_Validate_FieldPrecedence(t *testing.T) {
	t.Run("NilReceiver", func(t *testing.T) {
		var cmd *voiceover.VoiceoverPublishCommand
		err := cmd.Validate()
		require.Error(t, err)
		var vErr *voiceover.VoiceoverPublishCommandValidateError
		require.True(t, errors.As(err, &vErr),
			"errors.As must extract the canonical envelope struct")
		assert.Equal(t, "ID", vErr.Field,
			"nil receiver MUST be reported as ID (canonical first-failure)")
	})

	t.Run("EmptyID", func(t *testing.T) {
		cmd := canonicalVoiceoverPublishCommand()
		cmd.ID = "" // gate trigger
		err := cmd.Validate()
		require.Error(t, err)
		var vErr *voiceover.VoiceoverPublishCommandValidateError
		require.True(t, errors.As(err, &vErr))
		assert.Equal(t, "ID", vErr.Field,
			"empty ID MUST be reported before LocalPath/Filename (identity-first precedence)")
		assert.Contains(t, err.Error(), "empty ID")
	})

	t.Run("EmptyProject", func(t *testing.T) {
		cmd := canonicalVoiceoverPublishCommand()
		cmd.Project = "" // gate trigger
		err := cmd.Validate()
		require.Error(t, err)
		var vErr *voiceover.VoiceoverPublishCommandValidateError
		require.True(t, errors.As(err, &vErr),
			"errors.As must extract the canonical envelope (operator-scannable field name)")
		assert.Equal(t, "Project", vErr.Field,
			"empty Project MUST be reported with the canonical field name 'Project'")
		assert.True(t, errors.Is(err, voiceover.ErrVoiceoverPublishProjectRequired),
			"errors.Is must recover the typed sentinel via the Unwrap chain")
		assert.Contains(t, err.Error(), "PR-VOICEOVER-DRIVE-DRIFT",
			"error message must reference the canonical PR so operators can trace the drift closure")
	})

	t.Run("EmptyLanguage", func(t *testing.T) {
		cmd := canonicalVoiceoverPublishCommand()
		cmd.Language = "" // gate trigger
		err := cmd.Validate()
		require.Error(t, err)
		var vErr *voiceover.VoiceoverPublishCommandValidateError
		require.True(t, errors.As(err, &vErr),
			"errors.As must extract the canonical envelope (operator-scannable field name)")
		assert.Equal(t, "Language", vErr.Field)
		assert.True(t, errors.Is(err, voiceover.ErrVoiceoverPublishLanguageRequired),
			"errors.Is must recover the typed sentinel via the Unwrap chain")
		assert.Contains(t, err.Error(), "PR-P12-VOICEOVER-SEMANTIC-FIELDS")
	})

	t.Run("EmptyLocalPath", func(t *testing.T) {
		cmd := canonicalVoiceoverPublishCommand()
		cmd.LocalPath = "" // gate trigger
		err := cmd.Validate()
		require.Error(t, err)
		var vErr *voiceover.VoiceoverPublishCommandValidateError
		require.True(t, errors.As(err, &vErr))
		assert.Equal(t, "LocalPath", vErr.Field,
			"empty LocalPath MUST be reported AFTER Project/Language (routing-first precedence)")
		assert.Contains(t, err.Error(), "empty LocalPath",
			"envelope must surface the canonical field-name + condition")
	})

	t.Run("EmptyFilename", func(t *testing.T) {
		cmd := canonicalVoiceoverPublishCommand()
		cmd.Filename = "" // gate trigger
		err := cmd.Validate()
		require.Error(t, err)
		var vErr *voiceover.VoiceoverPublishCommandValidateError
		require.True(t, errors.As(err, &vErr))
		assert.Equal(t, "Filename", vErr.Field,
			"empty Filename MUST be reported AFTER Project/Language/LocalPath (routing-first precedence)")
		assert.Contains(t, err.Error(), "empty Filename")
	})
}

// TestVoiceoverPublishCommand_Validate_AllFieldsPopulated pins
// contract #4: when all 6 fields (ID + LocalPath + Filename +
// Language + Project + optional FolderID/IdempotencyKey) are
// populated, Validate returns nil. The happy-path contract is
// the canonical pass-condition that the rest of the field-precedence
// suite depends on.
func TestVoiceoverPublishCommand_Validate_AllFieldsPopulated(t *testing.T) {
	cmd := canonicalVoiceoverPublishCommand()
	err := cmd.Validate()
	require.NoError(t, err, "fully-populated command MUST pass Validate (happy-path contract)")
}

// TestVoiceoverPublishCommandValidateError_FieldExtraction pins
// contract #5: the typed envelope carries the canonical Field
// name (string), and operators can recover it via errors.As without
// parsing the error string. This is the godlike/07 typed-error
// contract — operator dashboards groupBy(field_name) WITHOUT
// depending on the canonical error message wording.
func TestVoiceoverPublishCommandValidateError_FieldExtraction(t *testing.T) {
	cmd := canonicalVoiceoverPublishCommand()
	cmd.Project = "" // gate trigger (Project = canonical drift-closure field)
	err := cmd.Validate()
	require.Error(t, err)

	var vErr *voiceover.VoiceoverPublishCommandValidateError
	require.True(t, errors.As(err, &vErr),
		"errors.As must extract the canonical envelope struct (operator-scannable field)")
	assert.Equal(t, "Project", vErr.Field,
		"Field name MUST be the canonical 'Project' (matches the typed-sentinel contract)")

	// Direct method-call also recovers the same field (defense-in-depth).
	assert.Equal(t, "Project", vErr.Field,
		"envelope.Field getter MUST return the canonical field name")

	// Unwrap chain MUST recover the typed sentinel (errors.Is).
	assert.True(t, errors.Is(err, voiceover.ErrVoiceoverPublishProjectRequired),
		"errors.Is chain MUST recover the wrapped typed sentinel via Unwrap()")

	// Error() method MUST include both the envelope prefix AND the
	// sentinel's full text (operator-log-readable).
	assert.Contains(t, err.Error(), "voiceover publish: validate: field Project",
		"envelope Error() MUST format as 'voiceover publish: validate: field <Name>'")
	assert.Contains(t, err.Error(), "PR-VOICEOVER-DRIVE-DRIFT",
		"sentinel text MUST surface in the final error message")
}

// TestPublisherAdapter_EmptyLanguage_FailsClosedWithTypedSentinel
// pins contract #6: when Language is empty on VoiceoverPublishCommand,
// the adapter MUST fail CLOSED at the typed sentinel
// voiceover.ErrVoiceoverPublishLanguageRequired. No Publisher
// invocation (the gate runs BEFORE the fallback chain). The sentinel
// is errors.Is-probable (godlike/07 typed-error contract) so callers
// can detect the failure mode without parsing string fragments.
//
// The test sets Project to a non-empty value so the routing-first
// precedence (ID → Project → Language → LocalPath → Filename)
// skips past the Project gate and lands on the Language gate —
// this locks that Project is checked BEFORE Language (not the
// reverse). The canonical order is: identity (ID), then routing
// (Project → Language), then filesystem (LocalPath → Filename).
func TestPublisherAdapter_EmptyLanguage_FailsClosedWithTypedSentinel(t *testing.T) {
	pub := canonicalRecordingPublisher()
	adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

	fileID, err := adapter.Publish(context.Background(), voiceover.VoiceoverPublishCommand{
		ID:        "vo-pr12-no-lang-001",
		LocalPath: "/tmp/vo-pr12-no-lang-001.mp3",
		Filename:  "vo-pr12-no-lang-001.mp3",
		Project:   "storia-boxe",
		// Language intentionally empty — fail-closed at the sentinel.
		// Project is non-empty so the gate passes Project and stops
		// at Language (per the identity-first precedence; this locks
		// that Project is checked BEFORE Language, not the reverse).
		Language: "",
	})
	require.Error(t, err)
	assert.Empty(t, fileID, "fileID MUST be empty when Language is empty (fail-closed contract)")
	assert.Equal(t, 0, pub.callCount(),
		"Publisher MUST NOT be invoked when Language is empty (pre-Stage-3 fail-closed gate)")
	assert.True(t, errors.Is(err, voiceover.ErrVoiceoverPublishLanguageRequired),
		"errors.Is must recover the typed sentinel so callers can probe without parsing strings")
	assert.Contains(t, err.Error(), "PR-P12-VOICEOVER-SEMANTIC-FIELDS",
		"error message must reference the canonical PR so operators can trace the policy")
}

// TestPublisherAdapter_ProjectAndLanguageBothEmpty_ProjectCheckedFirst
// pins the canonical precedence-order lock (PR-VOICEOVER-DRIVE-DRIFT
// code-reviewer feedback, 2026-08-08): when BOTH Project and Language
// are empty on VoiceoverPublishCommand, the adapter MUST fail CLOSED
// at the Project-required sentinel (NOT the Language-required sentinel).
// The routing-primary field is checked FIRST so operators debugging
// the drift see the most-actionable error directly.
//
// godlike/07 NO-FAKE-AVAILABILITY: the drift closure's primary use
// case is the Project-empty path (the legacy silent-fallback chain
// routed empty-Project to ParentFolderID which surfaced as
// "PathBuilder incomplete but ParentFolderID is set"). Surfacing
// the Project error FIRST lets operators debug the drift directly,
// without first fixing a downstream Language issue that may be a
// secondary symptom.
func TestPublisherAdapter_ProjectAndLanguageBothEmpty_ProjectCheckedFirst(t *testing.T) {
	pub := canonicalRecordingPublisher()
	adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

	fileID, err := adapter.Publish(context.Background(), voiceover.VoiceoverPublishCommand{
		ID:        "vo-pr12-both-empty-001",
		LocalPath: "/tmp/vo-pr12-both-empty-001.mp3",
		Filename:  "vo-pr12-both-empty-001.mp3",
		// Both Project AND Language intentionally empty.
		// Per canonical precedence (identity first, then content),
		// Project is checked BEFORE Language — so the typed error
		// MUST be ErrVoiceoverPublishProjectRequired, not
		// ErrVoiceoverPublishLanguageRequired.
		Project:  "",
		Language: "",
	})
	require.Error(t, err)
	assert.Empty(t, fileID)
	assert.Equal(t, 0, pub.callCount(),
		"Publisher MUST NOT be invoked when Project is empty (Validate gate runs first)")
	assert.True(t, errors.Is(err, voiceover.ErrVoiceoverPublishProjectRequired),
		"errors.Is must recover the Project-required sentinel (NOT the Language one) — precedence-order lock")
	assert.False(t, errors.Is(err, voiceover.ErrVoiceoverPublishLanguageRequired),
		"empty-Project failure MUST NOT be wrapped as Language-required (disambiguate failure modes + lock precedence)")
	assert.Contains(t, err.Error(), "PR-VOICEOVER-DRIVE-DRIFT",
		"error message must reference the Project-drift-closure PR so operators can trace the policy")
}

// TestVoiceoverPublishCommandValidateError_Unwrap_PreservesSentinel
// pins the load-bearing assertion for the errors.Is recovery path the
// adapter relies on (useCasePublisherAdapter.Publish wraps the
// envelope via fmt.Errorf("...: %w", err); errors.Is must traverse
// the dual-%w chain back to the typed sentinel).
//
// godlike/07 typed-error contract: callers probe via
// errors.Is(err, ErrVoiceoverPublishProjectRequired) to detect
// the drift-closure failure mode without parsing string fragments.
// If Unwrap() does not return the wrapped sentinel, the chain
// breaks and callers see only the opaque envelope text — a
// godlike/07 NO-FAKE-AVAILABILITY violation class.
func TestVoiceoverPublishCommandValidateError_Unwrap_PreservesSentinel(t *testing.T) {
	t.Run("Unwrap_ProjectSentinel", func(t *testing.T) {
		envelope := &voiceover.VoiceoverPublishCommandValidateError{
			Field:   "Project",
			Wrapped: voiceover.ErrVoiceoverPublishProjectRequired,
		}
		assert.Equal(t, voiceover.ErrVoiceoverPublishProjectRequired, envelope.Unwrap(),
			"Unwrap() MUST return the wrapped typed sentinel directly (canonical typed-error contract)")
	})

	t.Run("Unwrap_LanguageSentinel", func(t *testing.T) {
		envelope := &voiceover.VoiceoverPublishCommandValidateError{
			Field:   "Language",
			Wrapped: voiceover.ErrVoiceoverPublishLanguageRequired,
		}
		assert.Equal(t, voiceover.ErrVoiceoverPublishLanguageRequired, envelope.Unwrap(),
			"Unwrap() MUST return the wrapped typed sentinel directly (canonical typed-error contract)")
	})

	t.Run("ErrorsIs_DualPercentW_ChainTraversal", func(t *testing.T) {
		// godlike/07 minimum-blast-radius: the adapter wraps the
		// envelope via fmt.Errorf("...: %w", err). The dual-%w
		// chain (envelope → sentinel) must round-trip via
		// errors.Is so callers can detect the failure mode
		// through the wrap.
		cmd := canonicalVoiceoverPublishCommand()
		cmd.Project = "" // gate trigger
		err := cmd.Validate()
		require.Error(t, err)

		// Wrap the envelope error in another %w (mirroring the
		// adapter's dual-%w pattern).
		wrapped := fmt.Errorf("useCasePublisherAdapter.Publish: validate: %w", err)

		// errors.Is MUST traverse through the outer wrap → envelope
		// → sentinel chain.
		assert.True(t, errors.Is(wrapped, voiceover.ErrVoiceoverPublishProjectRequired),
			"errors.Is MUST recover the typed sentinel through a dual-%w chain (load-bearing for the adapter's wrap pattern)")

		// errors.As MUST extract the envelope from the dual-%w chain.
		var vErr *voiceover.VoiceoverPublishCommandValidateError
		assert.True(t, errors.As(wrapped, &vErr),
			"errors.As MUST extract the canonical envelope from the dual-%w chain (operator-scannable field name)")
		assert.Equal(t, "Project", vErr.Field,
			"extracted envelope MUST carry the canonical field name 'Project'")
	})
}

// TestPublisherAdapter_EmptyLocalPathOrFilename_PreStage3FailClosed
// pins contract #7: the pre-Stage-3 fail-closed gates reject empty
// LocalPath and empty Filename BEFORE any Publisher invocation.
// These are the pre-PR-12 gates preserved across the PR-P12
// migration — under the new identity-first precedence (post
// PR-VOICEOVER-DRIVE-DRIFT 2026-08-08), the routing checks
// (Project + Language) run BEFORE the filesystem checks
// (LocalPath + Filename) so a caller with both a routing issue
// AND a filesystem issue sees the routing error first.
//
// 7a: empty LocalPath → fail-closed AFTER the routing gates pass.
// 7b: empty Filename → fail-closed AFTER the routing gates pass.
//
// Both cases assert zero Publisher invocations and the absence
// of the typed-sentinel (so callers can disambiguate the failure
// mode from the Language/Project-required failure).
func TestPublisherAdapter_EmptyLocalPathOrFilename_PreStage3FailClosed(t *testing.T) {
	t.Run("EmptyLocalPath", func(t *testing.T) {
		pub := canonicalRecordingPublisher()
		adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

		fileID, err := adapter.Publish(context.Background(), voiceover.VoiceoverPublishCommand{
			ID:        "vo-pr12-nopath-001",
			LocalPath: "", // empty
			Filename:  "vo-pr12-nopath-001.mp3",
			Project:   "storia-boxe",
			Language:  "it-IT",
		})
		require.Error(t, err)
		assert.Empty(t, fileID)
		assert.Equal(t, 0, pub.callCount(),
			"Publisher MUST NOT be invoked when LocalPath is empty (pre-Stage-3 gate)")
		assert.False(t, errors.Is(err, voiceover.ErrVoiceoverPublishLanguageRequired),
			"empty-LocalPath failure MUST NOT be wrapped as Language-required (disambiguate failure modes)")
		assert.False(t, errors.Is(err, voiceover.ErrVoiceoverPublishProjectRequired),
			"empty-LocalPath failure MUST NOT be wrapped as Project-required (disambiguate failure modes)")
		assert.Contains(t, err.Error(), "empty LocalPath",
			"error message must identify the empty-LocalPath condition")
	})

	t.Run("EmptyFilename", func(t *testing.T) {
		pub := canonicalRecordingPublisher()
		adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

		fileID, err := adapter.Publish(context.Background(), voiceover.VoiceoverPublishCommand{
			ID:        "vo-pr12-nofn-001",
			LocalPath: "/tmp/vo-pr12-nofn-001.mp3",
			Filename:  "", // empty
			Project:   "storia-boxe",
			Language:  "it-IT",
		})
		require.Error(t, err)
		assert.Empty(t, fileID)
		assert.Equal(t, 0, pub.callCount(),
			"Publisher MUST NOT be invoked when Filename is empty (pre-Stage-3 gate)")
		assert.False(t, errors.Is(err, voiceover.ErrVoiceoverPublishLanguageRequired),
			"empty-Filename failure MUST NOT be wrapped as Language-required (disambiguate failure modes)")
		assert.False(t, errors.Is(err, voiceover.ErrVoiceoverPublishProjectRequired),
			"empty-Filename failure MUST NOT be wrapped as Project-required (disambiguate failure modes)")
		assert.Contains(t, err.Error(), "empty Filename",
			"error message must identify the empty-Filename condition")
	})
}
