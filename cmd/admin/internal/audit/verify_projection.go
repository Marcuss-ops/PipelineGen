// Package audit — verify_projection.go (RETIRED, POSTGRES-MEDIA-CUTOVER,
// September 2026).
//
// RunVerifyProjection compared the canonical eligible SQLite asset set
// against the ACTIVE Qdrant media projection (`media_assets` /
// `media_assets_current`) and reported missing/orphan points.
//
// That projection no longer exists. With PostgreSQL + pgvector as the media
// SSOT, the Qdrant media collection is not a projection of anything, so the
// comparison could only report drift against a retired plane while reading
// quarantine SQLite media state. The verifier it depended on
// (`qdrant/verification.NewProjectionVerifier`) was deleted with it, together
// with the projection reconciler that consumed the same report.
//
// The command stays REGISTERED and fails closed with a typed retirement error
// so operator scripts surface the retirement instead of a silent
// command-not-found. Media parity evidence today: `backfill-media-postgres
// --verify-only` plus `TEST_POSTGRES_DSN=… go test
// ./internal/platform/postgres/media/`.
package audit

import (
	"errors"
	"fmt"
)

// ErrMediaProjectionAuditRetired is returned by the retired projection audit.
var ErrMediaProjectionAuditRetired = errors.New("verify-projection: auditing the Qdrant media_assets projection was retired with the PostgreSQL media-SSOT cutover (media demolition, September 2026) — the Qdrant media collection is no longer a projection of SQLite; use 'backfill-media-postgres --verify-only' for media parity evidence")

// RunVerifyProjection is retained as a subcommand stub: it fails closed with
// the typed retirement error instead of measuring a retired projection.
func RunVerifyProjection(_ []string) error {
	return fmt.Errorf("%w", ErrMediaProjectionAuditRetired)
}
