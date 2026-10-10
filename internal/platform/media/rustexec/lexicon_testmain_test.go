package rustexec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// TestMain installs the repository lexicon registry for the whole test binary.
// The phrase-impact adapter resolves the language's phrase stop words from it,
// so asserting the injection against the real config/lexicons data is the only
// way to catch a wiring change that ships an English-only set for every
// language.
func TestMain(m *testing.M) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "config", "lexicons"))
	registry, err := linguistics.NewLexiconRegistry(root)
	if err != nil {
		panic(err)
	}
	if err := linguistics.SetDefaultLexicon(registry); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
