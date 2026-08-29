package performance

// cold_warm.go owns the comparative cold #1 vs warm #2-N report contract over
// performance_operations — the verifier's answer to "how much does the first
// attempt of a battery cost vs the warm repeats". It is a pure read
// projection: the report never re-measures anything and never mutates the
// registry.
//
// Semantics: attempts are the distinct measured runs in scope (run_id is one
// render attempt; the Chronon Metrics Adapter publishes one row per phase per
// attempt). The scope is the chronological sequence of attempts ordered by
// first-seen (created_at, insertion order); attempt #1 is the cold bucket,
// attempts #2..MaxAttempts are the warm bucket. This matches the certified
// battery shape (cold #1, warm #2..5) without guessing from metadata: the
// split is positional, never fabricated from cache flags.

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ColdWarmOptions scopes the comparison. MaxAttempts defaults to 5 when
// non-positive: attempt #1 = cold, attempts #2..MaxAttempts = warm. Since is
// an optional created_at floor (RFC3339) so a battery can be re-scoped to a
// date range.
type ColdWarmOptions struct {
	JobID       string
	MaxAttempts int
	Since       string
}

// OperationBucket is one operation's GROUP BY aggregate (AVG/MIN/MAX of
// elapsed_ms) over the measured rows of one bucket (cold or warm).
type OperationBucket struct {
	Operation    string  `json:"operation"`
	Runs         int     `json:"runs"`
	AvgElapsedMS float64 `json:"avg_elapsed_ms"`
	MinElapsedMS int64   `json:"min_elapsed_ms"`
	MaxElapsedMS int64   `json:"max_elapsed_ms"`
}

// ColdWarmComparison is the comparative report: the same operations bucketed
// into cold (attempt #1) and warm (attempts #2..N). Attempts is the total
// number of attempts bucketed; ColdAttempts is 1 (when any attempt exists) and
// WarmAttempts = Attempts - ColdAttempts.
type ColdWarmComparison struct {
	JobID        string            `json:"job_id,omitempty"`
	Attempts     int               `json:"attempts"`
	ColdAttempts int               `json:"cold_attempts"`
	WarmAttempts int               `json:"warm_attempts"`
	Cold         []OperationBucket `json:"cold"`
	Warm         []OperationBucket `json:"warm"`
}

// ColdWarmSource is the narrow query-side port for the comparative cold/warm
// report over performance_operations. The concrete adapter is the platform
// OperationStore; an empty history is a valid answer (no attempts bucketed).
type ColdWarmSource interface {
	ColdWarmComparison(context.Context, ColdWarmOptions) (ColdWarmComparison, error)
}

// RenderColdWarmText renders the comparative report as an aligned text table
// for the CLI/dashboard consumer. Empty buckets render as "—".
func RenderColdWarmText(c ColdWarmComparison) string {
	var b strings.Builder
	if c.WarmAttempts > 0 {
		fmt.Fprintf(&b, "Cold #1 vs Warm #2-%d — performance_operations (GROUP BY operation, AVG/MIN/MAX elapsed_ms)\n",
			c.ColdAttempts+c.WarmAttempts)
	} else {
		b.WriteString("Cold vs Warm — performance_operations (GROUP BY operation, AVG/MIN/MAX elapsed_ms)\n")
	}
	scope := "all jobs"
	if c.JobID != "" {
		scope = "job " + c.JobID
	}
	fmt.Fprintf(&b, "scope: %s | attempts: %d (cold: %d, warm: %d)\n\n",
		scope, c.Attempts, c.ColdAttempts, c.WarmAttempts)

	// Union of operations across both buckets, sorted.
	seen := map[string]bool{}
	var operations []string
	for _, op := range append(append([]OperationBucket{}, c.Cold...), c.Warm...) {
		if !seen[op.Operation] {
			seen[op.Operation] = true
			operations = append(operations, op.Operation)
		}
	}
	sort.Strings(operations)
	if len(operations) == 0 {
		b.WriteString("(no measured operations in scope)\n")
		return b.String()
	}

	width := len("operation")
	for _, op := range operations {
		if len(op) > width {
			width = len(op)
		}
	}
	col := func(n int) string { return fmt.Sprintf("%d", n) }
	fmt.Fprintf(&b, "%-*s | cold %-6s %8s %8s %8s | warm %-6s %8s %8s %8s\n",
		width, "operation", "n", "avg_ms", "min_ms", "max_ms", "n", "avg_ms", "min_ms", "max_ms")
	fmt.Fprintf(&b, "%s-|-------%s-|-------%s\n",
		strings.Repeat("-", width), strings.Repeat("-", 41), strings.Repeat("-", 41))
	byOp := map[string]map[string]OperationBucket{}
	for _, bucket := range c.Cold {
		if byOp[bucket.Operation] == nil {
			byOp[bucket.Operation] = map[string]OperationBucket{}
		}
		byOp[bucket.Operation]["cold"] = bucket
	}
	for _, bucket := range c.Warm {
		if byOp[bucket.Operation] == nil {
			byOp[bucket.Operation] = map[string]OperationBucket{}
		}
		byOp[bucket.Operation]["warm"] = bucket
	}
	cell := func(bucket OperationBucket, present bool) string {
		if !present {
			return fmt.Sprintf("%-6s %8s %8s %8s", "—", "—", "—", "—")
		}
		return fmt.Sprintf("%-6s %8.0f %8d %8d", col(bucket.Runs), bucket.AvgElapsedMS, bucket.MinElapsedMS, bucket.MaxElapsedMS)
	}
	for _, op := range operations {
		coldBucket, hasCold := byOp[op]["cold"]
		warmBucket, hasWarm := byOp[op]["warm"]
		fmt.Fprintf(&b, "%-*s | %s | %s\n", width, op, cell(coldBucket, hasCold), cell(warmBucket, hasWarm))
	}
	return b.String()
}
