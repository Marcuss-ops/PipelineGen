package audio

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// TestFinalAudioOutputPathIsUniquePerInvocation is the deterministic half of
// the listen-gate flake regression: the plan hash is stable across runs, so the
// output name must not be. Concurrent invocations for the same plan must
// reserve distinct files, otherwise one renderer truncates the master while
// another probes it.
func TestFinalAudioOutputPathIsUniquePerInvocation(t *testing.T) {
	const planSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const invocations = 64
	paths := make([]string, invocations)
	var wg sync.WaitGroup
	for i := 0; i < invocations; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := FinalAudioOutputPath(planSHA)
			if err != nil {
				t.Errorf("reserve output: %v", err)
				return
			}
			paths[i] = path
		}()
	}
	wg.Wait()

	seen := make(map[string]struct{}, invocations)
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			t.Fatalf("two renders reserved the same output path %q", path)
		}
		seen[path] = struct{}{}
		if dir := os.TempDir(); !strings.HasPrefix(path, dir) {
			t.Fatalf("output %q is not under the OS temp dir %q", path, dir)
		}
		if !strings.Contains(path, planSHA) {
			t.Fatalf("output %q lost the plan-hash traceability", path)
		}
		if !strings.HasSuffix(path, ".m4a") || !strings.Contains(path, "pipelinegen-final-audio-") {
			t.Fatalf("output %q does not match the final-audio naming contract", path)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("placeholder %q was not removed before the render: %v", path, err)
		}
	}
	if len(seen) != invocations {
		t.Fatalf("reserved %d distinct paths, want %d", len(seen), invocations)
	}
}

func TestFinalAudioOutputPathWithoutPlanHash(t *testing.T) {
	first, err := FinalAudioOutputPath("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := FinalAudioOutputPath("")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("hash-less renders collided on %q", first)
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("placeholder %q was not removed: %v", path, err)
		}
	}
}
