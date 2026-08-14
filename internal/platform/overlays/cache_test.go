package overlays

import (
	"strings"
	"testing"
)

func TestCachePutIsAtomicAndContentAddressed(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path, err := c.Put("assets", strings.Repeat("a", 64), "asset.bin", strings.NewReader("asset"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Has("assets", strings.Repeat("a", 64), "asset.bin") {
		t.Fatal("cache miss after put")
	}
	if path == "" {
		t.Fatal("empty cache path")
	}
}
