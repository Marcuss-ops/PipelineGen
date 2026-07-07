// Package app — adapters_voiceover_publisher_test.go
// (PR-P12-VOICEOVER-SEMANTIC-FIELDS, July 2026).
//
// Contract test surface for the canonical useCasePublisherAdapter.Publish
// seam (internal/app/adapters_voiceover_publisher.go). 5 TDD cases pin:
//
//  1. Project + Language forward to PublishRequest (req.ProjectID +
//     req.Language) — the canonical semantic routing path.
//  2. Empty Project + non-empty FolderID → req.RootFolderOverride
//     (backward-compat with pre-PR-12 callers that resolved the
//     folder manually and pass a FolderID literal).
//  3. Empty Project + empty FolderID → req.ProjectID = cmd.ID
//     (graceful degradation: voiceover ID is used as the canonical
//     project slot; warning log captures the godlike/07 no-fake-
//     availability signal).
//  4. Empty Language → typed sentinel
//     ErrVoiceoverPublishLanguageRequired (errors.Is-probable,
//     fail-closed at the seam).
//  5. Empty LocalPath + empty Filename → pre-Stage-3 fail-closed
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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
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
// folder is pre-resolved by the caller via DestinationResolver),
// so this stub returns an empty folder ID and a typed-error sentinel
// if a future test ever exercises the path. Keeps the test stub
// a REAL delivery.Publisher per godlike/06 SSOT compile-time pin.
func (r *recordingPublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "", nil
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

// TestPublisherAdapter_ProjectAndLanguage_ForwardedToPublishRequest
// pins contract #1: when both Project and Language are non-empty
// on VoiceoverPublishCommand, the adapter forwards them to
// req.ProjectID + req.Language on the canonical PublishRequest.
// This is the canonical semantic routing path — the Publisher's
// VoiceoverPath will build {project}/{language}/ subpath from
// these fields. No RootFolderOverride is set; no fallback warning
// is logged.
func TestPublisherAdapter_ProjectAndLanguage_ForwardedToPublishRequest(t *testing.T) {
	pub := canonicalRecordingPublisher()
	adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

	fileID, err := adapter.Publish(context.Background(), voiceover.VoiceoverPublishCommand{
		ID:        "vo-pr12-canonical-001",
		LocalPath: "/tmp/vo-pr12-canonical-001.mp3",
		Filename:  "vo-pr12-canonical-001_it-IT.mp3",
		// Legacy FolderID is intentionally non-empty to prove the
		// adapter prefers Project over FolderID (semantic-first per
		// godlike/06 SSOT).
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
	assert.Empty(t, got.RootFolderOverride,
		"req.RootFolderOverride MUST be empty when Project is set (semantic-first precedence)")
}

// TestPublisherAdapter_EmptyProject_FolderIDFallbackToRootOverride
// pins contract #2: when Project is empty AND FolderID is non-empty
// (pre-PR-12 callers that resolved the folder manually and pass a
// FolderID literal), the adapter uses FolderID as the
// req.RootFolderOverride. The Publisher bypasses DestinationRegistry
// root-folder resolution and writes to the pre-resolved root.
func TestPublisherAdapter_EmptyProject_FolderIDFallbackToRootOverride(t *testing.T) {
	pub := canonicalRecordingPublisher()
	adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

	fileID, err := adapter.Publish(context.Background(), voiceover.VoiceoverPublishCommand{
		ID:        "vo-pr12-legacy-001",
		LocalPath: "/tmp/vo-pr12-legacy-001.mp3",
		Filename:  "vo-pr12-legacy-001.mp3",
		// Project empty (legacy caller doesn't know the semantic surface)
		// but FolderID non-empty (legacy caller resolved the folder manually).
		FolderID: "legacy-resolved-folder-id",
		Language: "en-US",
	})
	require.NoError(t, err)
	assert.Equal(t, "drive-file-id-canonical", fileID)
	assert.Equal(t, 1, pub.callCount())

	got := pub.calls[0]
	assert.Equal(t, "legacy-resolved-folder-id", got.RootFolderOverride,
		"empty Project + non-empty FolderID MUST surface as req.RootFolderOverride (legacy compat path)")
	assert.Empty(t, got.ProjectID,
		"req.ProjectID MUST be empty in the legacy compat path (no semantic routing)")
	assert.Equal(t, "en-US", got.Language)
}

// TestPublisherAdapter_EmptyProjectAndFolder_GracefulDegradationWithWarn
// pins contract #3: when BOTH Project and FolderID are empty
// (the canonical godlike/07 no-fake-availability degradation
// path), the adapter uses cmd.ID as req.ProjectID so VoiceoverPath
// can still build {project}/{language}/. A Warn-level log captures
// the degradation signal so operators can detect callers that
// haven't migrated to the semantic surface.
//
// Uses zaptest/observer to capture the warn log entry — confirms
// the canonical observability contract pinned by the action plan.
func TestPublisherAdapter_EmptyProjectAndFolder_GracefulDegradationWithWarn(t *testing.T) {
	pub := canonicalRecordingPublisher()
	core, logs := observer.New(zap.WarnLevel)
	adapter := newUseCasePublisherAdapter(pub, zap.New(core))

	fileID, err := adapter.Publish(context.Background(), voiceover.VoiceoverPublishCommand{
		ID:        "vo-pr12-degraded-001",
		LocalPath: "/tmp/vo-pr12-degraded-001.mp3",
		Filename:  "vo-pr12-degraded-001.mp3",
		// Both Project and FolderID empty — graceful degradation path.
		Language: "en-US",
	})
	require.NoError(t, err)
	assert.Equal(t, "drive-file-id-canonical", fileID)
	assert.Equal(t, 1, pub.callCount())

	got := pub.calls[0]
	assert.Equal(t, "vo-pr12-degraded-001", got.ProjectID,
		"empty Project + empty FolderID MUST use voiceover ID as ProjectID fallback (graceful degradation)")
	assert.Empty(t, got.RootFolderOverride,
		"req.RootFolderOverride MUST be empty in the degradation path")
	assert.Equal(t, "en-US", got.Language)

	// Warn-level log assertion: the adapter MUST emit exactly one
	// warn entry with the canonical godlike/07 degradation-signal
	// message so operators can detect pre-PR-12 callers.
	require.Equal(t, 1, logs.Len(),
		"graceful-degradation path MUST emit exactly one Warn log (no log = silent-success anti-pattern)")
	entry := logs.All()[0]
	assert.Equal(t, zap.WarnLevel, entry.Level)
	assert.Contains(t, entry.Message, "voiceover publisher",
		"warn message must start with the canonical adapter prefix")
	assert.Contains(t, entry.Message, "degradation signal",
		"warn message must call out the godlike/07 no-fake-availability contract")
	assert.Contains(t, entry.Message, "neither Project nor FolderID set",
		"warn message must identify the empty-input condition")
}

// TestPublisherAdapter_EmptyLanguage_FailsClosedWithTypedSentinel
// pins contract #4: when Language is empty on VoiceoverPublishCommand,
// the adapter MUST fail CLOSED at the typed sentinel
// voiceover.ErrVoiceoverPublishLanguageRequired. No Publisher
// invocation (the gate runs BEFORE the fallback chain). The sentinel
// is errors.Is-probable (godlike/07 typed-error contract) so callers
// can detect the failure mode without parsing string fragments.
func TestPublisherAdapter_EmptyLanguage_FailsClosedWithTypedSentinel(t *testing.T) {
	pub := canonicalRecordingPublisher()
	adapter := newUseCasePublisherAdapter(pub, zap.NewNop())

	fileID, err := adapter.Publish(context.Background(), voiceover.VoiceoverPublishCommand{
		ID:        "vo-pr12-no-lang-001",
		LocalPath: "/tmp/vo-pr12-no-lang-001.mp3",
		Filename:  "vo-pr12-no-lang-001.mp3",
		Project:   "storia-boxe",
		// Language intentionally empty — fail-closed at the sentinel.
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

// TestPublisherAdapter_EmptyLocalPathOrFilename_PreStage3FailClosed
// pins contract #5: the pre-Stage-3 fail-closed gates reject empty
// LocalPath and empty Filename BEFORE any Publisher invocation.
// These are the pre-PR-12 gates preserved verbatim across the
// PR-P12 migration (the new Language gate sits between them and
// the Publisher call).
//
// 5a: empty LocalPath → fail-closed before the Language gate.
// 5b: empty Filename → fail-closed before the Language gate.
//
// Both cases assert zero Publisher invocations and the absence
// of the typed-sentinel (so callers can disambiguate the failure
// mode from the Language-required failure).
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
		assert.Contains(t, err.Error(), "empty Filename",
			"error message must identify the empty-Filename condition")
	})
}
