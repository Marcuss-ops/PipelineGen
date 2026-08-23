//go:build drivepolicypkgtest

package drive

// publisher_policies_test.go (June 2026)
//
// The 3 P0 #2 tests in this file exercise NewDestinationRegistryWithPolicies
// (the test-only seam in internal/application/assets/delivery/registry_test_factories.go).
// Both files share the //go:build drivepolicypkgtest tag so:
//
//   - Default `go test ./internal/infrastructure/drive/...` does NOT compile
//     these tests (the test affordance is hidden from production code paths).
//   - `go test -tags drivepolicypkgtest ./internal/infrastructure/drive/...`
//     compiles them along with the factory and pins the P0 #2 invariants.
//
// The helper `degenerateEmptyPathBuilder` lived in publisher_test.go before
// this refactor; it moved here to co-locate with the (only) callers — keeping
// the production `go doc` and `go vet` surface clean when -tags is off.

import (
	"context"
	"testing"

	"go.uber.org/zap"

	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	platformdelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/stretchr/testify/require"
)

// degenerateEmptyPathBuilder is the canonical test-only PathBuilder that
// silently returns an empty segment slice. It mirrors what a malformed
// or even maliciously crafted policy could do at runtime, and only
// RequireSubpath=true callers can catch it. Used by P0 #2 regression
// tests to verify that resolveDestination (and therefore both Publish
// AND ResolveFolder) rejects it symmetrically.
func degenerateEmptyPathBuilder(_ delivery.PublishRequest) ([]string, error) {
	return []string{}, nil
}

// TestPublisher_PublishRejectsRequireSubpath is the P0 #2 paired test
// for the Publish side of the symmetric enforcement. Uses a degenerate
// registry where RequireSubpath=true and the PathBuilder returns an
// empty segment slice. Without the centralised resolveDestination
// helper, this would have been caught inside Publish's Step 3; with
// the refactor, the check lives in the helper so this test verifies
// the Publish path still rejects (which it does, via the helper).
func TestPublisher_PublishRejectsRequireSubpath(t *testing.T) {
	reg := platformdelivery.NewDestinationRegistryWithPolicies(map[delivery.DestinationKey]delivery.DestinationPolicy{
		delivery.DestinationYouTubeClip: {
			RootFolderID:   "clips-root",
			PathBuilder:    degenerateEmptyPathBuilder,
			RequireSubpath: true,
		},
	})
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		// Group/Subject omitted on purpose: degenerateEmptyPathBuilder
		// accepts them and returns []string{}, forcing the helper to
		// trigger the RequireSubpath check.
	})
	require.Error(t, err, "Publish must reject when PathBuilder returns []string{} and RequireSubpath=true")
	require.Contains(t, err.Error(), "forbidden", "Publish error must come from the RequireSubpath check")
}

// TestPublisher_ResolveFolder_HonorsRequireSubpath is the P0 #2 regression
// catch for the symmetric enforcement. Before P0 #2, ResolveFolder
// skipped the RequireSubpath check entirely (it had a near-duplicate
// of Steps 1-4 but dropped Step 3), so this test would have FAILED:
// ResolveFolder would have returned the rootFolderID without error,
// even though the SAME request would have been rejected by Publish.
//
// With P0 #2 both Publish and ResolveFolder go through
// resolveDestination so the check fires symmetrically. This test
// would silently PASS today but still serves as a guard against
// future drift — if a developer reverts the refactor and ResolveFolder
// gets a duplicated Steps-1-4 block again, this test catches them.
func TestPublisher_ResolveFolder_HonorsRequireSubpath(t *testing.T) {
	reg := platformdelivery.NewDestinationRegistryWithPolicies(map[delivery.DestinationKey]delivery.DestinationPolicy{
		delivery.DestinationYouTubeClip: {
			RootFolderID:   "clips-root",
			PathBuilder:    degenerateEmptyPathBuilder,
			RequireSubpath: true,
		},
	})
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	got, err := pub.ResolveFolder(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		// Group/Subject omitted — degenerateEmptyPathBuilder returns
		// []string{} on purpose so the helper triggers the check.
	})
	require.Error(t, err, "ResolveFolder must reject when PathBuilder returns []string{} and RequireSubpath=true (was the Pre-P0#2 bypass — must NOT regress)")
	require.Contains(t, err.Error(), "forbidden", "ResolveFolder error must come from the RequireSubpath check, not from a uploader/folder error")
	require.Empty(t, got, "ResolveFolder must return empty folder ID on rejection")

	// Sanity: the symmetric Publish call must ALSO reject with the
	// SAME error class. This proves both paths flow through the same
	// helper and the user-facing surface is now consistent.
	_, publishErr := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
	})
	require.Error(t, publishErr, "Publish must reject symmetrically with ResolveFolder")
	require.Contains(t, publishErr.Error(), "forbidden")

	// Sanity: EnsureFolder must NOT have been called. The check fires
	// before folder hierarchy creation, so no Drive writes can leak.
	require.Empty(t, folders.ensureCalls, "ResolveFolder must reject BEFORE EnsureFolder (no Drive writes on rejection)")
	require.Empty(t, files.uploadCalls, "ResolveFolder must reject BEFORE PutFile (no upload considerations)")
}

// TestPublisher_ResolveFolder_SuccessWhenSubpathProvided is the positive
// counterpart. With a degenerate registry but a real PathBuilder that
// returns segments, both Publish and ResolveFolder should succeed.
func TestPublisher_ResolveFolder_SuccessWhenSubpathProvided(t *testing.T) {
	reg := platformdelivery.NewDestinationRegistryWithPolicies(map[delivery.DestinationKey]delivery.DestinationPolicy{
		delivery.DestinationYouTubeClip: {
			RootFolderID: "clips-root",
			PathBuilder: func(_ delivery.PublishRequest) ([]string, error) {
				return []string{"NBA News", "video-xyz"}, nil
			},
			RequireSubpath: true,
		},
	})
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	got, err := pub.ResolveFolder(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
	})
	require.NoError(t, err, "ResolveFolder should succeed when PathBuilder returns non-empty segments")
	require.Equal(t, "video-folder-id", got, "ResolveFolder should return the leaf folder ID from EnsureFolder")
	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, []string{"NBA News", "video-xyz"}, folders.ensureCalls[0].segments)
}

// TestPublisher_SymmetricRequireSubpath_AcrossDestinations is the F1.2
// table-driven regression pin for the P0 #2 (June 2026) symmetric
// RequireSubpath enforcement. The companion tests above pin the
// invariant for ONE destination (DestinationYouTubeClip); this test
// table-drives across all 8 canonical destinations so a future
// regression that reverts RequireSubpath enforcement for a subset
// (e.g. ResolveFolder only enforces it for YouTube and stops
// enforcing it for Book) is caught immediately.
//
// It exercises both directions:
//   - RequireSubpath=true + empty segments  → both Publish and
//     ResolveFolder MUST reject with a "forbidden" error containing
//     the destination key.
//   - RequireSubpath=false + empty segments → BOTH Publish and
//     ResolveFolder MUST succeed (root upload is permitted when the
//     policy explicitly opts in).
//
// Build tag: //go:build drivepolicypkgtest (gated; see
// registry_test_factories.go for the pairing rationale and the
// pipeline's `-tags drivepolicypkgtest` opt-in convention).
func TestPublisher_SymmetricRequireSubpath_AcrossDestinations(t *testing.T) {
	dests := []delivery.DestinationKey{
		delivery.DestinationYouTubeClip,
		delivery.DestinationArtlist,
		delivery.DestinationStock,
		delivery.DestinationImage,
		delivery.DestinationVoiceover,
		delivery.DestinationBook,
		delivery.DestinationScript,
		delivery.DestinationSoundEffect,
	}

	for _, dest := range dests {
		dest := dest
		// ── Direction A: RequireSubpath=true + empty segments → REJECT ──
		t.Run(string(dest)+"/reject", func(t *testing.T) {
			reg := platformdelivery.NewDestinationRegistryWithPolicies(map[delivery.DestinationKey]delivery.DestinationPolicy{
				dest: {
					RootFolderID:   "root-" + string(dest),
					PathBuilder:    degenerateEmptyPathBuilder,
					RequireSubpath: true,
				},
			})
			// `result` intentionally left as zero value — the
			// RequireSubpath check fires inside resolveDestination
			// before the `len(segments) > 0` branch, so EnsureFolder
			// is unreachable on this path. (See /accept subtest for
			// the symmetric rationale.)
			folders := &fakeFolderManager{}
			files := &fakeFileUploader{}
			pub, err := NewPublisher(reg, folders, files, zap.NewNop())
			require.NoError(t, err)

			req := delivery.PublishRequest{
				Destination: dest,
				LocalPath:   "/tmp/x.bin",
				Filename:    "x.bin",
			}

			_, publishErr := pub.Publish(context.Background(), req)
			require.Error(t, publishErr,
				"Publish must reject empty segments when RequireSubpath=true for destination %q", dest)
			require.Contains(t, publishErr.Error(), "forbidden",
				"Publish error must come from the symmetric RequireSubpath check (destination %q)", dest)
			require.Contains(t, publishErr.Error(), string(dest),
				"Publish error must identify the destination (destination %q)", dest)

			got, resolveErr := pub.ResolveFolder(context.Background(), req)
			require.Error(t, resolveErr,
				"ResolveFolder must reject empty segments when RequireSubpath=true for destination %q (was the Pre-P0#2 bypass — must NOT regress)", dest)
			require.Contains(t, resolveErr.Error(), "forbidden",
				"ResolveFolder error must come from the symmetric RequireSubpath check (destination %q)", dest)
			require.Contains(t, resolveErr.Error(), string(dest),
				"ResolveFolder error must identify the destination (destination %q)", dest)
			require.Empty(t, got,
				"ResolveFolder must return empty folder ID on rejection (destination %q)", dest)

			require.Empty(t, folders.ensureCalls,
				"ResolveFolder must reject BEFORE EnsureFolder is called (destination %q)", dest)
			require.Empty(t, files.uploadCalls,
				"Publish must reject BEFORE PutFile is called (destination %q)", dest)
		})

		// ── Direction B: RequireSubpath=false + empty segments → ACCEPT ──
		// Catches a future regression that flips the symmetric check to
		// "reject when len(0)" unconditionally (which would over-block
		// root-upload destinations that intentionally opt out).
		//
		// `fakeFolderManager.result` is intentionally left as the zero
		// value ("") because EnsureFolder is NOT called on this path
		// (the `len(segments) > 0` short-circuit in resolveDestination
		// skips it). Initialising `result` here would mislead readers
		// into expecting EnsureFolder to be exercised.
		t.Run(string(dest)+"/accept", func(t *testing.T) {
			reg := platformdelivery.NewDestinationRegistryWithPolicies(map[delivery.DestinationKey]delivery.DestinationPolicy{
				dest: {
					RootFolderID:   "root-" + string(dest),
					PathBuilder:    degenerateEmptyPathBuilder,
					RequireSubpath: false,
				},
			})
			folders := &fakeFolderManager{}
			files := &fakeFileUploader{}
			pub, err := NewPublisher(reg, folders, files, zap.NewNop())
			require.NoError(t, err)

			req := delivery.PublishRequest{
				Destination: dest,
				LocalPath:   "/tmp/x.bin",
				Filename:    "x.bin",
			}

			got, resolveErr := pub.ResolveFolder(context.Background(), req)
			require.NoError(t, resolveErr,
				"ResolveFolder must succeed with empty segments when RequireSubpath=false (destination %q) — root upload is permitted by policy", dest)
			require.Equal(t, "root-"+string(dest), got,
				"ResolveFolder must return the root folder ID when segments are empty and RequireSubpath is opt-out (destination %q) — proves the use-root branch was taken", dest)
			// EnsureFolder must NOT have been called (empty segments ⇒ root folder used).
			require.Empty(t, folders.ensureCalls,
				"ResolveFolder must skip EnsureFolder when segments are empty (destination %q)", dest)

			_, publishErr := pub.Publish(context.Background(), req)
			require.NoError(t, publishErr,
				"Publish must succeed with empty segments when RequireSubpath=false (destination %q)", dest)
			require.Len(t, files.uploadCalls, 1,
				"Publish must call PutFile exactly once when explicitly allowed (destination %q)", dest)
		})
	}
}

// TestPublisher_FolderPathEmptyForRootUpload closes the audit
// “root upload vietato anche tramite ResolveFolder” gap observed in
// Phase 1 step 13: when RequireSubpath=false and PathBuilder returns
// empty segments (root-folder upload), FolderPath (the derived
// single-string surface) must be empty — NOT a stray "/" sentinel.
// Also pins the P0 #9 enriched fields (DownloadLink + Action) on
// the root-upload branch so the no-reconstruction contract holds
// end-to-end, not just for subpath destinations.
//
// This test lives in publisher_policies_test.go (alongside the
// other /accept and /reject policy tests) because it requires
// `platformdelivery.NewDestinationRegistryWithPolicies`, which is itself
// build-tag gated via internal/application/assets/delivery/registry_test_factories.go.
func TestPublisher_FolderPathEmptyForRootUpload(t *testing.T) {
	reg := platformdelivery.NewDestinationRegistryWithPolicies(map[delivery.DestinationKey]delivery.DestinationPolicy{
		delivery.DestinationYouTubeClip: {
			RootFolderID:   "clips-root",
			PathBuilder:    degenerateEmptyPathBuilder,
			RequireSubpath: false, // root-upload opt-in
		},
	})
	folders := &fakeFolderManager{result: "clips-root"}
	files := &fakeFileUploader{putAction: PutActionCreated}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "clip_at_root.mp4",
		// Group + Subject omitted so degenerateEmptyPathBuilder
		// returns []string{} → root-folder upload surface.
	})
	require.NoError(t, err)
	// Path/Folder invariants for the root-upload branch.
	require.Empty(t, result.FolderPath,
		"FolderPath must be empty when PathSegments is empty (root-folder upload) — strings.Join of []string{} is \"\", NOT a stray \"/\" sentinel")
	require.Empty(t, result.PathSegments,
		"PathSegments must remain empty for root-folder upload")
	require.Equal(t, "clips-root", result.FolderID,
		"FolderID must collapse to RootFolderID when no segments are built")
	// P0 #9 enriched fields MUST also land on the root-upload branch.
	// Equal (not NotEmpty) pins BOTH presence AND format — closes the
	// no-reconstruction contract end-to-end.
	require.Equal(t, "https://drive.google.com/uc?id=fake-file-id", result.DownloadLink,
		"DownloadLink must be populated verbatim on the root-upload branch (P0 #9 contract is end-to-end, NOT just for subpath destinations)")
	require.Equal(t, pub.actionFor(PutActionCreated), result.Action,
		"Action must translate to PublishActionCreated on the root-upload branch via the SAME Publisher.actionFor that Publish uses (single source of truth)")
	// Drive write-surface invariants for the root-upload branch.
	require.Empty(t, folders.ensureCalls,
		"EnsureFolder MUST NOT be called on the root-folder upload branch")
	require.Len(t, files.uploadCalls, 1,
		"PutFile MUST be called exactly once on the root-folder upload branch")
}
