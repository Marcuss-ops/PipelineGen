package animreg

import (
	"strings"
	"testing"
)

// P0-3: il catalog digest è versionato, stabile e non vuoto.
func TestCatalogDigestVersioned(t *testing.T) {
	a, b := CatalogDigest(), CatalogDigest()
	if a == "" || a != b {
		t.Fatalf("digest must be stable non-empty, got %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "sha256:") || !strings.HasSuffix(a, ":v1") {
		t.Fatalf("digest must be sha256:...:v1, got %q", a)
	}
}
