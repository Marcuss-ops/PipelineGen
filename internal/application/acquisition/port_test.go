// Package acquisition — port_test.go (Stock Cutover §12-4, July 2026).
//
// Hermetic tests for the canonical SourceStager port surface:
//   - port-level types (SourceRef, PrepareRequest, PrepareContext)
//   - typed-error contract (errors.Is against the 6 sentinels)
//   - deterministic-derivation helpers (IdempotencyKey, CleanupToken,
//     StageID) — byte-stable across calls + cross-process-stable
//   - Validate + Validated gates — first-violation surfaced
//   - Expired / HasLocal — caller-side guards
//
// All tests are hermetic (no filesystem, no subprocess). The filesystem
// concrete's integration tests live in
// internal/infrastructure/acquisition/filesystem_stager_test.go.

package acquisition

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSourceStager is the recording stub used by the hermetic
// test surface. Production concretes (filesystem-backed,
// yt-dlp-backed) live under internal/infrastructure/acquisition/.
type fakeSourceStager struct {
	prepareCalls int
	releaseCalls int

	// lastPrepareRequest captures the most recent Prepare request
	// so tests can assertions on the deterministic-derivation
	// outcomes (IdempotencyKey, etc.).
	lastPrepareRequest PrepareRequest

	// lastReleaseToken captures the most recent Release token.
	lastReleaseToken string

	// pre-loaded Prepare receipts — when non-nil, Prepare returns
	// (receipts[i], nil) on the i-th call. When nil, Prepare
	// returns (nil, errOnPrepare) so tests can simulate
	// pipeline-level failures.
	receipts []*PrepareContext

	// errOnPrepare makes the next Prepare call return err instead
	// of a receipt (when receipts is nil or exhausted).
	errOnPrepare error
}

func (f *fakeSourceStager) Prepare(_ context.Context, req PrepareRequest) (*PrepareContext, error) {
	f.prepareCalls++
	f.lastPrepareRequest = req
	if len(f.receipts) > 0 {
		receipt := f.receipts[0]
		f.receipts = f.receipts[1:]
		if f.errOnPrepare != nil {
			err := f.errOnPrepare
			f.errOnPrepare = nil
			return receipt, err // return receipt + simulated failure
		}
		return receipt, nil
	}
	if f.errOnPrepare != nil {
		err := f.errOnPrepare
		f.errOnPrepare = nil
		return nil, err
	}
	return nil, ErrAcquisitionPrepareFailed
}

func (f *fakeSourceStager) Release(_ context.Context, token string) error {
	f.releaseCalls++
	f.lastReleaseToken = token
	return nil
}

// Compile-time assertion: *fakeSourceStager satisfies SourceStager.
var _ SourceStager = (*fakeSourceStager)(nil)

// ── Validate: PrepareRequest gate ───────────────────────────────────

func TestPrepareRequest_Validate_EmptyURL_ReturnsTypedError(t *testing.T) {
	req := PrepareRequest{
		Source:         SourceRef{URL: ""},
		IdempotencyKey: "k-1",
	}
	err := req.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionInvalidRequest), "errors.Is sentinel propagation")
	assert.Contains(t, err.Error(), "URL must be non-empty", "diagnostic carries the rule's invariant")
}

func TestPrepareRequest_Validate_EmptyIdempotencyKey_ReturnsTypedError(t *testing.T) {
	req := PrepareRequest{
		Source:         SourceRef{URL: "https://example.com/x.mp4"},
		IdempotencyKey: "", // ← the defensible-empty case
	}
	err := req.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionInvalidRequest))
	assert.Contains(t, err.Error(), "IdempotencyKey must be non-empty")
}

func TestPrepareRequest_Validate_NegativeTimeout_ReturnsTypedError(t *testing.T) {
	req := PrepareRequest{
		Source:         SourceRef{URL: "https://example.com/x.mp4"},
		IdempotencyKey: "k-1",
		Timeout:        -1 * time.Second,
	}
	err := req.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionInvalidRequest))
	assert.Contains(t, err.Error(), "Timeout")
}

func TestPrepareRequest_Validate_NegativeTTL_ReturnsTypedError(t *testing.T) {
	req := PrepareRequest{
		Source:         SourceRef{URL: "https://example.com/x.mp4"},
		IdempotencyKey: "k-1",
		TTL:            -1 * time.Hour,
	}
	err := req.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionInvalidRequest))
	assert.Contains(t, err.Error(), "TTL")
}

func TestPrepareRequest_Validate_HappyPath_ReturnsNil(t *testing.T) {
	req := PrepareRequest{
		Source:         SourceRef{URL: "https://example.com/x.mp4"},
		IdempotencyKey: "k-1",
		Timeout:        30 * time.Second,
		TTL:            12 * time.Hour,
	}
	require.NoError(t, req.Validate())
}

// ── Validate: PrepareContext gate ────────────────────────────────────

func TestPrepareContext_Validated_NilPointer_ReturnsTypedError(t *testing.T) {
	var c *PrepareContext
	err := c.Validated()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionInvalidRequest))
}

func TestPrepareContext_Validated_EmptyURL_ReturnsTypedError(t *testing.T) {
	c := &PrepareContext{
		SHA256:       "abc",
		CleanupToken: "ct-1",
		LocalPath:    "/tmp/staged.mp4",
	}
	err := c.Validated()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionInvalidRequest))
	assert.Contains(t, err.Error(), "SourceRef.URL")
}

func TestPrepareContext_Validated_EmptyCleanupToken_ReturnsTypedError(t *testing.T) {
	c := &PrepareContext{
		SourceRef: SourceRef{URL: "https://example.com/x.mp4"},
		SHA256:    "abc",
		LocalPath: "/tmp/staged.mp4",
	}
	err := c.Validated()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionInvalidRequest))
	assert.Contains(t, err.Error(), "CleanupToken")
}

func TestPrepareContext_Validated_EmptySHA256_ReturnsTypedError(t *testing.T) {
	c := &PrepareContext{
		SourceRef:    SourceRef{URL: "https://example.com/x.mp4"},
		CleanupToken: "ct-1",
		LocalPath:    "/tmp/staged.mp4",
	}
	err := c.Validated()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionInvalidRequest))
	assert.Contains(t, err.Error(), "SHA256")
}

func TestPrepareContext_Validated_BothLocalAndStorageEmpty_ReturnsTypedError(t *testing.T) {
	c := &PrepareContext{
		SourceRef:    SourceRef{URL: "https://example.com/x.mp4"},
		SHA256:       "abc",
		CleanupToken: "ct-1",
	}
	err := c.Validated()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionInvalidRequest))
	assert.Contains(t, err.Error(), "LocalPath AND StorageURI are both empty")
}

func TestPrepareContext_Validated_HappyPath_ReturnsNil(t *testing.T) {
	c := &PrepareContext{
		SourceRef:    SourceRef{URL: "https://example.com/x.mp4"},
		SHA256:       "abc123",
		CleanupToken: "ct-1",
		LocalPath:    "/tmp/staged.mp4",
	}
	require.NoError(t, c.Validated())
}

// ── HasLocal / Expired ───────────────────────────────────────────────

func TestPrepareContext_HasLocal_LocalAndEmptyURI_True(t *testing.T) {
	c := &PrepareContext{LocalPath: "/tmp/x.mp4", StorageURI: ""}
	assert.True(t, c.HasLocal())
}

func TestPrepareContext_HasLocal_BothPopulated_False(t *testing.T) {
	c := &PrepareContext{LocalPath: "/tmp/x.mp4", StorageURI: "drive://abc"}
	assert.False(t, c.HasLocal(), "local+URI mixed = remote-only staging (forward-pointer for DriveStager / S3Stager)")
}

func TestPrepareContext_HasLocal_NilPointer_False(t *testing.T) {
	var c *PrepareContext
	assert.False(t, c.HasLocal())
}

func TestPrepareContext_Expired_FutureExpiry_False(t *testing.T) {
	c := &PrepareContext{ExpiresAt: time.Now().UTC().Add(1 * time.Hour)}
	assert.False(t, c.Expired())
}

func TestPrepareContext_Expired_PastExpiry_True(t *testing.T) {
	c := &PrepareContext{ExpiresAt: time.Now().UTC().Add(-1 * time.Minute)}
	assert.True(t, c.Expired())
}

func TestPrepareContext_Expired_NilPointer_True(t *testing.T) {
	var c *PrepareContext
	assert.True(t, c.Expired(), "nil-pointer short-circuits to 'expired' so caller-side guards don't panic")
}

// ── DeriveIdempotencyKey: byte-stability across repeated calls ────────

func TestDeriveIdempotencyKey_StableAcrossCalls(t *testing.T) {
	ref := SourceRef{
		URL:               "https://example.com/x.mp4",
		DownloadSection:   "*00:01:00-00:01:30",
		SuggestedFilename: "intro.mp4",
		PolicyVersion:     "v1",
	}
	k1 := DeriveIdempotencyKey(ref)
	k2 := DeriveIdempotencyKey(ref)
	require.Equal(t, k1, k2, "IdempotencyKey derivation MUST be byte-stable across calls (idempotency invariant)")
	assert.Len(t, k1, 64, "SHA-256 hex is 64 chars")
}

func TestDeriveIdempotencyKey_DifferentInputs_DifferentKeys(t *testing.T) {
	a := DeriveIdempotencyKey(SourceRef{URL: "https://a/"})
	b := DeriveIdempotencyKey(SourceRef{URL: "https://b/"})
	assert.NotEqual(t, a, b, "URL differences → different keys (no collision)")
}

func TestDeriveIdempotencyKey_PolicyVersionDifference_DifferentKeys(t *testing.T) {
	a := DeriveIdempotencyKey(SourceRef{URL: "https://x/", PolicyVersion: "v1"})
	b := DeriveIdempotencyKey(SourceRef{URL: "https://x/", PolicyVersion: "v2"})
	assert.NotEqual(t, a, b, "PolicyVersion difference → different keys (forward-pointer: policy bumps force a fresh cache)")
}

// ── DeriveCleanupToken: distinct from IdempotencyKey ─────────────────

func TestDeriveCleanupToken_DistinctFromIdempotencyKey(t *testing.T) {
	ref := SourceRef{URL: "https://example.com/x.mp4", PolicyVersion: "v1"}
	idempKey := DeriveIdempotencyKey(ref)
	cleanupToken := DeriveCleanupToken(ref)
	assert.NotEqual(t, idempKey, cleanupToken, "CleanUpToken + IdempotencyKey MUST differ (different namespace prefix prevents collision)")
}

func TestDeriveCleanupToken_StableAcrossCalls(t *testing.T) {
	ref := SourceRef{URL: "https://example.com/x.mp4", PolicyVersion: "v1"}
	t1 := DeriveCleanupToken(ref)
	t2 := DeriveCleanupToken(ref)
	require.Equal(t, t1, t2)
}

// ── DeriveStageID: filename sanitisation ─────────────────────────────

func TestDeriveStageID_StripsPathSeparators(t *testing.T) {
	ref := SourceRef{URL: "/tmp/foo/bar/x.mp4"}
	id := DeriveStageID(ref)
	assert.NotContains(t, id, "/", "ID MUST NOT contain path separators (filesystem layout safety)")
	assert.NotContains(t, id, "\\")
}

func TestDeriveStageID_EmptyFallsBackToStage(t *testing.T) {
	ref := SourceRef{URL: ""}
	id := DeriveStageID(ref)
	assert.Equal(t, "stage", id, "empty URL → 'stage' fallback")
}

func TestDeriveStageID_DownloadSectionAppendsSuffix(t *testing.T) {
	basic := DeriveStageID(SourceRef{URL: "https://x/y.mp4"})
	withSection := DeriveStageID(SourceRef{URL: "https://x/y.mp4", DownloadSection: "*00:01:20-00:01:35"})
	assert.Contains(t, withSection, basic, "section form contains the basic ID as prefix")
	assert.NotEqual(t, basic, withSection, "section must produce a distinct ID")
}

// ── safeBaseName (private helper) ────────────────────────────────────

func TestSafeBaseName_StripsDangerousChars(t *testing.T) {
	cases := map[string]string{
		"abc/def\\ghi":     "abc_def_ghi",
		"a:b*c?d\"e<f>g|h": "a_b_c_d_e_f_g_h",
		"hello world":      "hello_world",
		"normal-name.mp4":  "normal-name.mp4",
		"":                 "",
	}
	for input, want := range cases {
		assert.Equal(t, want, safeBaseName(input), "input=%q", input)
	}
}

func TestSafeBaseName_CollapsesUnderRuns(t *testing.T) {
	in := "a/_/_/b"
	out := safeBaseName(in)
	assert.False(t, strings.Contains(out, "__"), "consecutive separators collapse to a single '_'")
}

// ── SourceStager port contract via FakeSourceStager ──────────────────

func TestSourceStager_Prepare_HappyPath_RecordsCallArguments(t *testing.T) {
	receipt := &PrepareContext{
		ID:           "x-1",
		SourceRef:    SourceRef{URL: "https://example.com/x.mp4"},
		LocalPath:    "/tmp/x.mp4",
		SHA256:       "abc",
		CleanupToken: "ct-1",
	}
	f := &fakeSourceStager{receipts: []*PrepareContext{receipt}}

	req := PrepareRequest{
		Source:         SourceRef{URL: "https://example.com/x.mp4"},
		IdempotencyKey: "k-1",
		Timeout:        30 * time.Second,
	}
	got, err := f.Prepare(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "x-1", got.ID)
	assert.Equal(t, 1, f.prepareCalls)
	assert.Equal(t, "https://example.com/x.mp4", f.lastPrepareRequest.Source.URL)
}

func TestSourceStager_Prepare_AcquisitionFailure_ReturnsPrepareFailedSentinel(t *testing.T) {
	f := &fakeSourceStager{} // no receipts → ErrAcquisitionPrepareFailed
	req := PrepareRequest{Source: SourceRef{URL: "https://x/"}, IdempotencyKey: "k-1"}
	_, err := f.Prepare(context.Background(), req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAcquisitionPrepareFailed))
}

func TestSourceStager_Release_RecordsToken(t *testing.T) {
	f := &fakeSourceStager{}
	require.NoError(t, f.Release(context.Background(), "ct-1"))
	assert.Equal(t, 1, f.releaseCalls)
	assert.Equal(t, "ct-1", f.lastReleaseToken)
}

// ── Resolved sentinels are distinct (godlike/07 sanity) ──────────────

func TestSentinels_AllDistinct(t *testing.T) {
	all := []error{
		ErrAcquisitionNotWired,
		ErrAcquisitionPrepareFailed,
		ErrAcquisitionInvalidRequest,
		ErrAcquisitionAlreadyReleased,
		ErrAcquisitionInvalidToken,
		ErrAcquisitionExpired,
	}
	seen := make(map[string]bool, len(all))
	for _, e := range all {
		msg := e.Error()
		require.False(t, seen[msg], "sentinels MUST be distinct (collision breaks errors.Is): %q", msg)
		seen[msg] = true
	}
}

func TestWrap_PreservesSentinel(t *testing.T) {
	detail := "URL was empty"
	wrapped := Wrap(ErrAcquisitionInvalidRequest, detail)
	require.True(t, errors.Is(wrapped, ErrAcquisitionInvalidRequest), "wrap MUST preserve errors.Is identity")
	assert.Contains(t, wrapped.Error(), detail, "wrap MUST embed the detail message")
}

func TestWrap_EmptyDetail_ReturnsSentinelVerbatim(t *testing.T) {
	got := Wrap(ErrAcquisitionNotWired, "")
	require.Same(t, ErrAcquisitionNotWired, got, "empty detail → sentinel returned verbatim (no fmt.Errorf allocation)")
}

func TestWrap_NilSentinel_ReturnsNil(t *testing.T) {
	assert.Nil(t, Wrap(nil, "context"))
}
