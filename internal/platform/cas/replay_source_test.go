package cas

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	capreplay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/replay"
)

func newReplaySource(t *testing.T) (*ReplayAssetSource, *Store) {
	t.Helper()
	store, _, _ := newTestStore(t)
	source, err := NewReplayAssetSource(store, filepath.Join(t.TempDir(), "staging"))
	if err != nil {
		t.Fatal(err)
	}
	return source, store
}

func TestMaterializeRoundTrip(t *testing.T) {
	source, store := newReplaySource(t)
	ctx := context.Background()
	payload := []byte("replay asset bytes")
	obj, err := store.Put(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}

	got, err := source.Materialize(ctx, capreplay.ReplayAsset{
		AssetID: "clip-a", SHA256: obj.SHA256, CASURI: capreplay.CanonicalCASURI(obj.SHA256), SizeBytes: obj.SizeBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.AssetID != "clip-a" || got.SHA256 != obj.SHA256 || got.SizeBytes != obj.SizeBytes {
		t.Fatalf("materialized asset mismatch: %+v", got)
	}
	data, err := os.ReadFile(got.LocalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("materialized bytes = %q, want %q", data, payload)
	}
}

func TestMaterializeVerifiesCorruption(t *testing.T) {
	source, store := newReplaySource(t)
	ctx := context.Background()
	obj, err := store.Put(ctx, bytes.NewReader([]byte("pristine bytes")))
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the object on disk.
	target, err := store.LocalPath(obj.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := source.Materialize(ctx, capreplay.ReplayAsset{AssetID: "clip-a", SHA256: obj.SHA256, CASURI: capreplay.CanonicalCASURI(obj.SHA256)}); !errors.Is(err, ErrCorruption) {
		t.Fatalf("materialize of corrupt object: want ErrCorruption, got %v", err)
	}
}

func TestMaterializeSizeMismatch(t *testing.T) {
	source, store := newReplaySource(t)
	ctx := context.Background()
	obj, err := store.Put(ctx, bytes.NewReader([]byte("exact bytes")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Materialize(ctx, capreplay.ReplayAsset{AssetID: "clip-a", SHA256: obj.SHA256, CASURI: capreplay.CanonicalCASURI(obj.SHA256), SizeBytes: obj.SizeBytes + 10}); !errors.Is(err, ErrCorruption) {
		t.Fatalf("materialize with wrong recorded size: want ErrCorruption, got %v", err)
	}
}

func TestMaterializeMissingObject(t *testing.T) {
	source, _ := newReplaySource(t)
	ctx := context.Background()
	missing := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := source.Materialize(ctx, capreplay.ReplayAsset{AssetID: "clip-a", SHA256: missing, CASURI: "cas://" + missing}); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("materialize missing object: want ErrObjectNotFound, got %v", err)
	}
}

func TestNewReplayAssetSourceValidation(t *testing.T) {
	if _, err := NewReplayAssetSource(nil, t.TempDir()); err == nil {
		t.Fatal("nil store must be rejected")
	}
	store, _, _ := newTestStore(t)
	if _, err := NewReplayAssetSource(store, ""); err == nil {
		t.Fatal("empty staging dir must be rejected")
	}
}

var _ capreplay.AssetSource = (*ReplayAssetSource)(nil)
