// Package usecase — asset_publish_pipeline_test.go: hermetic unit-test
// surface for PR-CLIPINGEST-PIPELINE Step 10 publish caller (July
// 2026).
//
// The tests below are HERMETIC: no ffmpeg, no Drive, no fs I/O. The
// publisher port is mocked (records every Publish call + feeds back a
// canned PublishResult). The cleanup port is mocked (records every
// remove call + can be configured to fail). The pure filter helper is
// exercised via t.Run table-driven cases.
//
// godlike/06 SSOT (one canonical owner per fact): the test surface
// pins the contract so adding a new rendition kind OR shifting the
// BuildPublishRequest field-mapping breaks a test, not a runtime.
//
// godlike/07 typed-error contract: every typed sentinel listed in
// the implementation file has a corresponding negative test that
// probes via errors.Is.
package usecase

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── stub publisher (records every Publish call + feeds back result/error) ──

type stubAssetPublisher struct {
	calls []delivery.PublishRequest
	// err is the canned error to return on every Publish. nil = success.
	err error
	// result is the canned PublishResult to return on success. nil
	// is allowed (some paths probe for FileID only).
	result *delivery.PublishResult
}

func (s *stubAssetPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	s.calls = append(s.calls, req)
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result, nil
	}
	// default happy pay
	return &delivery.PublishResult{
		FileID:      "drive_fake_" + req.Filename,
		Destination: req.Destination,
	}, nil
}

// stubRemoveFn — simple recorder; err returned on every invocation.
type stubRemoveFn struct {
	calls    []string
	err      error // err returned on every call (nil = success)
	notExist bool  // when true, every call returns os.ErrNotExist (re-run idempotency probe)
	recorded []string
}

func (s *stubRemoveFn) remove(localPath string) error {
	s.calls = append(s.calls, localPath)
	if s.notExist {
		// Simulates os.Remove on a non-existent path — the real
		// behaviour is *fs.PathError{Err: os.ErrNotExist}; we
		// return the canonical sentinel directly so the
		// implementation's errors.Is check drops it correctly.
		return os.ErrNotExist
	}
	if s.err != nil {
		return s.err
	}
	s.recorded = append(s.recorded, localPath)
	return nil
}

// ── test fixtures (canonical 6 renditions layered per Step 9) ───────────────

func mkRenditions() []asset.RenditionOutput {
	return []asset.RenditionOutput{
		{Kind: asset.RenditionKindMaster, LocalPath: "/tmp/master.mp4", Filename: "master.mp4", FileHash: "hash_master_64chars_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 1024},
		{Kind: asset.RenditionKindMezzanine, LocalPath: "/tmp/mezz.mp4", Filename: "mezz.mp4", FileHash: "hash_mezz_64chars_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SizeBytes: 1024},
		{Kind: asset.RenditionKindProxy, LocalPath: "/tmp/preview.mp4", Filename: "preview.mp4", FileHash: "hash_proxy_64chars_ccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", SizeBytes: 512},
		{Kind: asset.RenditionKindThumbnail, LocalPath: "/tmp/thumb.jpg", Filename: "thumb.jpg", FileHash: "hash_thumb_64chars_dddddddddddddddddddddddddddddddddddddddddddddddddddddddd", SizeBytes: 64},
		{Kind: asset.RenditionKindStoryboard, LocalPath: "/tmp/story.jpg", Filename: "story.jpg", FileHash: "hash_story_64chars_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", SizeBytes: 128},
		{Kind: asset.RenditionKindManifest, LocalPath: "/tmp/manifest.json", Filename: "manifest.json", FileHash: "hash_manifest_64chars_fffffffffffffffffffffffffffffffffffffffffffffffffffff", SizeBytes: 256},
	}
}

// ── pure filter ────────────────────────────────────────────────────────────

func TestShouldPublishRendition_PerKindMapping(t *testing.T) {
	cases := []struct {
		name string
		kind asset.RenditionKind
		want bool
	}{
		{"master → publish", asset.RenditionKindMaster, true},
		{"proxy/preview → publish", asset.RenditionKindProxy, true},
		{"manifest → publish", asset.RenditionKindManifest, true},
		{"mezzanine → skip (redundant)", asset.RenditionKindMezzanine, false},
		{"thumbnail → skip (out of spec)", asset.RenditionKindThumbnail, false},
		{"storyboard → skip (out of spec)", asset.RenditionKindStoryboard, false},
		{"unknown future kind → fail-safe skip", asset.RenditionKind("vo_xml_track_v1"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldPublishRendition(tc.kind)
			if got != tc.want {
				t.Fatalf("kind=%s got=%v want=%v", tc.kind, got, tc.want)
			}
		})
	}
}

// ── happy path ─────────────────────────────────────────────────────────────

func TestPublishRenditionsToYouTubeAsset_HappyPath_ThreePublishes_ThreeCleanups(t *testing.T) {
	pub := &stubAssetPublisher{}
	rm := &stubRemoveFn{}
	rends := mkRenditions()

	report, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		pub,
		"channel_xyz",
		"video_abc",
		"asset_123",
		rends,
		rm.remove,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 6 inputs, 3 published, 3 filtered (mezzanine/thumb/story).
	if report.InputCount != 6 {
		t.Errorf("InputCount = %d, want 6", report.InputCount)
	}
	if len(report.Published) != 3 {
		t.Errorf("Published len = %d, want 3", len(report.Published))
	}
	if len(report.FilteredOut) != 3 {
		t.Errorf("FilteredOut len = %d, want 3", len(report.FilteredOut))
	}
	if len(report.CleanedUp) != 3 {
		t.Errorf("CleanedUp len = %d, want 3", len(report.CleanedUp))
	}

	// publisher.Publish called exactly 3 times — one for each
	// published-eligible rendition (master + proxy + manifest).
	if len(pub.calls) != 3 {
		t.Errorf("publisher calls = %d, want 3", len(pub.calls))
	}

	// Filtered kinds are exactly mezzanine/thumbnail/storyboard.
	filtered := map[asset.RenditionKind]bool{}
	for _, k := range report.FilteredOut {
		filtered[k] = true
	}
	for _, want := range []asset.RenditionKind{
		asset.RenditionKindMezzanine,
		asset.RenditionKindThumbnail,
		asset.RenditionKindStoryboard,
	} {
		if !filtered[want] {
			t.Errorf("expected filtered kind missing: %s", want)
		}
	}

	// Cleanup was called for each successful publish — 3 times.
	if len(rm.calls) != 3 {
		t.Errorf("remove calls = %d, want 3", len(rm.calls))
	}
	if len(rm.recorded) != 3 {
		t.Errorf("recorded (successful) remove calls = %d, want 3", len(rm.recorded))
	}
	// CleanupFailures must be empty on the happy path.
	if len(report.CleanupFailures) != 0 {
		t.Errorf("CleanupFailures = %v, want []", report.CleanupFailures)
	}
}

// ── per-rendition request mapping (Step 9 thread) ──────────────────────────

func TestPublishRenditionsToYouTubeAsset_ThreadsSizeAndContentHash(t *testing.T) {
	pub := &stubAssetPublisher{}
	rm := &stubRemoveFn{}
	rends := mkRenditions()

	_, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		pub,
		"channel_xyz",
		"video_abc",
		"asset_123",
		rends,
		rm.remove,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pub.calls) != 3 {
		t.Fatalf("publish calls = %d, want 3", len(pub.calls))
	}

	// Build a per-Kind lookup so we can assert each call carried
	// the canonical RenditionOutput.SizeBytes + FileHash + LocalPath
	// (the Step 9 thread).
	byKind := map[asset.RenditionKind]delivery.PublishRequest{}
	for _, c := range pub.calls {
		// The locator is LocalPath here (Filenames are unique in
		// the fixture set; using Kind + LocalPath gives a precise
		// mapping without depending on Filename uniqueness).
		switch c.LocalPath {
		case "/tmp/master.mp4":
			byKind[asset.RenditionKindMaster] = c
		case "/tmp/preview.mp4":
			byKind[asset.RenditionKindProxy] = c
		case "/tmp/manifest.json":
			byKind[asset.RenditionKindManifest] = c
		}
	}

	wantCases := []struct {
		kind      asset.RenditionKind
		sizeBytes int64
		hash      string
	}{
		{asset.RenditionKindMaster, 1024, "hash_master_64chars_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{asset.RenditionKindProxy, 512, "hash_proxy_64chars_ccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		{asset.RenditionKindManifest, 256, "hash_manifest_64chars_fffffffffffffffffffffffffffffffffffffffffffffffffffff"},
	}
	for _, w := range wantCases {
		got, ok := byKind[w.kind]
		if !ok {
			t.Errorf("missing publish call for kind=%s", w.kind)
			continue
		}
		if got.Destination != delivery.DestinationYouTubeAsset {
			t.Errorf("kind=%s Destination = %q, want %q", w.kind, got.Destination, delivery.DestinationYouTubeAsset)
		}
		if got.SizeBytes != w.sizeBytes {
			t.Errorf("kind=%s SizeBytes = %d, want %d", w.kind, got.SizeBytes, w.sizeBytes)
		}
		if got.ContentHash != w.hash {
			t.Errorf("kind=%s ContentHash = %q, want %q", w.kind, got.ContentHash, w.hash)
		}
		if got.ChannelID != "channel_xyz" {
			t.Errorf("kind=%s ChannelID = %q, want %q", w.kind, got.ChannelID, "channel_xyz")
		}
		if got.Subject != "video_abc" {
			t.Errorf("kind=%s Subject = %q, want %q", w.kind, got.Subject, "video_abc")
		}
		if got.AssetID != "asset_123" {
			t.Errorf("kind=%s AssetID = %q, want %q", w.kind, got.AssetID, "asset_123")
		}
	}
}

// ── cleanup gate ───────────────────────────────────────────────────────────

func TestPublishRenditionsToYouTubeAsset_CleanupErrorDoesNotPropagate(t *testing.T) {
	pub := &stubAssetPublisher{}
	rm := &stubRemoveFn{err: errors.New("disk full")} // every remove call fails
	rends := mkRenditions()

	report, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		pub,
		"channel_xyz",
		"video_abc",
		"asset_123",
		rends,
		rm.remove,
	)
	// The publish should still succeed — only the cleanup failed.
	if err != nil {
		t.Fatalf("unexpected error (cleanup failure MUST NOT propagate): %v", err)
	}
	if len(report.Published) != 3 {
		t.Errorf("Published len = %d, want 3 (publish should succeed even though cleanup failed)", len(report.Published))
	}
	// Every successful publish attempted cleanup; all 3 failed.
	if len(report.CleanupFailures) != 3 {
		t.Errorf("CleanupFailures len = %d, want 3", len(report.CleanupFailures))
	}
	// 3 publishes succeeded; 3 cleanups attempted.
	if len(rm.calls) != 3 {
		t.Errorf("remove calls = %d, want 3 (each published rendition attempts cleanup)", len(rm.calls))
	}
	// No successful cleanups.
	if len(rm.recorded) != 0 {
		t.Errorf("recorded (successful) remove calls = %d, want 0", len(rm.recorded))
	}
}

// os.ErrNotExist on cleanup is silently dropped (re-run idempotency).
func TestPublishRenditionsToYouTubeAsset_CleanupErrNotExistIsIdempotent(t *testing.T) {
	pub := &stubAssetPublisher{}
	rm := &stubRemoveFn{notExist: true} // every remove returns os.ErrNotExist (via override)
	rends := mkRenditions()

	report, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		pub,
		"channel_xyz",
		"video_abc",
		"asset_123",
		rends,
		rm.remove,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CleanupFailures MUST be empty — os.ErrNotExist is the re-run
	// idempotency case, not a leak.
	if len(report.CleanupFailures) != 0 {
		t.Errorf("CleanupFailures = %v, want [] (os.ErrNotExist must be silently dropped)", report.CleanupFailures)
	}
	if len(report.CleanedUp) != 0 {
		t.Errorf("CleanedUp = %v, want [] (the notExist path doesn't record it as a successful removal)", report.CleanedUp)
	}
}

// ── publisher error path (fail-fast + no cleanup) ──────────────────────────

func TestPublishRenditionsToYouTubeAsset_PublisherError_LeavesFilesAndStopsEarly(t *testing.T) {
	pub := &stubAssetPublisher{
		err: errors.New("drive upload failed"),
	}
	rm := &stubRemoveFn{}
	rends := mkRenditions()

	report, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		pub,
		"channel_xyz",
		"video_abc",
		"asset_123",
		rends,
		rm.remove,
	)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrAssetPublishFailed) {
		t.Errorf("err = %v, want errors.Is(err, ErrAssetPublishFailed)", err)
	}
	// Fail-fast: exactly 1 Publish call before the function bails.
	if len(pub.calls) != 1 {
		t.Errorf("publish calls = %d, want 1 (fail-fast on first error)", len(pub.calls))
	}
	// Cleanup MUST NOT be called when the publish failed (the
	// user-spec gating contract: "gates os.Remove on
	// PublishResult-with-no-error").
	if len(rm.calls) != 0 {
		t.Errorf("remove calls = %d, want 0 (publish failed → keep local file)", len(rm.calls))
	}
	if len(report.Published) != 0 {
		t.Errorf("Published len = %d, want 0 (publish failed)", len(report.Published))
	}
}

// ── fail-closed setup-time sentinels (godlike/07) ─────────────────────────

func TestPublishRenditionsToYouTubeAsset_NilPublisher(t *testing.T) {
	_, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		nil,
		"channel_xyz",
		"video_abc",
		"asset_123",
		mkRenditions(),
		func(string) error { return nil },
	)
	if !errors.Is(err, ErrAssetPublishPipelineNilPublisher) {
		t.Errorf("err = %v, want errors.Is(err, ErrAssetPublishPipelineNilPublisher)", err)
	}
}

func TestPublishRenditionsToYouTubeAsset_NilRemoveFn(t *testing.T) {
	_, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		&stubAssetPublisher{},
		"channel_xyz",
		"video_abc",
		"asset_123",
		mkRenditions(),
		nil,
	)
	if !errors.Is(err, ErrAssetPublishPipelineNilRemoveFn) {
		t.Errorf("err = %v, want errors.Is(err, ErrAssetPublishPipelineNilRemoveFn)", err)
	}
}

func TestPublishRenditionsToYouTubeAsset_MissingFields_AllBranches(t *testing.T) {
	cases := []struct {
		name    string
		channel string
		video   string
		asset   string
	}{
		{"empty channel_id", "", "video_abc", "asset_123"},
		{"empty video_id", "channel_xyz", "", "asset_123"},
		{"empty asset_id", "channel_xyz", "video_abc", ""},
		{"all three empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PublishRenditionsToYouTubeAsset(
				context.Background(),
				&stubAssetPublisher{},
				tc.channel,
				tc.video,
				tc.asset,
				mkRenditions(),
				func(string) error { return nil },
			)
			if !errors.Is(err, ErrAssetPublishPipelineMissingFields) {
				t.Errorf("err = %v, want errors.Is(err, ErrAssetPublishPipelineMissingFields)", err)
			}
		})
	}
}

// ── publish-result FileID propagated to PublishedRenditionOutcome ──────────

func TestPublishRenditionsToYouTubeAsset_FileIDPopulatedOnSuccess(t *testing.T) {
	pub := &stubAssetPublisher{
		result: &delivery.PublishResult{FileID: "stub_file_id_xyz"},
	}
	rm := &stubRemoveFn{}
	rends := mkRenditions()

	report, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		pub,
		"channel_xyz",
		"video_abc",
		"asset_123",
		rends,
		rm.remove,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Every successful publish should have populated FileID from the
	// canned PublishResult.
	for i, p := range report.Published {
		if p.FileID != "stub_file_id_xyz" {
			t.Errorf("Published[%d].FileID = %q, want %q", i, p.FileID, "stub_file_id_xyz")
		}
	}
}

// ── empty rendition slice is NOT an error ──────────────────────────────────

func TestPublishRenditionsToYouTubeAsset_EmptyRenditionsIsOK(t *testing.T) {
	pub := &stubAssetPublisher{}
	rm := &stubRemoveFn{}
	report, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		pub,
		"channel_xyz",
		"video_abc",
		"asset_123",
		nil,
		rm.remove,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.InputCount != 0 {
		t.Errorf("InputCount = %d, want 0", report.InputCount)
	}
	if len(report.Published) != 0 {
		t.Errorf("Published len = %d, want 0", len(report.Published))
	}
	if len(pub.calls) != 0 {
		t.Errorf("publish calls = %d, want 0", len(pub.calls))
	}
}

// ── helper convenience wrapper ─────────────────────────────────────────────

func TestPublishRenditionsToYouTubeAssetOSRemove_WiresOsRemove(t *testing.T) {
	pub := &stubAssetPublisher{}
	rends := mkRenditions()
	// The convenience wrapper injects os.Remove. The fixture paths
	// (/tmp/master.mp4 etc.) do NOT exist in the test process —
	// os.Remove returns *fs.PathError{Err: os.ErrNotExist}, which
	// the implementation errors.Is-drops (re-run idempotency).
	// We verify: (1) the publish path is reached, (2) 3 publishes
	// happen, (3) the cleanup is silently dropped (not propagated).
	report, err := PublishRenditionsToYouTubeAssetOSRemove(
		context.Background(),
		pub,
		"channel_xyz",
		"video_abc",
		"asset_123",
		rends,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pub.calls) != 3 {
		t.Errorf("publish calls = %d, want 3", len(pub.calls))
	}
	if len(report.Published) != 3 {
		t.Errorf("Published len = %d, want 3", len(report.Published))
	}
	// CleanupFailures MUST be 0 — os.Remove on non-existent test
	// paths returns os.ErrNotExist which the implementation
	// errors.Is-drops (re-run idempotency).
	if len(report.CleanupFailures) != 0 {
		t.Errorf("CleanupFailures len = %d, want 0 (os.ErrNotExist on non-existent test paths MUST be dropped)", len(report.CleanupFailures))
	}
	if len(report.CleanedUp) != 0 {
		t.Errorf("CleanedUp len = %d, want 0 (os.ErrNotExist must NOT be recorded as a successful removal)", len(report.CleanedUp))
	}
}

// ── rendition ordering is preserved (canonical Step 9 3-of-6 determinism) ──

func TestPublishRenditionsToYouTubeAsset_PublishOrderFollowsInputOrder(t *testing.T) {
	pub := &stubAssetPublisher{}
	rm := &stubRemoveFn{}
	rends := mkRenditions()

	_, err := PublishRenditionsToYouTubeAsset(
		context.Background(),
		pub,
		"channel_xyz",
		"video_abc",
		"asset_123",
		rends,
		rm.remove,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expected order: master, preview, manifest (filter preserves
	// input order; only the eligible kinds are uploaded).
	wantOrder := []string{
		"/tmp/master.mp4",
		"/tmp/preview.mp4",
		"/tmp/manifest.json",
	}
	if len(pub.calls) != len(wantOrder) {
		t.Fatalf("publish calls = %d, want %d", len(pub.calls), len(wantOrder))
	}
	for i, want := range wantOrder {
		if pub.calls[i].LocalPath != want {
			t.Errorf("call[%d].LocalPath = %q, want %q", i, pub.calls[i].LocalPath, want)
		}
	}
}
