package imagesearch

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// The resolver's structural vocabulary (negation phrases, pronouns, articles,
// possessive openers, subordinate markers, contrast openers) is lexicon data
// from config/lexicons/**, not code. The test process installs the same
// repository registry the composition root does, so the tests exercise the
// shipped data instead of a test-only copy.
func TestMain(m *testing.M) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../config/lexicons"))
	registry, err := linguistics.NewLexiconRegistry(root)
	if err != nil {
		panic(err)
	}
	if err := linguistics.SetDefaultLexicon(registry); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
