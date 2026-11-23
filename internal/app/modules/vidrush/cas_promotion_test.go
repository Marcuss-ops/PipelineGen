package vidrush

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	infraartifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
)

func newTestCASStore(t *testing.T) *cas.Store {
	t.Helper()
	root := t.TempDir()
	stager, err := infraartifacts.NewLocalStore(infraartifacts.Config{Workspace: filepath.Join(root, ".staging")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cas.NewStore(cas.Config{Root: root, Stager: stager})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// TestPromoteArtifactToCASReturnsContentAddress pins the durability contract:
// the finalized bytes land under their own hash, so the ephemeral download can
// be removed and a later run can re-open the bytes by content address.
func TestPromoteArtifactToCASReturnsContentAddress(t *testing.T) {
	store := newTestCASStore(t)
	source := filepath.Join(t.TempDir(), "vidrush-image-1")
	if err := os.WriteFile(source, []byte("image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	object, err := store.Put(context.Background(), file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}

	finalizer := &vidRushArtifactFinalizer{cas: store, log: zap.NewNop()}
	path := finalizer.promoteArtifactToCAS(context.Background(), source, object.SHA256)
	if !strings.HasSuffix(path, object.SHA256) {
		t.Fatalf("promoted path = %q, want the content address %q", path, object.SHA256)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("CAS object missing: %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("promotion must not delete the source; the caller owns cleanup: %v", err)
	}
}

// TestPromoteArtifactToCASKeepsTheSourcePathWhenUnavailable proves the
// degraded mode: no CAS store or a missing file keeps the original path, never
// an empty one.
func TestPromoteArtifactToCASKeepsTheSourcePathWhenUnavailable(t *testing.T) {
	finalizer := &vidRushArtifactFinalizer{log: zap.NewNop()}
	const source = "/tmp/vidrush-image-missing"
	if got := finalizer.promoteArtifactToCAS(context.Background(), source, "abc"); got != source {
		t.Fatalf("path = %q, want the original %q", got, source)
	}
	finalizer.cas = newTestCASStore(t)
	if got := finalizer.promoteArtifactToCAS(context.Background(), filepath.Join(t.TempDir(), "absent"), "abc"); got == "" {
		t.Fatal("a missing source must keep a usable path")
	}
}
