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
// gate the alias switch. Fields mirror qdrant.SwitchReport (the
// infra-side type) — the VerifierAdapter in dr_adapter.go performs
// the field-by-field translation.
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


