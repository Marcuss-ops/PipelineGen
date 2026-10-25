package adapters

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// English stop/function-word set from config/lexicons/**. The test process
// installs the repository registry, matching the composition root.
func TestMain(m *testing.M) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../config/lexicons"))
	registry, err := linguistics.NewLexiconRegistry(root)
	if err != nil {
		panic(err)
	}
	if err := linguistics.SetDefaultLexicon(registry); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
