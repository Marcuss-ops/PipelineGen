// Package mediamemory — port-level types re-exported from
// capabilities/mediamemory/ports_types.go
package mediamemory

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"

type QueryCacheEntry = mediamemory.QueryCacheEntry
type SearchFanOutQuery = mediamemory.SearchFanOutQuery
type SearchFanOutResult = mediamemory.SearchFanOutResult
type MaterializeOptions = mediamemory.MaterializeOptions
type RightsDecision = mediamemory.RightsDecision
type RightsVerdict = mediamemory.RightsVerdict

const (
	RightsVerdictAllow            = mediamemory.RightsVerdictAllow
	RightsVerdictAllowConditional = mediamemory.RightsVerdictAllowConditional
	RightsVerdictDeny             = mediamemory.RightsVerdictDeny
)

var IsKnownRightsVerdict = mediamemory.IsKnownRightsVerdict