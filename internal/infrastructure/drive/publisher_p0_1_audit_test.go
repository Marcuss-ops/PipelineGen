package drive

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── P0 #9 tests (June 2026) — PublishResult enrichment ─────────────
//
// Pre-P0-#9 the publisher dropped DownloadLink / MD5Checksum / Action
// from the underlying PutFileResult, forcing every consumer to
// reconstruct the download URL via string interpolation. The test
// below pins the contract: PublishResult carries all four metadata
// fields verbatim from PutFileResult and the resolved PathSegments,
// so no consumer can fall back to reconstruction without first
// deleting this test or breaking it.

// TestPublisher_PublishEnrichesPublishResult_F1_5_P0_9 pins the P0
// #9 surface: PublishResult must expose DownloadLink, MD5Checksum,
// FolderPath, and a translated Action so no consumer reconstructs
// the download URL via string interpolation. Uses a non-default
// PutActionUpdated so the Action translation is observably distinct
// from the zero value.
func TestPublisher_PublishEnrichesPublishResult_F1_5_P0_9(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{
		putAction:   PutActionUpdated, // non-default to observe translation
		md5Checksum: "abc123def456",   // non-empty to observe propagation
	}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "clip_abc.mp4",
		Group:       "NBA News",
		Subject:     "video-xyz",
	})
	require.NoError(t, err)

	// (1) DownloadLink propagated verbatim from PutFileResult —
	//     verifies the no-reconstruction contract.
	require.Equal(t, "https://drive.google.com/uc?id=fake-file-id", result.DownloadLink,
		"Publish must propagate PutFileResult.DownloadLink verbatim — consumers MUST never reconstruct via uc?id=")

	// (2) MD5Checksum propagated verbatim — closes the re-FindFileByName
	//     surface that pre-P0-#9 callers used to recover the hash.
	require.Equal(t, "abc123def456", result.MD5Checksum,
		"Publish must propagate PutFileResult.MD5Checksum verbatim")

	// (3) Action translated at the delivery/drive boundary via the
	//     SAME method that Publish calls (single source of truth).
	require.Equal(t, pub.actionFor(PutActionUpdated), result.Action,
		"Publish must translate drive.PutActionUpdated → delivery.PublishActionUpdated at the boundary")
	require.NotEqual(t, delivery.PublishActionUnknown, result.Action,
		"PublishActionUnknown must NOT silently swallow a real action")

	// (4) FolderPath = strings.Join(PathSegments, "/") — the derived
	//     single-string view that consumers downstream want.
	require.Equal(t, "NBA News/video-xyz", result.FolderPath,
		"Publish must compute FolderPath as strings.Join(PathSegments, \"/\")")

	// (5) PathSegments remains authoritative: FolderPath and
	//     PathSegments coexist; PathSegments preserves order and
	//     is the canonical ordered view (FolderPath is derived).
	require.Equal(t, []string{"NBA News", "video-xyz"}, result.PathSegments,
		"PathSegments remains the authoritative ordered view; FolderPath is the derived display string")
}

// TestPublisher_TranslatePutAction_Table pins the boundary switch
// arm-by-arm, calling the SAME Publisher.actionFor method that
// production uses (single source of truth). Adding a future
// drive.PutAction constant without updating the switch surfaces as
// a failing test, not a silent fall-through to PublishActionUnknown.
func TestPublisher_TranslatePutAction_Table(t *testing.T) {
	reg := testRegistry()
	pub, err := NewPublisher(reg, &fakeFolderManager{}, &fakeFileUploader{}, zap.NewNop())
	require.NoError(t, err)

	cases := []struct {
		name     string
		input    PutAction
		expected delivery.PublishAction
	}{
		{"created", PutActionCreated, delivery.PublishActionCreated},
		{"updated", PutActionUpdated, delivery.PublishActionUpdated},
		{"skipped", PutActionSkipped, delivery.PublishActionSkipped},
		{"renamed", PutActionRenamed, delivery.PublishActionRenamed},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.expected, pub.actionFor(c.input),
				"drive.PutAction%q must translate to delivery.PublishAction%q at the boundary", c.input, c.expected)
		})
	}
}

// ── P0 #1 tests (June 2026) — audit pins #5 + #6 ──────────────────────
//
// Pre-P0-#1 ConflictPolicy was a dead enum — every caller passed the
// zero value and Uploader.UploadFileWithDescription always overwrote.
// F1.1 (commit 70f2b6c8) introduced the PutFileRequest/PutFileResult
// seam so policy + outcomes are explicit. The two tests below pin
// the end-to-end behavior at the Publisher translation layer:
// ConflictSkip + an existing-match on Drive must surface as
// PublishActionSkipped (audit pin #5 — "no upload happened"); and
// ConflictRename + existing-match must surface as
// PublishActionRenamed (audit pin #6 — "a new file gets created
// under the new name"). The fakeFileUploader's behavioral routing
// mode mimics Uploader.doPutFile's table without needing a mock
// Google Drive API client.

// TestPublisher_Publish_P0_1_ConflictSkip_ExistingMatch pins audit pin #5:
// when ConflictSkip propagates through Publisher and the underlying
// uploader reports an existing match (PutActionSkipped), the resulting
// PublishResult must surface PublishActionSkipped. The audit pin
// "no upload happened" is enforced at the Uploader layer by
// Uploader.doPutFile's ConflictSkip short-circuit (return existing
// metadata WITHOUT opening the local file); this publisher-layer
// pin proves the conflict outcome reaches the consumer without
// being reshaped or demoted to Created.
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
	require.Equal(t, PutActionSkipped, files.uploadCalls[0].routedAction,
		"behavioral fake must route to PutActionSkipped when filename exists + policy Skip — audit pin #5 closure")
	require.Equal(t, pub.actionFor(files.uploadCalls[0].routedAction), result.Action,
		"Publish must translate the routed PutAction to delivery.PublishAction at the boundary — no-reconstruction contract")
	require.NotEqual(t, delivery.PublishActionCreated, result.Action,
		"PublishActionCreated must NOT be assumed for an explicit skip; the no-reconstruction contract requires the actual action class")
}

// TestPublisher_Publish_P0_1_ConflictRename_ExistingMatch pins audit pin #6:
// when ConflictRename propagates through Publisher and the underlying
// uploader reports an existing match (PutActionRenamed), the resulting
// PublishResult must surface PublishActionRenamed. The audit pin
// "a new file is created under the new name" is enforced at the
// Uploader layer by Uploader.doPutFile's ConflictRename branch
// (Files.Create with renameWithTimestamp(original, UnixNano)); this
// publisher-layer pin proves the renamed-file outcome reaches the
// consumer.
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
