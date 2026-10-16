package postgres

import (
	"strings"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// stateLiteral renders a lifecycle-state SQL literal from the canonical
// model.State vocabulary.
//
// The migrations' CHECK constraints are pinned to AllStates() by
// queue/internal/model/state_schema_test.go, but QUERY TEXT was not: a raw
// `'running'` literal keeps compiling after a state rename and fails only at
// runtime against the database. Every state predicate in this package goes
// through here, so a rename is a compile-time-visible change.
func stateLiteral(s model.State) string { return "'" + string(s) + "'" }

// stateIn renders an SQL IN list of lifecycle states, e.g. ('running',
// 'finalizing').
func stateIn(states ...model.State) string {
	parts := make([]string, len(states))
	for i, s := range states {
		parts[i] = stateLiteral(s)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
