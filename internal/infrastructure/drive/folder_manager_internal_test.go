package drive

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFindOrCreateFolder_NoDuplicate_OnLookupError is the P0.4 contract
// test. It pins the no-duplicate-on-transient-lookup-error surface by
// driving the lookup seam directly: when the seam returns an error,
// findOrCreateFolder MUST propagate the error verbatim AND MUST NOT
// invoke any Create path.
//
// The pre-P0.4 regression vector was the branch literal:
//
//	if err == nil && len(list.Files) > 0 {
//	    return list.Files[0].Id, nil
//	}
//	// (BUG: err != nil falls through to Files.Create below)
//
// — any transient error on Files.List caused findOrCreateFolder to
// fall through to Files.Create on the same (parent, name) pair, which
// produced a duplicate folder on Drive when the genuine folder
// existed but the transient error masked the lookup success.
//
// Internal test (package drive) so we can call findOrCreateFolder
// directly, bypassing EnsureFolder's nil-svc guard. The lookup seam is
// the only injection point; findOrCreateFolder must not call out to
// anything else, so the absence of Create in this test path implicitly
// pins the no-fallback-to-Create contract.
//
// The stub err format matches isRetryableDriveErr's substring heuristic
// ("503" + "backendError") so the production defaultFolderLookup's
// retry WOULD have attempted retries on this exact error. We bypass
// defaultFolderLookup entirely via WithLookup so the higher-level
// surface (lookup-error → propagated err, NOT Create fallback) is what
// the test asserts — the retry mechanic itself is unit-tested through
// pkg/retry + uploader_put_test.go (parallel structure to P0 #1).
func TestFindOrCreateFolder_NoDuplicate_OnLookupError(t *testing.T) {
	var lookupCalls int
	transientErr := errors.New(`googleapi: got HTTP response code 503 with body: {"error":{"code":503,"message":"backendError"}}`)
	stubLookup := func(_ context.Context, _, _ string) (string, error) {
		lookupCalls++
		return "", transientErr
	}

	adapter := &DriveFolderManagerAdapter{
		svc:    nil, // not used — findOrCreateFolder only consults a.lookup
		log:    nil, // not used — stub lookup has no logging
		lookup: stubLookup,
	}

	got, err := adapter.findOrCreateFolder(context.Background(), "parent-id", "NBA News")

	require.Error(t, err,
		"P0.4 contract: lookup-error MUST propagate from findOrCreateFolder (no silent Create fallback)")
	require.ErrorIs(t, err, transientErr,
		"the propagated error must wrap the seam's err (callers must be able to errors.Is/As the transient status)")
	require.Contains(t, err.Error(), "NO fallback-to-create",
		"the wrapped error must carry the P0.4 contract surface marker (uppercase NO) so audit trails can grep for it")
	require.Empty(t, got,
		"no folder ID expected on the lookup-error path")
	require.Equal(t, 1, lookupCalls,
		"findOrCreateFolder must consult the lookup seam exactly once per attempt")
}

// TestFindOrCreateFolder_ReusesExistingFolder pins the companion
// happy-path branch. When the lookup seam returns an existing ID,
// findOrCreateFolder MUST return that ID unchanged without invoking
// any Create path. Symmetric to the no-duplicate contract but covering
// the "folder already exists" branch that pre-P0.4 correctly handled
// (and which we want to keep verifying).
func TestFindOrCreateFolder_ReusesExistingFolder(t *testing.T) {
	var lookupCalls int
	stubLookup := func(_ context.Context, _, _ string) (string, error) {
		lookupCalls++
		return "existing-folder-id", nil
	}

	adapter := &DriveFolderManagerAdapter{
		svc:    nil,
		log:    nil,
		lookup: stubLookup,
	}

	got, err := adapter.findOrCreateFolder(context.Background(), "parent-id", "NBA News")
	require.NoError(t, err,
		"happy-path lookup returning existing ID must succeed without errors")
	require.Equal(t, "existing-folder-id", got,
		"findOrCreateFolder must return the existing folder ID unchanged")
	require.Equal(t, 1, lookupCalls,
		"happy-path lookup must be called exactly once")
}
