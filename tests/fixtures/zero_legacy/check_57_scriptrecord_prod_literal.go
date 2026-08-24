//go:build ignore

// Package zero_legacy — fixture for Check 57 (forbid ports.ScriptRecord
// literal outside canonical allowlist).
//
// This file exists ONLY so that `bash scripts/ci-architectural-checks.sh
// --self-check` can verify that the regex `ports\.ScriptRecord\{` (the
// ripgrep pattern for the production gate) catches the forbidden
// composite-literal pattern. It is NOT executed at runtime and is
// excluded from the production build by the gate's own walker (which
// never sees this file — self-check mode reads each fixture directly).
//
// The line below contains `&ports.ScriptRecord{ID: 1}` which the regex
// MUST match. If a future contributor edits the Check 57 regex and
// accidentally regresses its precision, the self-check loop will
// detect it (rg returns nothing on this fixture → self-check exit 1).
package fixture

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

// BadLiteral is the synthetic production-shape pattern the gate MUST
// catch. The composite literal `&ports.ScriptRecord{ID: 1}` mirrors
// the canonical read-path translator's `return &ports.ScriptRecord{…}`
// construction in `internal/infrastructure/database/sqlite/scripts/repository_adapter.go`,
// but in violation of the gate's allowlist. PersistenceProcessor is the
// SOLE canonical writer; cross-package construction of `*ports.ScriptRecord`
// outside the allowlisted canonical sites is a godlike/06 one-owner-per-fact
// regression.
func BadLiteral() *ports.ScriptRecord {
	return &ports.ScriptRecord{ID: 1}
}
