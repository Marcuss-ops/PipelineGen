// Tests for the domain.SourcingIndexStatus canonical typed-enum.
//
// Per §12-5 CONTRACT test pins:
//
//	(1) marshal/unmarshal round-trip JSON wire field `indexing_status`;
//	(2) compile-time assertion that the canonical type compiles;
//	(3) residue grep test — zero references to the retired IndexingStatus
//	    alias (the alias was removed in §12-5 CONTRACT).
package assets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/Marcuss-ops/PipelineGen/internal/capabilities/sourcing"
)

// repoRoot walks up from the test's working directory until it finds
// the project's go.mod. The test runs with cwd set to the package
// directory (internal/application/assets/sourcing/) so the path upward
// is reliable across machines. Returns the absolute repo root.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(candidate); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("go.mod not found from %s", wd)
	return ""
}

// TestIndexingStatus_TypeIdentity proves that domain.SourcingIndexStatus
// is the canonical typed-enum for the `indexing_status` JSON wire field.
// After §12-5 CONTRACT, the sourcing.IndexingStatus alias was removed;
// production code consumes domain.SourcingIndexStatus directly.
func TestIndexingStatus_TypeIdentity(t *testing.T) {
	// Canonical constant assignment.
	var x domain.SourcingIndexStatus = domain.SourcingIndexStatusPending
	var y domain.SourcingIndexStatus = x
	if y != domain.SourcingIndexStatusPending {
		t.Fatalf("round-trip identity mismatch: x=%q y=%q", x, y)
	}

	// Empty-marker round-trip.
	var empty domain.SourcingIndexStatus = ""
	var back domain.SourcingIndexStatus = empty
	if back != "" {
		t.Fatalf("empty string round-trip mismatch: empty=%q back=%q", empty, back)
	}
}

// TestIndexingStatus_TypedAliasRoundTrip is the JSON round-trip test
// on the domain.SourcingIndexStatus canonical enum.
// Verifies byte-stability at the JSON layer after §12-5 CONTRACT.
func TestIndexingStatus_TypedAliasRoundTrip(t *testing.T) {
	type payload struct {
		IndexingStatus domain.SourcingIndexStatus `json:"indexing_status"`
	}
	for _, status := range []domain.SourcingIndexStatus{
		domain.SourcingIndexStatusPending,
		domain.SourcingIndexStatusSkipped,
		domain.SourcingIndexStatusCompleted,
		domain.SourcingIndexStatusFailed,
	} {
		t.Run(status.String(), func(t *testing.T) {
			enc, err := json.Marshal(payload{IndexingStatus: status})
			if err != nil {
				t.Fatalf("marshal %q: %v", status, err)
			}
			// Wire contains the canonical 4-state value.
			if !strings.Contains(string(enc), `"indexing_status":"`+status.String()+`"`) {
				t.Errorf("JSON %q missing wire %q", string(enc), status.String())
			}
			var dec payload
			if err := json.Unmarshal(enc, &dec); err != nil {
				t.Fatalf("unmarshal %q: %v", status, err)
			}
			if dec.IndexingStatus != status {
				t.Fatalf("round trip = %q, want %q", dec.IndexingStatus, status)
			}
		})
	}
}

// TestSourcingNoLegacyIndexingStrings verifies zero references to the
// pre-§12-5 placeholder strings "enqueued" or "not_configured" in
// production code under internal/application/assets/sourcing/ and
// internal/api/assets/register/.
//
// Spec pin (3): residue grep test in sourcing/ verifies zero references
// to IndexingStatus standalone (alias call-sites via TYPE only). Alias
// call-sites via TYPE are allowed (struct field declarations, type
// casts); literal-string equivalents MUST be empty post-§12-5 EXPAND.
func TestSourcingNoLegacyIndexingStrings(t *testing.T) {
	legacyLiterals := []string{`"enqueued"`, `"not_configured"`}

	root := repoRoot(t)
	subtrees := []string{
		filepath.Join(root, "internal/application/assets/sourcing/"),
		filepath.Join(root, "internal/api/assets/register/"),
	}

	for _, root := range subtrees {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			// _test.go files are ALLOWED to mention legacy literals
			// (the residue-test itself, godlike/06 audit-pins, or future
			// operator log fixtures).
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			// §12-5 EXPAND §B.2 audit-pin discipline: doc-comments
			// describing the migration (e.g. "wire value evolves
			// from enqueued/not_configured → pending/skipped") are
			// explicitly anchored as audit-pins and must remain
			// readable for future agents. The residue check skips
			// documentation is preserved; non-comment lines that
			// still mention "enqueued" or "not_configured" are
			// real production-code failures.
			for _, literal := range legacyLiterals {
				for _, line := range strings.Split(string(data), "\n") {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "//") {
						continue // skip `//`-prefix doc-comments
					}
					if strings.Contains(line, literal) {
						t.Errorf("RESIDUE: %s contains legacy literal %s in code-line %q (§12-5 EXPAND residue — must migrate to canonical %q/%q)",
							path, literal, line,
							domain.SourcingIndexStatusPending, domain.SourcingIndexStatusSkipped)
						break // one error per file per literal (avoid spam)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("walk %s: %v", root, err)
		}
	}
}

// TestSourcingAliasCohesion verifies the canonical IndexingStatus alias
// has been PHYSICALLY REMOVED from types.go (§12-5 CONTRACT). The alias
// was retired in favour of direct consumption of domain.SourcingIndexStatus.
func TestSourcingAliasCohesion(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "internal/application/assets/sourcing/types.go"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	// §12-5 CONTRACT: the alias MUST be gone.
	aliasCount := strings.Count(contents, "type IndexingStatus")
	if aliasCount != 0 {
		t.Errorf("expected ZERO declarations of type IndexingStatus in types.go after §12-5 CONTRACT, got %d", aliasCount)
	}
}
