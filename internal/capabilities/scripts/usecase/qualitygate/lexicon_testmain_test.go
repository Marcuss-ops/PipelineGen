package qualitygate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// The quality-gate package detects the generated language against the
// repository lexicon, so its test process installs the same registry the
// composition root uses; no test-only word lists are allowed. Mirrors
// ../lexicon_testmain_test.go (the package was extracted from usecase).
func TestMain(m *testing.M) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../../config/lexicons"))
	registry, err := linguistics.NewLexiconRegistry(root)
	if err != nil {
		panic(err)
	}
	if err := linguistics.SetDefaultLexicon(registry); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
