package qualitygate

import (
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// The test process installs the same Rust-generated lexicon snapshot the
// composition root uses; no test-only word lists are allowed.
func TestMain(m *testing.M) {
	profiles, err := linguistics.NewProfiles()
	if err != nil {
		panic(err)
	}
	if err := linguistics.SetDefaultProfiles(profiles); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
