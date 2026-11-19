package rustexec

import (
	"reflect"
	"testing"
)

func TestSemanticCacheEnvironmentIsOptIn(t *testing.T) {
	t.Setenv("SEMANTIC_CACHE_DIR", "")
	t.Setenv("SEMANTIC_CACHE_MAX_BYTES", "")
	if got := semanticCacheEnvironment(); len(got) != 0 {
		t.Fatalf("unset cache variables must forward nothing, got %v", got)
	}

	t.Setenv("SEMANTIC_CACHE_DIR", "/tmp/semantic-cache")
	t.Setenv("SEMANTIC_CACHE_MAX_BYTES", "1048576")
	want := []string{
		"SEMANTIC_CACHE_DIR=/tmp/semantic-cache",
		"SEMANTIC_CACHE_MAX_BYTES=1048576",
	}
	if got := semanticCacheEnvironment(); !reflect.DeepEqual(got, want) {
		t.Fatalf("semanticCacheEnvironment() = %v, want %v", got, want)
	}
}
