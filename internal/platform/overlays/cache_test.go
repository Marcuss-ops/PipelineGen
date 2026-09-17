package overlays

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func addressOf(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
}

// TestCachePutIsAtomicAndContentAddressed pins the happy path: bytes written
// under their own address are readable at that address.
//
// The previous version of this test wrote the bytes "asset" under the address
// "aaaa…", which does not name them at all, and then asserted the entry was
// present. That is the bug this cache is supposed to make impossible, encoded
// as an expectation — so the fixture now uses the real digest.
func TestCachePutIsAtomicAndContentAddressed(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const content = "asset"
	address := addressOf(content)

	path, err := c.Put("assets", address, "asset.bin", strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("empty cache path")
	}
	if !c.Has("assets", address, "asset.bin") {
		t.Fatal("cache miss after put")
	}
}

// TestCacheHasRejectsBytesThatDoNotMatchTheirAddress is the regression this
// change exists for: a file that is present but does not hash to its name must
// read as ABSENT, so no caller can skip materialization and render it.
func TestCacheHasRejectsBytesThatDoNotMatchTheirAddress(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Right address, wrong bytes: exactly what a truncated download or a
	// replaced file looks like on disk.
	address := addressOf("the real bytes")
	if _, err := c.Put("assets", address, "asset.bin", strings.NewReader("something else")); err != nil {
		t.Fatal(err)
	}
	if c.Has("assets", address, "asset.bin") {
		t.Fatal("Has = true for bytes that do not match their content address")
	}
	if _, err := os.Stat(c.Path("assets", address, "asset.bin")); err != nil {
		t.Fatalf("test fixture did not create the mismatched entry: %v", err)
	}
}

// TestCacheHasTreatsZeroByteEntryAsAbsent covers the shape a crashed install
// leaves behind: the file exists and has the right name, but holds nothing.
func TestCacheHasTreatsZeroByteEntryAsAbsent(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	address := addressOf("")
	if _, err := c.Put("assets", address, "asset.bin", strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if c.Has("assets", address, "asset.bin") {
		t.Fatal("Has = true for an empty content-addressed entry")
	}
}

// TestCacheHasKeepsNonAssetNamespacesAsPresenceProbes pins the deliberately
// weaker contract for namespaces whose keys are not content addresses: there is
// nothing to verify them against, so presence is the honest answer.
func TestCacheHasKeepsNonAssetNamespacesAsPresenceProbes(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Put("thumbnails", "job-1", "preview.png", strings.NewReader("bytes")); err != nil {
		t.Fatal(err)
	}
	if !c.Has("thumbnails", "job-1", "preview.png") {
		t.Fatal("Has = false for a present non-content-addressed entry")
	}
}
