package staged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// mockIndexStore is the test fixture IndexStore implementation. The
// resolver_test.go tests inject path behavior via getLocalPathFunc
// so the suite is hermetic (no SQLite / no sqlmock dependency —
// Pattern 0 unit testability).
type mockIndexStore struct {
	getLocalPathFunc func(ctx context.Context, assetID string) (string, error)
}

func (m *mockIndexStore) GetLocalPath(_ context.Context, assetID string) (string, error) {
	return m.getLocalPathFunc(context.Background(), assetID)
}

// newMockStore is a convenience constructor reducing bolierplate.
func newMockStore(fn func(ctx context.Context, assetID string) (string, error)) *mockIndexStore {
	return &mockIndexStore{getLocalPathFunc: fn}
}

// Compile-time pin (godlike/06 SSOT discipline): the test fixture
// must keep the same surface as the production binding. Drift in
// the IndexStore 1-method signature surfaces at build failure
// rather than runtime panic.
var _ IndexStore = (*mockIndexStore)(nil)

// TestResolve_StagedArtifact_HappyPath pins the canonical wire
// contract:
//
//	asset_index row -> local_path -> os.Stat -> files.HashFile
//	-> StagedArtifact{AssetID, LocalPath, SHA256, SizeBytes}
//
// SHA256 byte-stability is the load-bearing assertion: the
// resolver must compute the same SHA256 as files.SHA256Bytes
// against the same payload — proving the recompute is byte-
// stable + correct, NOT pulling a stale hash from the DB's
// content_hash column.
func TestResolve_StagedArtifact_HappyPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cosmos.png")
	payload := []byte("the-human-fascination-with-the-cosmos\n")
	if err := os.WriteFile(file, payload, 0o644); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	const wantAssetID = "asset-cosmos"
	store := newMockStore(func(_ context.Context, assetID string) (string, error) {
		if assetID != wantAssetID {
			return "", nil
		}
		return file, nil
	})
	r, err := NewResolver(store)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	got, err := r.ResolveStagedArtifact(context.Background(), wantAssetID)
	if err != nil {
		t.Fatalf("ResolveStagedArtifact: %v", err)
	}
	if got.AssetID != wantAssetID {
		t.Errorf("AssetID: got %q want %q", got.AssetID, wantAssetID)
	}
	if got.LocalPath != file {
		t.Errorf("LocalPath: got %q want %q", got.LocalPath, file)
	}
	wantSHA := files.SHA256Bytes(payload)
	if got.SHA256 != wantSHA {
		t.Errorf("SHA256: got %q want %q (stale recompute pulled from DB?)", got.SHA256, wantSHA)
	}
	if got.SizeBytes != int64(len(payload)) {
		t.Errorf("SizeBytes: got %d want %d", got.SizeBytes, len(payload))
	}

	// nil ctor store -> ErrStagedArtifactMissing via ctor (fail-closed at
	// composition time).
	if _, err := NewResolver(nil); err == nil {
		t.Errorf("nil IndexStore to NewResolver should error at composition time (godlike/05 wiring-error rule)")
	}
}

// TestResolve_StagedArtifact_NotFound pins the typed-sentinel
// branch across all 3 failure paths the Resolver can take:
//
//	(a) IndexStore returns ("", nil) — DB row absent.
//	(b) IndexStore returns ("", err) — DB underlying error surfaced.
//	(c) IndexStore returns a path, but os.Stat fails — TTL GC swept
//	    the file or path was moved after the DB write.
//
// All three branches MUST surface the canonical sentinel
// (errors.Is(err, ErrStagedArtifactMissing)) AND return a nil
// StagedArtifact (godlike/07 no-fake-availability: never return
// a struct with empty fields).
func TestResolve_StagedArtifact_NotFound(t *testing.T) {
	t.Run("db-miss empty-path", func(t *testing.T) {
		store := newMockStore(func(_ context.Context, _ string) (string, error) {
			return "", nil
		})
		r, err := NewResolver(store)
		if err != nil {
			t.Fatalf("NewResolver: %v", err)
		}
		got, err := r.ResolveStagedArtifact(context.Background(), "asset-x")
		if err == nil {
			t.Fatal("expected error on DB miss, got nil")
		}
		if got != nil {
			t.Errorf("expected nil StagedArtifact on failure, got %+v", got)
		}
		if !errors.Is(err, ErrStagedArtifactMissing) {
			t.Errorf("must wrap ErrStagedArtifactMissing via errors.Is; got %v", err)
		}
	})

	t.Run("db-underlying-error", func(t *testing.T) {
		store := newMockStore(func(_ context.Context, _ string) (string, error) {
			return "", errors.New("simulated: sql: no rows in result set")
		})
		r, _ := NewResolver(store)
		got, err := r.ResolveStagedArtifact(context.Background(), "asset-x")
		if err == nil {
			t.Fatal("expected error on DB error, got nil")
		}
		if got != nil {
			t.Errorf("expected nil StagedArtifact on failure, got %+v", got)
		}
		if !errors.Is(err, ErrStagedArtifactMissing) {
			t.Errorf("must wrap ErrStagedArtifactMissing via errors.Is; got %v", err)
		}
	})

	t.Run("stale-db-row-file-missing", func(t *testing.T) {
		// Path that will never exist (TTL GC swept or worker upload
		// not yet finished). os.Stat will fail with ErrNotExist.
		missing := filepath.Join(t.TempDir(), "ttl-swept.png")
		store := newMockStore(func(_ context.Context, _ string) (string, error) {
			return missing, nil
		})
		r, _ := NewResolver(store)
		got, err := r.ResolveStagedArtifact(context.Background(), "asset-stale")
		if err == nil {
			t.Fatal("expected error on missing file, got nil")
		}
		if got != nil {
			t.Errorf("expected nil StagedArtifact on missing file, got %+v", got)
		}
		if !errors.Is(err, ErrStagedArtifactMissing) {
			t.Errorf("must wrap ErrStagedArtifactMissing on missing file; got %v", err)
		}
	})
}

// TestResolve_StagedArtifact_Idempotency pins the canonical
// idempotency contract:
//
//	two consecutive calls for the same assetID + same on-disk bytes
//	MUST return byte-equivalent StagedArtifacts (AssetID, LocalPath,
//	SHA256, SizeBytes all equal across calls).
//
// PLUS the drift-detection invariant (the load-bearing corollary
// of "recompute on every call"): a mutation of the local file
// between calls MUST make the SHA256 field differ on the second
// call. If recompute were a NOOP (e.g. cached stale hash), the
// drftest would silently produce the SAME SHA across both calls
// — masking a TTL GC sweep or a parallel-write overwrite.
//
// Together these two halves pin the resolver's load-bearing
// claim: the resolver NEVER publishes a payload it has not
// actually re-derived from disk.
func TestResolve_StagedArtifact_Idempotency(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "idempotent.png")
	originalPayload := []byte("first payload\n")
	if err := os.WriteFile(file, originalPayload, 0o644); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	store := newMockStore(func(_ context.Context, _ string) (string, error) {
		return file, nil
	})
	r, err := NewResolver(store)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	const assetID = "asset-idempotent"

	// First call.
	first, err := r.ResolveStagedArtifact(context.Background(), assetID)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Second call (no file mutation between): byte-equivalent.
	second, err := r.ResolveStagedArtifact(context.Background(), assetID)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first.AssetID != second.AssetID {
		t.Errorf("AssetID drift across 2 calls: %q vs %q", first.AssetID, second.AssetID)
	}
	if first.LocalPath != second.LocalPath {
		t.Errorf("LocalPath drift across 2 calls: %q vs %q", first.LocalPath, second.LocalPath)
	}
	if first.SHA256 != second.SHA256 {
		t.Errorf("SHA256 drift across 2 calls (recompute broken?): %q vs %q", first.SHA256, second.SHA256)
	}
	if first.SizeBytes != second.SizeBytes {
		t.Errorf("SizeBytes drift across 2 calls: %d vs %d", first.SizeBytes, second.SizeBytes)
	}

	// Drift detection: mutate the file, expect SHA256 to differ on
	// the third call. This is the silent-corruption-detector half
	// of the resolver contract.
	mutatedPayload := []byte("mutated payload content - sentinel rewrite for drift detection\n")
	if err := os.WriteFile(file, mutatedPayload, 0o644); err != nil {
		t.Fatalf("mutate write: %v", err)
	}
	third, err := r.ResolveStagedArtifact(context.Background(), assetID)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if third.SHA256 == first.SHA256 {
		t.Error("SHA256 did NOT drift on file content change — resolver must recompute on every call to detect silent corruption; got identical hash twice")
	}
	wantMutatedSHA := files.SHA256Bytes(mutatedPayload)
	if third.SHA256 != wantMutatedSHA {
		t.Errorf("SHA256 after mutation: got %q want %q", third.SHA256, wantMutatedSHA)
	}
	if third.SizeBytes != int64(len(mutatedPayload)) {
		t.Errorf("SizeBytes after mutation: got %d want %d", third.SizeBytes, len(mutatedPayload))
	}
}
