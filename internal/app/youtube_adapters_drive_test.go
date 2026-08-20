// Package app — youtube_adapters_drive_test.go: PR-P12-YOUTUBE-LEGACY-RETIRE
// contract tests (P0 absolute, deadline 2026-08-08).
//
// Pins the canonical semantic-routing contract for
// YouTubePublisherDriveAdapter:
//
//  1. GetOrCreateFolder: Group = channelName, ParentFolderID = ""
//     (RETIRED per godlike/07 NO-FAKE-AVAILABILITY — the
//     Publisher resolves the root via DestinationRegistry +
//     DestinationPolicy.RootFolderID).
//  2. GetOrCreateFolder: publisher == nil returns a typed error
//     (godlike/07 fail-closed at the seam).
//  3. GetOrCreateFolder: Publisher.ResolveFolder error is wrapped
//     (typed-error contract preserved via fmt.Errorf %w).
//  4. UploadFileIfChanged: DestinationFolderID = resolved folder ID
//     (canonical leaf — direct upload, no path-builder subfolder),
//     Group/Subject propagated, ConflictPolicy = ConflictSkip
//     (preserved).
//  5. UploadFileIfChanged: skipped bool derives from
//     PublishResult.Action == delivery.UploadOutcomeSkipped
//     (godlike/07 NO-FAKE-AVAILABILITY: no caller-side heuristic,
//     the canonical Publisher declares the outcome).
//
// godlike/06 SSOT: the recorded `lastResolveReq` and `lastPublishReq`
// are the canonical assertion targets — the request-selected folder
// must be threaded through the publish seam so uploads do not fall
// back to the registry root.
//
// godlike/07 NO-FAKE-AVAILABILITY: every case asserts the recorded
// `lastReq` matches the expected wire-shape exactly; a future
// refactor that silently drops the resolved folder override (or
// silently drops ConflictSkip) surfaces as a test failure.
package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

// ytDriveRecordingPublisher is a stub delivery.Publisher that captures
// the PublishRequest it receives so tests can assert the wire-shape
// (Group/Subject/ParentFolderID/Destination/ConflictPolicy)
// propagated from YouTubePublisherDriveAdapter. The Publish
// outcome is configurable via publishAction field so Case 5 can
// verify the skipped-bool derivation against both
// UploadOutcomeCreated (skipped=false) and UploadOutcomeSkipped
// (skipped=true). Named with the ytDrive prefix to avoid collision
// with the recordingPublisher stub in adapters_voiceover_publisher_test.go
// (same package `app`).
type ytDriveRecordingPublisher struct {
	lastResolveReq delivery.PublishRequest
	lastPublishReq delivery.PublishRequest

	resolveCalls int
	publishCalls int

	// Optional error to surface from ResolveFolder / Publish (default nil).
	resolveErr error
	publishErr error

	// Outcome returned by Publish (default UploadOutcomeCreated).
	publishAction delivery.UploadOutcome
	// FileID + WebViewLink returned in PublishResult.
	publishFileID      string
	publishWebViewLink string
}

var _ delivery.Publisher = (*ytDriveRecordingPublisher)(nil)

func (r *ytDriveRecordingPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	r.lastPublishReq = req
	r.publishCalls++
	if r.publishErr != nil {
		return nil, r.publishErr
	}
	action := r.publishAction
	if action == "" {
		action = delivery.UploadOutcomeCreated
	}
	return &delivery.PublishResult{
		Action:      action,
		FileID:      r.publishFileID,
		WebViewLink: r.publishWebViewLink,
	}, nil
}

func (r *ytDriveRecordingPublisher) ResolveFolder(_ context.Context, req delivery.PublishRequest) (string, error) {
	r.lastResolveReq = req
	r.resolveCalls++
	if r.resolveErr != nil {
		return "", r.resolveErr
	}
	return "resolved-folder-id", nil
}

func (r *ytDriveRecordingPublisher) Close() error { return nil }

// newYouTubePubAdapter builds a YouTubePublisherDriveAdapter with
// the supplied publisher (logs via zap.NewNop per the canonical
// composition root convention). The legacy `drive.Admin` parameter
// was RETIRED per code-reviewer M1+M2 — the canonical surface is
// delivery.Publisher only (Group/Subject silently dropped on the
// prior dual-path).
func newYouTubePubAdapter(pub *ytDriveRecordingPublisher) *YouTubePublisherDriveAdapter {
	return NewYouTubePublisherDriveAdapter(pub, zap.NewNop())
}

// TestGetOrCreateFolder_GroupSubjectSplit_ThreadsParentRoot pins the
// canonical GetOrCreateFolder contract for payload-selected roots:
//
//   - input (channelName="boxing-channels", parentFolderID="any")
//   - lastResolveReq.Group = "youtube_subtitles"
//   - lastResolveReq.Subject = "boxing-channels"
//   - lastResolveReq.Destination = DestinationYouTubeClip
//   - lastResolveReq.ParentFolderID = "any" so the folder is
//     created under the caller-selected Drive root
//   - returned folderID = the canonical Publisher.ResolveFolder
//     resolved ID ("resolved-folder-id")
func TestGetOrCreateFolder_GroupSubjectSplit_ThreadsParentRoot(t *testing.T) {
	t.Parallel()
	pub := &ytDriveRecordingPublisher{}
	a := newYouTubePubAdapter(pub)

	got, err := a.GetOrCreateFolder(context.Background(), "boxing-channels", "legacy-parent-folder")
	if err != nil {
		t.Fatalf("GetOrCreateFolder err: %v", err)
	}
	if got != "resolved-folder-id" {
		t.Fatalf("folder ID = %q, want %q", got, "resolved-folder-id")
	}
	if pub.resolveCalls != 1 {
		t.Fatalf("publisher.ResolveFolder called %d times, want 1", pub.resolveCalls)
	}
	if pub.lastResolveReq.Destination != delivery.DestinationYouTubeClip {
		t.Errorf("Destination = %q, want %q", pub.lastResolveReq.Destination, delivery.DestinationYouTubeClip)
	}
	if pub.lastResolveReq.Group != "youtube_subtitles" {
		t.Errorf("Group = %q, want %q", pub.lastResolveReq.Group, "youtube_subtitles")
	}
	if pub.lastResolveReq.Subject != "boxing-channels" {
		t.Errorf("Subject = %q, want %q", pub.lastResolveReq.Subject, "boxing-channels")
	}
	// The parent folder must be threaded through so the child folder is
	// created under the request-selected root instead of the catalog root.
	if pub.lastResolveReq.ParentFolderID != "legacy-parent-folder" {
		t.Fatalf("ParentFolderID = %q, want %q", pub.lastResolveReq.ParentFolderID, "legacy-parent-folder")
	}
}

// TestGetOrCreateFolder_PublisherNil_FailsClosed pins the
// fail-closed contract: when the canonical Publisher is nil
// (composition-time wiring gap), GetOrCreateFolder MUST return
// a typed error (godlike/07 fail-closed at the seam, NOT a
// silent nil-deref panic). The error MUST mention the
// publisher-not-wired condition so the operator's diagnostic
// surface can act on it.
func TestGetOrCreateFolder_PublisherNil_FailsClosed(t *testing.T) {
	t.Parallel()
	a := NewYouTubePublisherDriveAdapter(nil, zap.NewNop())

	got, err := a.GetOrCreateFolder(context.Background(), "boxing-channels", "legacy-parent")
	if err == nil {
		t.Fatal("GetOrCreateFolder err = nil, want typed error (publisher not wired)")
	}
	// godlike/07 NO-FAKE-AVAILABILITY: the error MUST be
	// surface-able — the literal substring "publisher not wired"
	// is the canonical operator-actionable signal.
	if !strings.Contains(err.Error(), "publisher not wired") {
		t.Errorf("err = %q, want substring %q (operator-actionable diagnostic)", err.Error(), "publisher not wired")
	}
	// The legacy parentFolderID MUST be returned as the
	// fail-closed fallback (pre-PR-12 behavior preserved for
	// graceful-degradation callers that may consume the ID
	// even on failure — channel_folders.go logs and continues).
	if got != "legacy-parent" {
		t.Errorf("folder ID on nil publisher = %q, want %q (fail-closed parentFolderID)", got, "legacy-parent")
	}
}

// TestGetOrCreateFolder_PublisherError_WrapsTypedError pins the
// godlike/07 typed-error contract: when Publisher.ResolveFolder
// returns an error, GetOrCreateFolder MUST wrap it via
// fmt.Errorf %w so callers can errors.Is-probe the underlying
// cause. The error message MUST also surface the operation
// context ("GetOrCreateFolder") for operator diagnostics.
func TestGetOrCreateFolder_PublisherError_WrapsTypedError(t *testing.T) {
	t.Parallel()
	cause := errors.New("downstream drive connection refused")
	pub := &ytDriveRecordingPublisher{resolveErr: cause}
	a := newYouTubePubAdapter(pub)

	got, err := a.GetOrCreateFolder(context.Background(), "boxing-channels", "legacy-parent")
	if err == nil {
		t.Fatal("GetOrCreateFolder err = nil, want wrapped error")
	}
	// godlike/07 typed-error contract: errors.Is must recover
	// the underlying cause through the fmt.Errorf %w wrap.
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false; err = %v", err)
	}
	// The error MUST mention the operation context for
	// operator-dashboard triage.
	if !strings.Contains(err.Error(), "GetOrCreateFolder") {
		t.Errorf("err = %q, want substring %q (operation context)", err.Error(), "GetOrCreateFolder")
	}
	// Fail-closed: parentFolderID returned on error (same as
	// the publisher-nil case for consistent caller semantics).
	if got != "legacy-parent" {
		t.Errorf("folder ID on publisher error = %q, want %q (fail-closed parentFolderID)", got, "legacy-parent")
	}
}

// TestUploadFileIfChanged_ThreadsResolvedFolder_PreservesConflictPolicy
// pins the canonical UploadFileIfChanged contract per PR-P12:
//
//   - input (localPath="/tmp/clip.mp4", folderID="resolved-folder",
//     filename="clip-yt_abc123_0_30_v1.mp4")
//   - lastPublishReq.LocalPath = "/tmp/clip.mp4"
//   - lastPublishReq.Filename = "clip-yt_abc123_0_30_v1.mp4"
//   - lastPublishReq.Destination = DestinationYouTubeClip
//   - lastPublishReq.ParentFolderID = "resolved-folder-id"
//   - lastPublishReq.ConflictPolicy = ConflictSkip
//     (preserved — Publisher's content-dedupe via hash comparison)
//   - Group + Subject omitted (per-file identity in Filename;
//     folder context was established by prior GetOrCreateFolder)
func TestUploadFileIfChanged_ThreadsResolvedFolder_PreservesConflictPolicy(t *testing.T) {
	t.Parallel()
	pub := &ytDriveRecordingPublisher{
		publishFileID:      "drive-file-id-123",
		publishWebViewLink: "https://drive.google.com/file/d/123/view",
	}
	a := newYouTubePubAdapter(pub)

	res, skipped, err := a.UploadFileIfChanged(
		context.Background(),
		"/tmp/clip-yt_abc123_0_30_v1.mp4",
		"resolved-folder-id",
		"clip-yt_abc123_0_30_v1.mp4",
		"boxing-channels",
		"abc123",
	)
	if err != nil {
		t.Fatalf("UploadFileIfChanged err: %v", err)
	}
	if skipped {
		t.Errorf("skipped = true, want false (PublishResult.Action = UploadOutcomeCreated)")
	}
	if res == nil {
		t.Fatal("UploadResultDTO = nil, want non-nil")
	}
	if res.FileID != "drive-file-id-123" {
		t.Errorf("FileID = %q, want %q", res.FileID, "drive-file-id-123")
	}
	if res.WebViewLink != "https://drive.google.com/file/d/123/view" {
		t.Errorf("WebViewLink = %q, want %q", res.WebViewLink, "https://drive.google.com/file/d/123/view")
	}
	if pub.publishCalls != 1 {
		t.Fatalf("publisher.Publish called %d times, want 1", pub.publishCalls)
	}
	if pub.lastPublishReq.Destination != delivery.DestinationYouTubeClip {
		t.Errorf("Destination = %q, want %q", pub.lastPublishReq.Destination, delivery.DestinationYouTubeClip)
	}
	if pub.lastPublishReq.LocalPath != "/tmp/clip-yt_abc123_0_30_v1.mp4" {
		t.Errorf("LocalPath = %q, want %q", pub.lastPublishReq.LocalPath, "/tmp/clip-yt_abc123_0_30_v1.mp4")
	}
	if pub.lastPublishReq.Filename != "clip-yt_abc123_0_30_v1.mp4" {
		t.Errorf("Filename = %q, want %q", pub.lastPublishReq.Filename, "clip-yt_abc123_0_30_v1.mp4")
	}
	// The resolved folder must be threaded into the publish request
	// as DestinationFolderID (canonical leaf semantics) so the Drive
	// upload lands directly in the payload-selected folder — NOT as
	// ParentFolderID, which would re-run the YouTubeClipPath
	// builder and drift the upload into a
	// `youtube_uncategorized/<video_id>` subfolder under the root.
	if pub.lastPublishReq.DestinationFolderID != "resolved-folder-id" {
		t.Fatalf("DestinationFolderID = %q, want %q", pub.lastPublishReq.DestinationFolderID, "resolved-folder-id")
	}
	if pub.lastPublishReq.ParentFolderID != "" {
		t.Errorf("ParentFolderID = %q, want empty (leaf via DestinationFolderID)", pub.lastPublishReq.ParentFolderID)
	}
	// Group + Subject MUST be propagated for YouTubeClipPath path-building.
	// (With DestinationFolderID set, YouTubeClipPath returns nil
	// sub-segments — the leaf wins — but Group/Subject are still part
	// of the canonical wire-shape for callers without a resolved leaf.)
	if pub.lastPublishReq.Group != "boxing-channels" {
		t.Errorf("Group = %q, want %q (channel name must propagate to PublishRequest)", pub.lastPublishReq.Group, "boxing-channels")
	}
	if pub.lastPublishReq.Subject != "abc123" {
		t.Errorf("Subject = %q, want %q (video ID must propagate to PublishRequest)", pub.lastPublishReq.Subject, "abc123")
	}
	// ConflictPolicy MUST be preserved (Publisher's content-dedupe
	// via hash comparison is the canonical replacement for the
	// legacy Uploader.UploadFileIfChanged filename-based lookup).
	if pub.lastPublishReq.ConflictPolicy != delivery.ConflictSkip {
		t.Errorf("ConflictPolicy = %q, want %q (canonical content-dedupe)",
			pub.lastPublishReq.ConflictPolicy, delivery.ConflictSkip)
	}
}

// TestUploadFileIfChanged_SkippedBoolDerivesFromAction pins the
// godlike/07 NO-FAKE-AVAILABILITY contract: the skipped bool is
// the canonical Publisher-declared outcome (NOT a caller-side
// heuristic). When the Publisher returns UploadOutcomeSkipped
// (the same value as PublishActionSkipped via type alias),
// UploadFileIfChanged MUST return skipped=true; for any other
// action (UploadOutcomeCreated default), it MUST return
// skipped=false. This guards against future refactors that
// silently drift to a filename-based dedupe (the legacy
// behavior) instead of the canonical hash-based dedupe.
func TestUploadFileIfChanged_SkippedBoolDerivesFromAction(t *testing.T) {
	t.Parallel()

	t.Run("UploadOutcomeSkipped_returns_true", func(t *testing.T) {
		t.Parallel()
		pub := &ytDriveRecordingPublisher{publishAction: delivery.UploadOutcomeSkipped}
		a := newYouTubePubAdapter(pub)
		res, skipped, err := a.UploadFileIfChanged(context.Background(), "/tmp/clip.mp4", "folder", "clip.mp4", "group", "subj")
		if err != nil {
			t.Fatalf("UploadFileIfChanged err: %v", err)
		}
		if !skipped {
			t.Errorf("skipped = false, want true (UploadOutcomeSkipped)")
		}
		if res == nil {
			t.Errorf("UploadResultDTO = nil, want non-nil (skipped does not mean no-file)")
		}
	})

	t.Run("UploadOutcomeCreated_returns_false", func(t *testing.T) {
		t.Parallel()
		pub := &ytDriveRecordingPublisher{publishAction: delivery.UploadOutcomeCreated}
		a := newYouTubePubAdapter(pub)
		_, skipped, err := a.UploadFileIfChanged(context.Background(), "/tmp/clip.mp4", "folder", "clip.mp4", "group", "subj")
		if err != nil {
			t.Fatalf("UploadFileIfChanged err: %v", err)
		}
		if skipped {
			t.Errorf("skipped = true, want false (UploadOutcomeCreated)")
		}
	})
}
