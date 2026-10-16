package overlay

import "testing"

func TestResolveMapBackend(t *testing.T) {
	if got := ResolveMapBackend(false, false); got != MapBackendNative {
		t.Fatalf("plain geo → native, got %q", got)
	}
	if got := ResolveMapBackend(true, false); got != MapBackendPython {
		t.Fatalf("camera → python, got %q", got)
	}
	if got := ResolveMapBackend(false, true); got != MapBackendPython {
		t.Fatalf("label/composition → python, got %q", got)
	}
}
