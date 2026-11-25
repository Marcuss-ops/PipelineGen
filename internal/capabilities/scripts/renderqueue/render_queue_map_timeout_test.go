package renderqueue

import (
	"testing"
	"time"
)

func TestDynamicMapRenderTimeoutReadsEnv(t *testing.T) {
	t.Setenv("PIPELINEGEN_DYNAMIC_MAP_TIMEOUT", "45")
	if got := dynamicMapRenderTimeout(); got != 45*time.Second {
		t.Fatalf("timeout = %s, want 45s", got)
	}
	t.Setenv("PIPELINEGEN_DYNAMIC_MAP_TIMEOUT", "1m30s")
	if got := dynamicMapRenderTimeout(); got != 90*time.Second {
		t.Fatalf("duration timeout = %s, want 90s", got)
	}
}

func TestDynamicMapRenderTimeoutFallsBackOnInvalidValue(t *testing.T) {
	for _, value := range []string{"", "0", "-3", "not-a-number"} {
		t.Setenv("PIPELINEGEN_DYNAMIC_MAP_TIMEOUT", value)
		if got := dynamicMapRenderTimeout(); got <= 0 {
			t.Fatalf("timeout for %q = %s, want the positive default", value, got)
		}
	}
}
