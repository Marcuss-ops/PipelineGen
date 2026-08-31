package operational

import (
	"os"
	"testing"
)

// requireExplicitSmokeDB prevents live smoke wrappers from falling back to
// the operational SQLite database when a caller omitted the test database.
// The caller remains responsible for supplying either a temporary database
// or an explicitly approved live database through SMOKE_DB.
func requireExplicitSmokeDB(t *testing.T) {
	t.Helper()
	if os.Getenv("SMOKE_DB") == "" {
		t.Skip("SMOKE_DB not set; live operational test requires an explicit isolated or approved database path")
	}
}
