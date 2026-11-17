package scriptgeneration

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
	// The former Go matcher is retained only as a test oracle; production uses
	// the Rust localized-identity operation wired in script_generation_runtime.go.
	defaultLocalizedIdentityMatcher = oracleLocalizedIdentityMatcher{}
	os.Exit(m.Run())
}
