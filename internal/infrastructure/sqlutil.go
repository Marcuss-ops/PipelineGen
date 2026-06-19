// Package sqlutil provides SQL LIKE fallback builders.
//
// Deprecated: use pkg/sqlutil instead. This file delegates to the canonical
// implementation in pkg/sqlutil/ so call sites can migrate incrementally.
package platform

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/sqlutil"
)

// Deprecated: use pkg/sqlutil.BuildFallbackLikeConditions.
func BuildFallbackLikeConditions(tokens []string, columns []string) (string, []any) {
	return sqlutil.BuildFallbackLikeConditions(tokens, columns)
}

// Deprecated: use pkg/sqlutil.BuildFallbackLikeConditionsOR.
func BuildFallbackLikeConditionsOR(tokens []string, columns []string) (string, []any) {
	return sqlutil.BuildFallbackLikeConditionsOR(tokens, columns)
}
