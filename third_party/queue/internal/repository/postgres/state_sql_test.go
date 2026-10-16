package postgres

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestStateLiteralVocabulary pins the exact SQL the helpers render, so the
// generated predicates stay byte-identical to the literals they replaced.
func TestStateLiteralVocabulary(t *testing.T) {
	if got := stateLiteral(model.StateRunning); got != "'running'" {
		t.Fatalf("stateLiteral(StateRunning) = %q, want 'running'", got)
	}
	if got := stateIn(model.StateRunning, model.StateFinalizing); got != "('running', 'finalizing')" {
		t.Fatalf("stateIn(StateRunning, StateFinalizing) = %q, want ('running', 'finalizing')", got)
	}
}

// TestNoRawStateLiteralsInQueries is the ratchet for P1-6: every lifecycle
// state predicate in this package must go through stateLiteral/stateIn. A raw
// SQL literal survives a state rename at compile time and fails only at
// runtime against the database, which the CHECK-constraint vocabulary test
// does not catch. A new raw literal fails here.
func TestNoRawStateLiteralsInQueries(t *testing.T) {
	re := regexp.MustCompile(`state\s*(=|IN)\s*\(?\s*'`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if re.MatchString(line) {
				t.Errorf("%s:%d: raw lifecycle-state SQL literal; use stateLiteral/stateIn: %s", name, i+1, trimmed)
			}
		}
	}
}
