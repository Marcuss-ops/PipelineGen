// dr/types.go — QDRANT-005C PR3 / PR-QDRANT-WIRE-MIRROR (June 2026).
//
// PR-QDRANT-WIRE-MIRROR (June 2026): SnapshotDescription, RetentionConfig,
// and RetentionResult were unified in internal/domain/qdrantdr/. The
// type aliases below keep existing callers compiling while the
// canonical shape lives in the domain layer.
//
// Application-layer callers using dr.RetentionConfig may leave
// MaxAgeSeconds=0 and AgingTable=nil — the infra-side sweep falls
// back to the keep_last_n alpha cut.
package dr

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/qdrantdr"
)

// SnapshotDescription is the canonical DR snapshot shape (type alias).
type SnapshotDescription = qdrantdr.SnapshotDescription

// RetentionConfig is the canonical DR retention config (type alias).
type RetentionConfig = qdrantdr.RetentionConfig

// RetentionResult is the canonical DR retention result (type alias).
type RetentionResult = qdrantdr.RetentionResult

// VerifyReport is the canonical pre-switch verification report.
// Produced by Verifier.VerifyReindex; consumed by RestoreService to
// gate the alias switch. It is intentionally narrower than
// qdrant.schema.SwitchReport; the VerifierAdapter performs the explicit
// boundary projection so the DR use case does not depend on infra-only
// scan details.
type VerifyReport struct {
	Ready           bool     `json:"ready"`
	ExpectedPoints  int      `json:"expected_points"`
	ActualPoints    int      `json:"actual_points"`
	MissingCount    int      `json:"missing_count"`
	OrphanCount     int      `json:"orphan_count"`
	PayloadIssues   int      `json:"payload_issues"`
	VersionMismatch int      `json:"version_mismatch"`
	DeadLetterOpen  int      `json:"dead_letter_open"`
	Errors          []string `json:"errors,omitempty"`
}

// noopMetrics is the default DRMetrics implementation used when
// deps.Metrics is nil. All methods are no-ops.
type noopMetrics struct{}

func (noopMetrics) RecordAliasSwitch(string, float64) {}
func (noopMetrics) SetAliasCurrent(string, string)    {}

// NowFunc is the default clock source used when deps.Now is nil.
// Tests inject a fixed clock; production uses time.Now.
var NowFunc = time.Now
