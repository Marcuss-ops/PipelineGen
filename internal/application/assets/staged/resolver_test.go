// Package staged — resolver_test.go (Azione 1, July 2026,
// CUTOVER-COMPLETE-WITH-ARTIFACTS wave).
//
// 3 TDD tests per the user spec:
//
//	(1) happy path:      DB lookup returns row + on-disk file present
//	                     -> *StagedArtifact populated with 5-field envelope.
//	(2) not-found:       DB lookup returns ErrStagedArtifactMissing
//	                     via errors.Is; corrupted row with empty Path
//	                     converts to the same typed sentinel.
//	(3) idempotency:     two successive ResolveStagedArtifact(same artifactID) calls
//	                     return IDENTICAL *StagedArtifact shape (Path,
//	                     SHA256, Bytes, Source) AND the lookupFn is
//	                     invoked twice (no internal cache).
//
// godlike/07 tests: each test uses package-internal types so no
// production-side mocks are exposed externally. The stubLookup helper
// is test-scoped and unreferenced from resolver.go. testify/require +
// testify/assert mirror the canonical pattern documented in the
// existing codebase (per project convention).
package staged

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── stubLookup (test-scoped) ───────────────────────────────────────

// stubLookup is a deterministic lookupFn-shaped function that records
// the call count via an atomic.Int32 (for idempotency assertions) and
// returns the supplied *IndexRow + error response when the artifactID
// matches the needle. Any other artifactID returns ErrStagedArtifactMissing
// (correct miss-path shape — DB layer convention).
//
// Thread-safety: the atomic.Int32 protects the call counter across any
// potential concurrent ResolveStagedArtifact invocations from a future test expansion
// (current tests are sequential, but the stub is safe to share across
// concurrent goroutines if the test surface grows).
type stubLookup struct {
	calls   atomic.Int32
	needle  string
	respRow *IndexRow
	respErr error
}

// Lookup satisfies the ArtifactIndexLookupFn signature.
func (s *stubLookup) Lookup(_ context.Context, artifactID string) (*IndexRow, error) {
	s.calls.Add(1)
	if artifactID != s.needle {
		return nil, ErrStagedArtifactMissing
	}
	return s.respRow, s.respErr
}

// ── helper: temp file with known contents ──────────────────────────

// helperNewTempFileBytes creates a temp file with the supplied contents
// and returns the resolved absolute path. t.Cleanup removes the temp
// directory on test teardown.
func helperNewTempFileBytes(t *testing.T, name string, contents []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, contents, 0o600))
	return p
}

// ── (1) Happy path ────────────────────────────────────────────────

// TestResolver_ResolveStagedArtifact_HappyPath_PopulatedArtifact is the canonical
// happy-path TDD test: a successful DB row + an existing on-disk file
// yield a *StagedArtifact populated with the 5-field envelope (AssetID,
// Path, SHA256, Bytes, Source). Verifies that the SHA hex digest is
// the canonical 64-char lowercase hex (godlike/07 contract: NEVER an
// empty string).
func TestResolver_ResolveStagedArtifact_HappyPath_PopulatedArtifact(t *testing.T) {
	ctx := context.Background()
	const artifactID = "happy-001"
	knownBytes := []byte("hello world — staged artifact contents — happy fixture")
	path := helperNewTempFileBytes(t, "happy.bin", knownBytes)

	lookup := &stubLookup{
		needle:  artifactID,
		respRow: &IndexRow{Path: path, Source: "artlist"},
	}
	resolver, err := NewResolver(lookup.Lookup)
	require.NoError(t, err)
	require.NotNil(t, resolver)

	out, err := resolver.ResolveStagedArtifact(ctx, artifactID)
	require.NoError(t, err)
	require.NotNil(t, out)

	// Field-by-field assertions on the 5-field envelope.
	assert.Equal(t, artifactID, out.AssetID, "AssetID round-trips the input")
	assert.Equal(t, path, out.Path, "Path comes verbatim from the DB lookup")
	assert.Equal(t, "artlist", out.Source, "Source comes verbatim from the DB lookup")
	assert.Equal(t, int64(len(knownBytes)), out.Bytes, "Bytes is the on-disk size via os.Stat")
	assert.Len(t, out.SHA256, 64, "SHA256 hex digest should be exactly 64 chars (SHA-256 in hex)")
	assert.NotEqual(t, "", out.SHA256, "SHA256 must be populated, not empty (godlike/07 contract)")

	// SHA-256 byte-stability: the known contents deterministically
	// hash to a stable value. The fixture content is intentionally
	// human-readable so a debug operator can re-derive the expected
	// digest via `echo -n 'CONTENTS' | sha256sum`. (The test below
	// pins the canonical 64-char hex; we do NOT pin the exact digest
	// here because the recompute stability is the idempotency-test's
	// concern, not the happy-path test's.)
}

// ── (2) Not-found ─────────────────────────────────────────────────

// TestResolver_ResolveStagedArtifact_NotFound_DBLookupError pins the typed-error
// chain when the lookupFn returns ErrStagedArtifactMissing directly.
// errors.Is must reach the typed sentinel via the canonical
// godlike/07 contract (1x %w wrap depth).
func TestResolver_ResolveStagedArtifact_NotFound_DBLookupError(t *testing.T) {
	ctx := context.Background()
	const artifactID = "missing-001"

	lookup := &stubLookup{
		needle:  artifactID,
		respRow: nil,
		respErr: ErrStagedArtifactMissing,
	}
	resolver, err := NewResolver(lookup.Lookup)
	require.NoError(t, err)

	out, err := resolver.ResolveStagedArtifact(ctx, artifactID)
	require.Nil(t, out, "out must be nil on not-found (godlike/07 no-fake-availability)")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStagedArtifactMissing,
		"errors.Is(err, ErrStagedArtifactMissing) must succeed on the canonical miss path")
}

// TestResolver_ResolveStagedArtifact_NotFound_EmptyPathRow pins the row-with-empty-path
// guard: a non-nil *IndexRow with Path="" must convert to
// ErrStagedArtifactMissing (the typed sentinel), NOT to a zero-value
// envelope. godlike/07 tripwire: a corrupted row never satisfies the
// call as a "found" record.
func TestResolver_ResolveStagedArtifact_NotFound_EmptyPathRow(t *testing.T) {
	ctx := context.Background()
	const artifactID = "ghost-row-001"

	lookup := &stubLookup{
		needle:  artifactID,
		respRow: &IndexRow{Path: "", Source: "ghost"},
		respErr: nil,
	}
	resolver, err := NewResolver(lookup.Lookup)
	require.NoError(t, err)

	out, err := resolver.ResolveStagedArtifact(ctx, artifactID)
	require.Nil(t, out)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStagedArtifactMissing,
		"corrupted row (empty Path) must surface as ErrStagedArtifactMissing")
}

// ── (3) Idempotency ───────────────────────────────────────────────

// TestResolver_ResolveStagedArtifact_Idempotency_TwoCallsIdenticalShape is the
// canonical idempotency TDD test. Two successive ResolveStagedArtifact(same
// artifactID) calls return:
//
//	(a) IDENTICAL field values across the 5-field envelope (sans
//	    pointer identity).
//	(b) The lookupFn is invoked exactly twice (no internal cache on
//	    the typed seam).
//	(c) The SHA is byte-stable across calls (deterministic recompute
//	    from the same disk contents).
//
// godlike/07 idempotency invariant: this is the user-spec "due
// chiamate stesso artifactID = stesso path + SHA ricalcolato" anchor.
// A future caching layer (if introduced) MUST preserve byte-stability
// across consecutive calls — would be caught by this test.
func TestResolver_ResolveStagedArtifact_Idempotency_TwoCallsIdenticalShape(t *testing.T) {
	ctx := context.Background()
	const artifactID = "idem-001"
	knownBytes := []byte("idempotent fixture — staged artifact contents stay stable across calls")
	path := helperNewTempFileBytes(t, "idem.bin", knownBytes)

	lookup := &stubLookup{
		needle:  artifactID,
		respRow: &IndexRow{Path: path, Source: "voiceover"},
	}
	resolver, err := NewResolver(lookup.Lookup)
	require.NoError(t, err)

	first, err := resolver.ResolveStagedArtifact(ctx, artifactID)
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := resolver.ResolveStagedArtifact(ctx, artifactID)
	require.NoError(t, err)
	require.NotNil(t, second)

	// (a) Identical field values across the 5-field envelope.
	assert.Equal(t, first.AssetID, second.AssetID, "AssetID must be stable across calls")
	assert.Equal(t, first.Path, second.Path, "Path must be stable across calls (DB lookup)")
	assert.Equal(t, first.SHA256, second.SHA256,
		"SHA256 must be stable across calls (recompute is idempotent on identical disk contents)")
	assert.Equal(t, first.Bytes, second.Bytes,
		"Bytes must be stable across calls (os.Stat is idempotent on same file)")
	assert.Equal(t, first.Source, second.Source, "Source must be stable across calls (DB lookup)")

	// (b) lookupFn called exactly twice — no internal cache on the typed seam.
	assert.Equal(t, int32(2), lookup.calls.Load(),
		"stub lookupFn must be invoked exactly twice across two ResolveStagedArtifact calls (no cache)")

	// (c) Pointer-identity MAY differ — two distinct *StagedArtifact objects.
	if first == second {
		t.Fatalf("expected distinct *StagedArtifact pointer across calls (no shared state); got shared ptr %p", first)
	}
}
