package clipswiring

import "testing"

func TestClipRenderChunkingIsOptIn(t *testing.T) {
	t.Setenv(EnvClipRenderChunking, "")
	if ClipRenderChunkingEnabled() {
		t.Fatal("chunking must be OFF by default: the measured policy is NO-GO")
	}
	for _, raw := range []string{"0", "false", "no", "off"} {
		t.Setenv(EnvClipRenderChunking, raw)
		if ClipRenderChunkingEnabled() {
			t.Fatalf("chunking enabled by %q; only explicit 1/true may arm it", raw)
		}
	}
	for _, raw := range []string{"1", "true", " TRUE "} {
		t.Setenv(EnvClipRenderChunking, raw)
		if !ClipRenderChunkingEnabled() {
			t.Fatalf("chunking not enabled by explicit %q", raw)
		}
	}
}
