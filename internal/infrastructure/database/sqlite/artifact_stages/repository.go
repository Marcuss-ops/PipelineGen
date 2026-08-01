// Package artifactstages — internal/infrastructure/database/sqlite/artifact_stages/repository.go
//
// FASE 3 (Push 3.1a, July 2026): canonical concrete for the
// `artifact.Repository` port (internal/domain/artifact/stages.go).
// Backs the FASE 3 Spina Dorsale publication saga with the
// `artifact_stages` SQLite table (migration 147).
//
// godlike/06 SSOT: this file is the SINGLE canonical writer of the
// per-publication record. Application-layer code (staging.Store in
// Push 3.1b, publisher worker pool, finalizer) consumes the domain
// Repository port; the concrete is built at the composition root
// (internal/app/build_bundles_*.go) and receives *sql.DB.
//
// godlike/07 fail-closed:
//   - Pre-TX validation: State.IsValid() + Requirement.IsValid() +
//     Size > 0 + Hash non-empty; out-of-set values are rejected with
//     the typed sentinels (ErrInvalidArtifactStageState /
//     ErrInvalidRequirement / ErrArtifactStageEmpty /
//     ErrArtifactStageHashMismatch) WITHOUT touching the DB.
//   - Fenced CAS: each Mark* method gates the UPDATE on a
//     per-method state-fence that reflects what is terminal for
//     that method's target. MarkPublished uses the broadest
//     fence — `state NOT IN ('PUBLISHED','SUCCEEDED','FAILED_PERMANENT')`
//     — because a re-publish from PUBLISHED would silently
//     duplicate the Drive upload. The other Mark* methods
//     (MarkSucceeded / MarkFailedPermanent / IncrementAttemptCount)
//     fence only on truly-terminal states —
//     `state NOT IN ('SUCCEEDED','FAILED_PERMANENT')` — because
//     PUBLISHED is the canonical transitional state awaited by the
//     finalizer (PUBLISHED → SUCCEEDED via finalizer scan). The
//     disambiguation probe distinguishes "row absent" (NotFound)
//     from "row already terminal" (TerminalStateRejection) —
//     single round-trip on the success path, two on the failure
//     path (post-UPDATE SELECT probe).
//   - All timestamps are UTC + RFC3339Nano per PipelineGen SSOT.
//
// Implementation notes (Push 3.1a follow-up): the 8 methods are
// enough for the publisher worker pool + finalizer to round-trip
// the FASE 3 saga. A future Push may add
// `(*Repository).MarkAttemptFailed(ctx, id, lastError)` that bumps
// attempt_count + last_error in a single UPDATE (the per-publisher
// path uses 2 calls today; combining them is a perf + atomicity
// win but out of scope for 3.1a).
//
// File layout (split by domain, July 2026):
//
//	repository.go            core: struct, constructor, shared helpers + sentinels
//	repository_artifacts.go  artifact CRUD: Insert / GetByID / ListByJob / ListByState
//	repository_stages.go     state machine: MarkPublished / MarkSucceeded /
//	                         MarkFailedPermanent / IncrementAttemptCount + fenced CAS
//	repository_outbox.go     outbox co-emission: InsertWithOutbox
package artifactstages

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/domain/artifact"
)

// Compile-time assertion: *Repository satisfies the domain port.
var _ artifact.Repository = (*Repository)(nil)

// Repository is the SQLite-backed implementation of
// artifact.Repository. Holds a *sql.DB; all 8 methods are safe for
// concurrent use (database/sql is the standard connection pool).
type Repository struct {
	db *sql.DB
	// nowFn is overridable for tests (default time.Now). UTC is
	// enforced via the helper to match PipelineGen's SSOT.
	nowFn func() time.Time
}

// NewRepository constructs the canonical artifact_stages
// repository. Caller MUST supply a non-nil *sql.DB; composition
// root fails-fast on nil at construction (godlike/07).
func NewRepository(db *sql.DB) *Repository {
	return &Repository{
		db:    db,
		nowFn: func() time.Time { return time.Now().UTC() },
	}
}

// SetNowFn overrides the clock source used by Insert / Mark*
// operations for CreatedAt + UpdatedAt + PublishedAt column
// writing. Production code never calls this — time.Now() is the
// canonical source via NewRepository's default. The seam exists
// ONLY to enable hermetic test replay for time-sensitive paths
// (e.g., publish_drive/handler_integration_test.go's
// deterministic-clock assertions on published_at + updated_at).
//
// godlike/06 SSOT: the field `nowFn` is unexported (per Go
// convention); external test packages cannot field-assign it.
// The public SetNowFn is the canonical seam for crossing the
// package boundary without exporting the field itself.
//
// godlike/07 fail-closed: callers MUST supply non-nil fn; nil
// would panic at the first r.now() call (forward-pointer — a
// future hardening could error-return, but for now the
// panic-on-nil surfaces the misuse immediately at boot/test).
func (r *Repository) SetNowFn(fn func() time.Time) {
	r.nowFn = fn
}

// ── Pre-TX validation helpers ────────────────────────────────────────────

// validateForWrite enforces the Insert contract. Returns the
// FIRST violation's typed sentinel so callers can errors.Is-probe
// the specific failure class.
func validateForWrite(stage *artifact.ArtifactStage) error {
	if stage == nil {
		return fmt.Errorf("artifact_stages.Insert: nil stage")
	}
	if stage.ID == "" {
		return fmt.Errorf("%w", artifact.ErrInvalidArtifactStageID)
	}
	if stage.JobID == "" {
		return fmt.Errorf("%w", artifact.ErrInvalidJobID)
	}
	if !stage.State.IsValid() {
		return fmt.Errorf("%w: %q", artifact.ErrInvalidArtifactStageState, stage.State)
	}
	// godlike/07 state-machine enforcement: Insert is the entry
	// point of the saga; only StateStaged is allowed. PUBLISHED /
	// SUCCEEDED / FAILED_PERMANENT can only be reached via the
	// fenced Mark* transitions. A caller that tries to insert a
	// non-STAGED row bypasses the state machine; rejected with
	// ErrInvalidArtifactStageState (same sentinel as non-canonical
	// values, so log-greppers get one consistent failure class).
	if stage.State != artifact.StateStaged {
		return fmt.Errorf("%w: Insert requires state=STAGED (canonical initial state of the saga), got %q", artifact.ErrInvalidArtifactStageState, stage.State)
	}
	if !stage.Requirement.IsValid() {
		return fmt.Errorf("%w: %q", artifact.ErrInvalidRequirement, stage.Requirement)
	}
	if stage.Size <= 0 {
		return fmt.Errorf("%w: size=%d", artifact.ErrArtifactStageEmpty, stage.Size)
	}
	if stage.Hash == "" {
		return fmt.Errorf("%w: hash is empty", artifact.ErrArtifactStageHashMismatch)
	}
	return nil
}

// now returns the repository's monotonic time source, UTC-normalised.
func (r *Repository) now() time.Time {
	return r.nowFn().UTC()
}

// ── column list (single source of truth for INSERT + SELECT) ──────────────

const selectColumns = `id, job_id, local_path, hash, size, mime, requirement, destination, state, attempt_count, last_error, published_location, published_at, created_at, updated_at`

// scanRow materialises one row into a typed ArtifactStage. The
// time columns (created_at, updated_at, published_at) are stored
// as TEXT in the canonical schema (migration 147) and the
// mattn/go-sqlite3 driver's parseTime=true only auto-converts
// TIMESTAMP/DATETIME columns. We mirror the production pattern at
// internal/infrastructure/database/sqlite/jobs/finalize_attempt.go
// (which uses timeutil.FormatRFC3339 for the same columns): read
// TEXT, parse to time.Time with time.RFC3339Nano. published_at
// is NULL-able so we read into a *string + post-parse to *time.Time.
func scanRow(row interface{ Scan(...interface{}) error }) (artifact.ArtifactStage, error) {
	var (
		s            artifact.ArtifactStage
		publishedAtS *string
		publishedLoc string
		requirement  string
		state        string
		createdAtS   string
		updatedAtS   string
	)
	if err := row.Scan(
		&s.ID, &s.JobID, &s.LocalPath, &s.Hash, &s.Size, &s.Mime,
		&requirement, &s.Destination, &state, &s.AttemptCount,
		&s.LastError, &publishedLoc, &publishedAtS, &createdAtS, &updatedAtS,
	); err != nil {
		return s, err
	}
	createdAt, err := parseRFC3339Nano(createdAtS, "created_at")
	if err != nil {
		return s, err
	}
	updatedAt, err := parseRFC3339Nano(updatedAtS, "updated_at")
	if err != nil {
		return s, err
	}
	var publishedAt *time.Time
	if publishedAtS != nil && *publishedAtS != "" {
		pt, err := parseRFC3339Nano(*publishedAtS, "published_at")
		if err != nil {
			return s, err
		}
		publishedAt = &pt
	}
	s.Requirement = artifact.Requirement(requirement)
	s.State = artifact.ArtifactStageState(state)
	s.PublishedLocation = publishedLoc
	s.PublishedAt = publishedAt
	s.CreatedAt = createdAt
	s.UpdatedAt = updatedAt
	return s, nil
}

// parseRFC3339Nano parses a non-empty string as time.RFC3339Nano
// (the canonical PipelineGen wire format — see
// timeutil.FormatRFC3339Nano, used by every write site in this
// repository for nano-precision round-trip). Returns a typed
// sentinel error so the caller can errors.Is-probe the parse
// failure. Empty input returns the zero time + ErrTimestampEmpty
// so callers can distinguish a missing timestamp from a
// malformed one.
//
// godlike/07 fail-closed: only the canonical RFC3339Nano format
// is accepted. A row that holds a non-canonical format is a
// schema-drift signal — the upstream writer (a direct-DB INSERT
// bypassing the repository) is the bug, and the row MUST be
// remediated by the operator, NOT silently re-parsed by a
// permissive fallback.
func parseRFC3339Nano(s, columnName string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("%w: column=%s", ErrTimestampEmpty, columnName)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: column=%s value=%q parse_err=%v", ErrTimestampParse, columnName, s, err)
	}
	return t.UTC(), nil
}

// ── JSON helper (placeholder for future typed PublishedLocation) ─────────

// MarshalPublishedLocation is a helper for callers who want to
// serialise a typed PublishedLocation. The repository stores the
// canonical wire format (compact JSON); the field is TEXT in
// SQLite (per migration 147). Callers MAY pass any string-format
// payload; the repository does not interpret the bytes (forward-
// compat with shape evolution).
func MarshalPublishedLocation(loc any) (string, error) {
	b, err := json.Marshal(loc)
	if err != nil {
		return "", fmt.Errorf("artifact_stages.MarshalPublishedLocation: %w", err)
	}
	return string(b), nil
}

// ErrTimestampEmpty is the canonical repository-level sentinel
// returned by parseRFC3339Nano when the column value is the empty
// string. godlike/07 fail-closed: an empty TEXT timestamp is a
// schema-violation signal (every canonical column has DEFAULT
// datetime('now')) and is rejected as a parse failure rather
// than silently coerced to the zero time.
var ErrTimestampEmpty = errors.New("artifact_stages: timestamp column is empty (canonical format is RFC3339Nano)")

// ErrTimestampParse is the canonical repository-level sentinel
// returned by parseRFC3339Nano when the column value is non-empty
// but cannot be parsed in any known format. godlike/07 fail-closed.
var ErrTimestampParse = errors.New("artifact_stages: timestamp column value cannot be parsed as RFC3339Nano")
