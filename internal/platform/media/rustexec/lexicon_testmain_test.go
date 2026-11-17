package rustexec

import (
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// TestMain installs the Rust-generated lexicon snapshot for the whole test
// binary. The phrase-impact adapter resolves the language's phrase stop words
// from it, so asserting the injection against the generated data is the only
// way to catch a wiring change that ships an English-only set for every
// language.
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
