package drive

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── P1.1 tests (July 2026) — ConflictPolicy plumbing ──────────────────
//
// P1.1 replaces the legacy zero = ConflictOverwrite silent fallback:
// the publisher now consults DestinationRegistry's per-destination
// ConflictPolicy default when req.ConflictPolicy == 0 (the "caller
// didn't pick" signal). Explicit req.ConflictPolicy values
// (Skip / Overwrite / Rename) are honoured verbatim.

// TestPublisher_Publish_RegistryDrivesZero_P1_1 pins the P1.1 contract:
// when req.ConflictPolicy == 0, the publisher reads the registry's
// per-destination default and applies it. YouTubeClip is ConflictSkip
// in the canonical registry (immutable uploaded clip) so a caller
// that forgot to pick a policy MUST NOT silently overwrite an
// existing matching Drive file — the pre-P1.1 silent-ConflictOverwrite
// footgun is gone.
//
// This replaces the legacy TestPublisher_PublishForwardsConflictPolicy_ZeroValue
// test, which had pinned the dangerous behaviour (zero → Overwrite).
func TestPublisher_Publish_RegistryDrivesZero_P1_1(t *testing.T) {
	reg := testRegistry()
	// YouTubeClip is registered with ConflictSkip in the canonical
	// registry builder. Verify the registry-side contract first so a
	// future drift in the constructor is caught before the publisher
	// assertion below.
	policy, err := reg.Resolve(delivery.DestinationYouTubeClip)
	require.NoError(t, err)
	require.Equal(t, delivery.ConflictSkip, policy.ConflictPolicy,
		"YouTubeClip registry default MUST be ConflictSkip (immutable uploaded clip) — P1.1 invariant")

	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
		// ConflictPolicy omitted (zero) — the publisher MUST
		// consult the registry and forward ConflictSkip, NOT the
		// pre-P1.1 ConflictOverwrite fallback.
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictSkip, files.uploadCalls[0].policy,
		"zero-value req.ConflictPolicy MUST resolve to the registry's per-destination default (ConflictSkip for YouTubeClip) — pre-P1.1 ConflictOverwrite fallback is gone")
}

// TestPublisher_Publish_ExplicitOverridesRegistry_P1_1 pins the
// inverse direction: explicit req.ConflictPolicy wins over the
// registry default. A caller that wants ConflictOverwrite on a
// normally-Skip destination (e.g. an explicit admin reupload on a
// YouTubeClip) MUST be able to opt out without touching the registry.
//
// This keeps the door open for the historical "admin / migration /
// debug override" use case without re-introducing the silent-zero
// footgun.
func TestPublisher_Publish_ExplicitOverridesRegistry_P1_1(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip, // registry default = ConflictSkip
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
		// Explicit ConflictOverwrite — must NOT be overridden by
		// the registry's ConflictSkip default.
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictOverwrite, files.uploadCalls[0].policy,
		"explicit req.ConflictPolicy MUST win over the registry default (P1.1 — explicit override surface)")
}

// TestRegistry_ConflictPolicyPerDestination_P1_1 pins the per-key
// ConflictPolicy mapping on the registry side so a future drift in
// the constructor (e.g. accidentally setting Image to Overwrite)
// surfaces as a failing test, not a silent overwrite in production.
//
// Per-destination mapping rationale:
//
//	YouTube / Artlist / Stock / Image / Voiceover / SoundEffect
//	  → ConflictSkip (immutable / versioned assets)
//	Book / Script / Document
//	  → ConflictOverwrite (regenerable, latest-version-wins outputs)
func TestRegistry_ConflictPolicyPerDestination_P1_1(t *testing.T) {
	reg := testRegistry()

	skipDestinations := []delivery.DestinationKey{
		delivery.DestinationYouTubeClip,
		delivery.DestinationArtlist,
		delivery.DestinationStock,
		delivery.DestinationVoiceover,
		delivery.DestinationSoundEffect,
	}
	for _, dest := range skipDestinations {
		dest := dest
		t.Run(string(dest)+"/skip", func(t *testing.T) {
			policy, err := reg.Resolve(dest)
			require.NoError(t, err, "missing policy for %q", dest)
			require.Equal(t, delivery.ConflictSkip, policy.ConflictPolicy,
				"%q is an immutable / versioned asset — registry default MUST be ConflictSkip (P1.1 invariant)", dest)
		})
	}

	t.Run(string(delivery.DestinationImage)+"/skip-by-hash", func(t *testing.T) {
		policy, err := reg.Resolve(delivery.DestinationImage)
		require.NoError(t, err, "missing policy for %q", delivery.DestinationImage)
		require.Equal(t, delivery.ConflictSkipByHash, policy.ConflictPolicy,
			"%q must default to ConflictSkipByHash (content-hash dedupe)", delivery.DestinationImage)
	})

	overwriteDestinations := []delivery.DestinationKey{
		delivery.DestinationBook,
		delivery.DestinationScript,
		delivery.DestinationDocument,
	}
	for _, dest := range overwriteDestinations {
		dest := dest
		t.Run(string(dest)+"/overwrite", func(t *testing.T) {
			policy, err := reg.Resolve(dest)
			require.NoError(t, err, "missing policy for %q", dest)
			require.Equal(t, delivery.ConflictOverwrite, policy.ConflictPolicy,
				"%q is a regenerable output — registry default MUST be ConflictOverwrite (P1.1 invariant)", dest)
		})
	}
}

// TestPublisher_Publish_RegenerableDestZeroIsOverwrite_P1_1 pins the
// cross-check: zero req.ConflictPolicy against a ConflictOverwrite
// destination (e.g. Book) resolves to ConflictOverwrite, NOT to zero
// or Unknown. This guards against a future drift where the registry
// lookup returns an empty ConflictPolicy by accident — the publisher
// MUST forward the registry value verbatim, and the registry value
// for regenerable destinations MUST be ConflictOverwrite.
func TestPublisher_Publish_RegenerableDestZeroIsOverwrite_P1_1(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "book-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationBook, // regenerable → ConflictOverwrite
		LocalPath:   "/tmp/summary.pdf",
		Filename:    "summary.pdf",
		ProjectID:   "my-book-project",
		// ConflictPolicy omitted — publisher MUST resolve from
		// registry and forward ConflictOverwrite.
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictOverwrite, files.uploadCalls[0].policy,
		"Book zero-passthrough MUST resolve to ConflictOverwrite via registry (P1.1 cross-check)")
}

// TestPublisher_Publish_UnknownDestinationZeroStaysTypedError_P1_1
// pins the error surface: when req.ConflictPolicy == 0 AND the
// destination is unknown, the publisher MUST surface the typed
// "unknown destination key" error from registry.Resolve — NOT a
// silent fall-through to ConflictOverwrite. The lookup happens BEFORE
// resolveDestination so an unknown-destination error cannot be masked
// by the registry-driven default flow.
func TestPublisher_Publish_UnknownDestinationZeroStaysTypedError_P1_1(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: "nonexistent_destination_key",
		LocalPath:   "/tmp/file",
		Filename:    "file",
		// ConflictPolicy omitted.
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown destination key",
		"P1.1 zero-passthrough MUST surface 'unknown destination' verbatim — NOT silently overwrite")
}

func TestPublisher_PublishForwardsConflictPolicy_Overwrite(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{putAction: PutActionUpdated}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/video.mp4",
		Filename:       "video.mp4",
		Group:          "test",
		Subject:        "abc",
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictOverwrite, files.uploadCalls[0].policy)
}

func TestPublisher_PublishForwardsConflictPolicy_Skip(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{putAction: PutActionSkipped}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/video.mp4",
		Filename:       "video.mp4",
		Group:          "test",
		Subject:        "abc",
		ConflictPolicy: delivery.ConflictSkip,
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictSkip, files.uploadCalls[0].policy)
}

func TestPublisher_PublishForwardsConflictPolicy_Rename(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{putAction: PutActionRenamed}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/video.mp4",
		Filename:       "video.mp4",
		Group:          "test",
		Subject:        "abc",
		ConflictPolicy: delivery.ConflictRename,
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictRename, files.uploadCalls[0].policy)
}
