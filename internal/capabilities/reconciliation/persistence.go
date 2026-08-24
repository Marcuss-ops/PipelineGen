package reconciliation

import (
	"encoding/json"
	"fmt"
	"os"
)

// writeFileDefault is the default on-disk writer used by
// filesystemReportWriter. Indirected through a package-level var so
// tests can substitute an in-memory sink without touching the
// filesystem:
//
//	oldWrite := writeFileDefault
//	writeFileDefault = func(p string, v any) error { ... }
//	defer func() { writeFileDefault = oldWrite }()
//
// Tests MAY rely on this for golden-file comparisons but production
// callers should leave it untouched.
var writeFileDefault = func(path string, v any) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write report %q: %w", path, err)
	}
	return nil
}
