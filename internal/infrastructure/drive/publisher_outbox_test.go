package drive

// outbox surface tests for the drive publisher.

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPublisher_AllDestinationsRegistered(t *testing.T) {
	reg := testRegistry()

	expected := []delivery.DestinationKey{
		delivery.DestinationYouTubeClip,
		delivery.DestinationArtlist,
		delivery.DestinationStock,
		delivery.DestinationImage,
		delivery.DestinationVoiceover,
		delivery.DestinationBook,
		delivery.DestinationScript,
		delivery.DestinationSoundEffect,
	}

	for _, dest := range expected {
		require.True(t, reg.Has(dest), "destination %q not registered", dest)
		policy, err := reg.Resolve(dest)
		require.NoError(t, err, "Resolve(%q) failed", dest)
		require.NotEmpty(t, policy.RootFolderID, "RootFolderID empty for %q", dest)
		require.NotNil(t, policy.PathBuilder, "PathBuilder nil for %q", dest)
		if dest == delivery.DestinationArtlist {
			require.False(t, policy.RequireSubpath, "Artlist permits root-level curated uploads")
		} else {
			require.True(t, policy.RequireSubpath, "RequireSubpath must be true for %q", dest)
		}
	}
}

func TestPublisher_PublishYouTubeClip(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "clip_abc123.mp4",
		Description: "Test clip",
		AssetID:     "abc123",
		Group:       "NBA News",
		Subject:     "video-xyz",
	})
	require.NoError(t, err)
	require.Equal(t, "fake-file-id", result.FileID)
	require.Equal(t, "video-folder-id", result.FolderID)
	require.Equal(t, delivery.DestinationYouTubeClip, result.Destination)
	require.Equal(t, []string{"NBA News", "video-xyz"}, result.PathSegments)

	// Verify the folder manager was called correctly.
	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, "clips-root", folders.ensureCalls[0].parent)
	require.Equal(t, []string{"NBA News", "video-xyz"}, folders.ensureCalls[0].segments)

	// Verify the uploader was called correctly.
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "/tmp/clip.mp4", files.uploadCalls[0].localPath)
	require.Equal(t, "video-folder-id", files.uploadCalls[0].folderID)
	require.Equal(t, "clip_abc123.mp4", files.uploadCalls[0].filename)
	require.Equal(t, "Test clip", files.uploadCalls[0].description)
}

func TestPublisher_PublishBook(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "book-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationBook,
		LocalPath:   "/tmp/book.pdf",
		Filename:    "summary.pdf",
		ProjectID:   "my-book-project",
	})
	require.NoError(t, err)
	require.Equal(t, "book-folder-id", result.FolderID)
	require.Equal(t, []string{"my-book-project"}, result.PathSegments)

	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, "books-root", folders.ensureCalls[0].parent)
}

func TestPublisher_PublishSoundEffect(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "sfx-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationSoundEffect,
		LocalPath:   "/tmp/sfx.mp3",
		Filename:    "explosion.mp3",
		Group:       "explosions",
	})
	require.NoError(t, err)
	require.Equal(t, "sfx-folder-id", result.FolderID)
	require.Equal(t, []string{"explosions"}, result.PathSegments)

	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, "sfx-root", folders.ensureCalls[0].parent)
}

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
		require.Equal(t, delivery.ConflictSkip, policy.ConflictPolicy,
			"%q must default to ConflictSkip", delivery.DestinationImage)
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

func TestPublisher_Publish_P0_1_ConflictSkip_ExistingMatch(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{
		existingFilenames: map[string]bool{"clip_abc.mp4": true},
	}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/clip.mp4",
		Filename:       "clip_abc.mp4",
		Group:          "NBA News",
		Subject:        "video-xyz",
		ConflictPolicy: delivery.ConflictSkip,
	})
	require.NoError(t, err)

	// (1) Policy was forwarded to the Uploader seam correctly.
	require.Equal(t, delivery.ConflictSkip, files.uploadCalls[0].policy,
		"Publisher must forward ConflictSkip to PutFileRequest verbatim")

	// (2) Audit pin #5: the underlying Uploader routed the existing-match
	// through ConflictSkip → PutActionSkipped (no Drive write), and
	// Publisher translated that to PublishActionSkipped at the
	// delivery/drive boundary. End-to-end pin: result.Action is the
	// "no upload happened" signal that downstream ledger / dedupe /
	// no-op branches depend on.
	//
	// The fake's routed action is captured in lastAction by
	// fakeFileUploader.PutFile; the publisher's translation is captured
	// by pub.actionFor (single source of truth across prod and table
	// tests). Asserting both — and requiring that result.Action equals
	// the translation of lastAction — proves the chain end-to-end.
	require.Equal(t, PutActionSkipped, files.uploadCalls[0].routedAction,
		"behavioral fake must route to PutActionSkipped when filename exists + policy Skip — audit pin #5 closure")
	require.Equal(t, pub.actionFor(files.uploadCalls[0].routedAction), result.Action,
		"Publish must translate the routed PutAction to delivery.PublishAction at the boundary — no-reconstruction contract")
	require.NotEqual(t, delivery.PublishActionCreated, result.Action,
		"PublishActionCreated must NOT be assumed for an explicit skip; the no-reconstruction contract requires the actual action class")
}

func TestPublisher_Publish_P0_1_ConflictRename_ExistingMatch(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{
		existingFilenames: map[string]bool{"clip_abc.mp4": true},
	}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/clip.mp4",
		Filename:       "clip_abc.mp4",
		Group:          "NBA News",
		Subject:        "video-xyz",
		ConflictPolicy: delivery.ConflictRename,
	})
	require.NoError(t, err)

	// (1) Policy was forwarded correctly.
	require.Equal(t, delivery.ConflictRename, files.uploadCalls[0].policy,
		"Publisher must forward ConflictRename to PutFileRequest verbatim")

	// (2) Audit pin #6: the underlying Uploader routed the existing-match
	// through ConflictRename → PutActionRenamed (a new file is created
	// under the new timestamped name), and Publisher translated that
	// to PublishActionRenamed at the delivery/drive boundary. The
	// "treat-as-sibling-row" branches downstream depend on this
	// exact Action class.
	require.Equal(t, PutActionRenamed, files.uploadCalls[0].routedAction,
		"behavioral fake must route to PutActionRenamed when filename exists + policy Rename — audit pin #6 closure")
	require.Equal(t, pub.actionFor(files.uploadCalls[0].routedAction), result.Action,
		"Publish must translate the routed PutAction to delivery.PublishAction at the boundary — no-reconstruction contract")
	require.NotEqual(t, delivery.PublishActionCreated, result.Action,
		"PublishActionCreated must NOT be assumed for a renamed conflict; the no-reconstruction contract requires the actual rename action class")
}
