// Package jobs — RED-2 / JOBS-T01-001 closure (2026-07-04).
//
// rfc3339TimeScanner is a typed sql.Scanner for *time.Time targets whose
// underlying column returns TEXT (NOT native DATETIME). This is needed
// for the strftime() canonical wrap applied on ListEvents' SELECT
// (repository.go::ListEvents): with parseTime=false (the project's
// default in `internal/infrastructure/database/sqlite/`), the mattn
// driver surfaces the strftime output as `driver.Value` type string;
// the database/sql default value-converter cannot auto-convert
// `string` → `*time.Time`.
//
// godlike/06 SSOT: this type is the canonical ONE-time scan adapter
// for strftime-wrapped time columns in this package. Future surfaces
// that reuse the strftime wrap MUST also use this scanner; direct
// `&evt.CreatedAt` Scan targets against strftime columns will fail.
//
// godlike/07 minimum-blast-radius: scanner lives in its own file so
// it does not bloat repository.go (already 580+ lines; now slim
// orchestrator ~143 LOC post-PR-SPLIT-JOBS-REPO-RESIDUAL). Only caller
// is ListEvents (now in repository_events.go per the post-split
// topology). Forward-pointer:
// PR-SCAN-ADAPTER-DRY if more time.Time scan sites emerge.
package jobs

import (
	"fmt"
	"time"
)

type rfc3339TimeScanner struct {
	t *time.Time
}

func (s *rfc3339TimeScanner) Scan(value any) error {
	if value == nil {
		return nil
	}
	if t, ok := value.(time.Time); ok {
		*s.t = t
		return nil
	}
	var str string
	switch v := value.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	default:
		return fmt.Errorf("rfc3339TimeScanner: unsupported type %T", value)
	}
	if str == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, str)
	if err != nil {
		return fmt.Errorf("rfc3339TimeScanner: parse %q: %w", str, err)
	}
	*s.t = parsed
	return nil
}
