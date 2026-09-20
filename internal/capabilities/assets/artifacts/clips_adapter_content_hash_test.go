package artifacts

import (
	"context"
	"testing"
)

// TestClipsRegistryFindByContentHashFailsClosedWithoutDatabase pins MEDIA-SSOT:
// the content lookup resolves from the canonical media committer's engine only.
// With no canonical committer wired (media SSOT closed) it must error rather
// than degrade onto the operational SQLite mirror, which holds no committed
// media rows.
func TestClipsRegistryFindByContentHashFailsClosedWithoutDatabase(t *testing.T) {
	registry := NewClipsRegistry(nil, nil, nil)
	if _, err := registry.FindByContentHash(context.Background(), "some-sha256"); err == nil {
		t.Fatal("FindByContentHash succeeded with no content lookup database")
	}
	if _, err := (*ClipsRegistry)(nil).FindByContentHash(context.Background(), "some-sha256"); err == nil {
		t.Fatal("FindByContentHash succeeded on a nil registry")
	}
}
