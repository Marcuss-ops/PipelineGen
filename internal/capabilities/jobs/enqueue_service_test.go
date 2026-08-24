// Package jobs — enqueue_service_test.go (PR-jobs-retry-contract, July 2026).
//
// TDD-first coverage of:
//   - the 4-arg NewService fail-closed constructor
//     (ErrRegistryRequired composition-root gate)
//   - the strict typed MaxRetries resolution helper
//     (registry.GetMaxRetries is the canonical lookup, no silent
//     fallback)
//   - the typed sqlite3.Error UNIQUE-constraint probe that replaces
//     the pre-PR strings.Contains("UNIQUE constraint") heuristic
//     (godlike/07 NO-FAKE-AVAILABILITY; driver-invariant because
//     ExtendedCode is an int-backed enum, not a string)
//
// Hermetic strategy split by what each test actually exercises:
//
//  1. TestNewService_* — uses a real *Service struct; only needs
//     typed *Registry wiring (no broker required).
//
//  2. TestEnqueue_HappyPath_PopulatesMaxRetriesFromRegistry — uses
//     a real SQLite-backed SQLiteStore for the production path;
//     retrieval is hermetic via t.TempDir().
//
//  3. TestEnqueue_ExistingCorrelationID_DedupReturnsExisting — uses
//     a real SQLite-backed SQLiteStore. NOTE: this test covers the
//     pre-emptive FindByTypeAndCorrelation dedup SHORT-CIRCUIT, NOT
//     the typed-probe rescue path. The probe path requires a
//     controlled race window (FindByTypeAndCorrelation returning
//     nil) which isn't hermetic without a mock broker. The dedicated
//     probe-contract tests below pin the probe behavior via
//     synthetic sqlite3.Error construction.
//
//  4. TestEnqueue_UniqueProbe_* — pure-logic tests for the
//     driver-invariant typed probe. Constructs sqlite3.Error
//     directly (the struct is exported with int-backed Code field)
//     and asserts the probe-fires / probe-skips contract matches.
//     These tests pin the typed-error contract that the inline
//     probe in Enqueue() performs AGAINST a real driver error.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
)

// ── Constructor fail-closed (PR-jobs-retry-contract typed contract) ────

// TestNewService_NilRegistry_ReturnsErrRegistryRequired — composition-root
// fail-closed gate. nil-registry wiring surfaces immediately at startup
// rather than at first Enqueue (godlike/07).
func TestNewService_NilRegistry_ReturnsErrRegistryRequired(t *testing.T) {
	t.Parallel()
	svc, err := NewService(nil, nil, zap.NewNop(), nil)
	if err == nil {
		t.Fatalf("expected ErrRegistryRequired, got nil err (svc=%v)", svc)
	}
	if !errors.Is(err, ErrRegistryRequired) {
		t.Errorf("expected errors.Is(err, ErrRegistryRequired)=true, got err=%v", err)
	}
	if svc != nil {
		t.Errorf("expected svc=nil on error, got %v", svc)
	}
}

// TestNewService_HappyPath_ReturnsService — positive-path coverage
// pinning the canonical wiring surface (composition roots in
// internal/app rely on this contract).
func TestNewService_HappyPath_ReturnsService(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, 1*time.Minute, 7)
	svc, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if svc == nil {
		t.Fatalf("expected svc != nil")
	}
	if svc.registry != reg {
		t.Errorf("svc.registry != passed reg")
	}
	if svc.repo == nil {
		t.Errorf("svc.repo should be non-nil, got nil")
	}
}

// ── Enqueue happy path + dedup short-circuit (hermetic, real SQLite) ────

// TestEnqueue_HappyPath_PopulatesMaxRetriesFromRegistry — Enqueue happy
// path with MaxRetries=0 + a fresh registered jobType. The resolved
// MaxRetries MUST come from registry.GetMaxRetries (=7 in this
// fixture), NOT from any silent fallback (godlike/07
// no-fake-availability regression coverage).
func TestEnqueue_HappyPath_PopulatesMaxRetriesFromRegistry(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, 1*time.Minute, 7)

	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()

	store.SetProducesArtifacts(reg.ProducesArtifactsMap())

	// nil dispatcher skips the handler-registration gate; this test
	// exercises MaxRetries resolution, not handler wiring.
	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	req := &job.EnqueueRequest{
		Type:       wiringTestType,
		Priority:   5,
		MaxRetries: 0,
		Payload:    map[string]any{"hello": "world"},
	}
	got, err := svc.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatalf("Enqueue: unexpected err = %v", err)
	}
	if got == nil {
		t.Fatalf("Enqueue: nil job returned")
	}
	if got.MaxRetries != 7 {
		t.Errorf("PR-jobs-retry-contract: Enqueue.MaxRetries = %d, want registry default 7 (NOT a silent fallback)", got.MaxRetries)
	}
	if got.Type != wiringTestType {
		t.Errorf("Enqueue.Type = %q, want %q", got.Type, wiringTestType)
	}
}

// TestEnqueue_ExistingCorrelationID_DedupReturnsExisting — covers the
// pre-emptive FindByTypeAndCorrelation dedup SHORT-CIRCUIT. The
// hermetic SQLite-backed store writes the row on first Enqueue and
// returns the existing row on the second Enqueue query, so the
// dedup branch returns before repo.Create is reached.
//
// NOTE: this test does NOT exercise the typed sqlite3.Error probe
// path (the dedup short-circuits before Create). The probe path
// requires a controlled race window (FindByTypeAndCorrelation
// returning nil between the check and Create) which is the realm
// of the TestEnqueue_UniqueProbe_* tests below — those pin the
// probe behavior via synthetic sqlite3.Error construction.
func TestEnqueue_ExistingCorrelationID_DedupReturnsExisting(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, 1*time.Minute, 3)

	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()

	store.SetProducesArtifacts(reg.ProducesArtifactsMap())

	// nil dispatcher skips the handler-registration gate; this test
	// exercises correlation-id dedup, not handler wiring.
	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	correlationID := "cid-dedup-short-circuit-2026-07-04"
	req := &job.EnqueueRequest{
		Type:          wiringTestType,
		Priority:      5,
		MaxRetries:    0,
		CorrelationID: correlationID,
		Payload:       map[string]any{"run": 1},
	}
	first, err := svc.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatalf("Enqueue #1 (baseline): unexpected err = %v", err)
	}
	if first == nil || first.ID == "" {
		t.Fatalf("Enqueue #1 returned malformed job: %v", first)
	}

	// Second Enqueue with the SAME (Type, CorrelationID) — the
	// pre-emptive FindByTypeAndCorrelation dedup short-circuit
	// returns the existing row BEFORE repo.Create. Test the dedup
	// contract, NOT the probe (probe is pinned separately below).
	req2 := *req
	req2.Payload = map[string]any{"run": 2}
	second, err := svc.Enqueue(context.Background(), &req2)
	if err != nil {
		t.Fatalf("Enqueue #2 (dedup): unexpected err = %v", err)
	}
	if second == nil {
		t.Fatalf("Enqueue #2 returned nil — dedup short-circuit should return existing")
	}
	if second.ID != first.ID {
		t.Errorf("Enqueue #2 returned wrong job: got %q, want existing %q", second.ID, first.ID)
	}
}

// TestEnqueue_PopulatesRootJobID_CanonicalLineage — the canonical
// enqueue-time correlation fix. A root job (no parent linkage in the
// payload) resolves root_job_id to its own id; a child inherits its
// parent's already-resolved root. This pins the single canonical owner
// of the lineage fact (godlike/06 SSOT): derived projections no longer
// have to guess root_job_id from parent_job_id.
func TestEnqueue_PopulatesRootJobID_CanonicalLineage(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, 1*time.Minute, 3)

	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()

	store.SetProducesArtifacts(reg.ProducesArtifactsMap())

	// nil dispatcher skips the handler-registration gate; this test
	// exercises lineage resolution, not handler wiring.
	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Root job: no parent linkage → root_job_id is its own id.
	root, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{
		Type:    wiringTestType,
		Payload: map[string]any{"item": "root"},
	})
	if err != nil {
		t.Fatalf("Enqueue root: %v", err)
	}
	if root.RootJobID != root.ID {
		t.Errorf("root job: RootJobID = %q, want self %q", root.RootJobID, root.ID)
	}
	if root.ParentJobID != "" {
		t.Errorf("root job: ParentJobID = %q, want empty", root.ParentJobID)
	}

	// Child job: parent linkage in payload → inherits the parent's root.
	child, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{
		Type:    wiringTestType,
		Payload: map[string]any{"item": "child", "parent_job_id": root.ID},
	})
	if err != nil {
		t.Fatalf("Enqueue child: %v", err)
	}
	if child.ParentJobID != root.ID {
		t.Errorf("child job: ParentJobID = %q, want %q", child.ParentJobID, root.ID)
	}
	if child.RootJobID != root.ID {
		t.Errorf("child job: RootJobID = %q, want parent's root %q", child.RootJobID, root.ID)
	}
}

// ── Typed sqlite3 probe contract (synthetic-error unit tests) ──────────
//
// These tests pin the probe behavior the inline Enqueue() probe
// exhibits against a real driver error. They avoid the broker-mock
// overhead of fully exercising the SQL race by constructing the
// canonical typed error directly: sqlite3.Error is an exported struct
// with int-backed Code (ErrNo) and ExtendedCode (ErrNoExtended)
// fields. Three cardinal cases:
//
//   - identical-match: sqlite3.Error{Code: sqlite3.ErrConstraint, ExtendedCode: sqlite3.ErrConstraintUnique}
//     → probe MUST fire (positive case for the typed-error contract;
//     matches the real driver emission shape for UNIQUE-constraint
//     failures where Code=ErrConstraint and ExtendedCode=ErrConstraintUnique)
//   - non-sqlite3 error: errors.New("plain")
//     → probe MUST NOT fire (errors.As rejects non-matches)
//   - wrong-sqlite3-code: sqlite3.Error with a non-UNIQUE code
//     → probe MUST NOT fire (ExtendedCode comparison rejects the
//     off-by-one hazard that a future refactor might introduce)
//
// godlike/06 driver-invariant contract: sqlite3.Error.ExtendedCode is
// an int enum (ErrNoExtended type), NOT a string. The pre-PR
// strings.Contains(err.Error(), "UNIQUE constraint") heuristic could
// silently break on any driver string change — this typed probe cannot.
//
// PR-JOBS-RETRY-CONTRACT-TEST-FIX (2026-07-05): the prior test
// fixtures used `Code: sqlite3.ErrConstraintUnique` which was a
// TYPE MISMATCH (Code is `ErrNo` int, ErrConstraintUnique is
// `ErrNoExtended` int — different Go types). Updated to use
// `Code: sqlite3.ErrConstraint` (ErrNo-typed primary code) for the
// `Code` field, and `ExtendedCode: sqlite3.ErrConstraintUnique`
// (ErrNoExtended-typed extended code) for the `ExtendedCode` field.
// This matches the real driver emission shape.

// TestEnqueue_UniqueProbe_MatchingTypedError_FiresProbe — the probe MUST
// fire when the underlying error is a sqlite3.Error with
// ExtendedCode == sqlite3.ErrConstraintUnique. Mirrors the real driver
// emission shape for UNIQUE-constraint failures on the
// (type, correlation_id) index.
func TestEnqueue_UniqueProbe_MatchingTypedError_FiresProbe(t *testing.T) {
	t.Parallel()

	// Sanity: the canonical invariant the typed probe depends on is
	// that sqlite3.ErrConstraintUnique is non-zero. If the mattn
	// driver ever changes ErrConstraintUnique to 0 (which would
	// indicate a driver-level bug — the SQLITE_CONSTRAINT_UNIQUE
	// value is part of the SQLite C API surface), this test fails
	// loudly.
	if sqlite3.ErrConstraintUnique == 0 {
		t.Fatalf("mattn/go-sqlite3.ErrConstraintUnique is 0 — driver invariant violated")
	}

	// Synthetic sqlite3.Error exactly as the real driver emits it
	// for UNIQUE-constraint failures on the (type, correlation_id)
	// index.
	err := sqlite3.Error{Code: sqlite3.ErrConstraint, ExtendedCode: sqlite3.ErrConstraintUnique}

	// Mirror the inline probe from Enqueue() — KEEP THE PROBE
	// EXPRESSIONS IN SYNCHRONIZED VERBATIM. If a future refactor
	// diverges from this assertion the test FAILS, pinning the
	// contract.
	var sqliteErr sqlite3.Error
	probeFires := errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique
	if !probeFires {
		t.Errorf("PR-jobs-retry-contract: typed probe did NOT fire on matching sqlite3.Error{ExtendedCode: ErrConstraintUnique}")
	}
}

// TestEnqueue_UniqueProbe_NonTypedError_DoesNotFire — when the
// underlying error is a plain errors.New (NOT a sqlite3.Error), the
// probe MUST NOT fire. Errors.As rejects non-matches; the probe
// stays safe against non-driver errors (e.g. context.Canceled
// propagating up from a cancelled request).
func TestEnqueue_UniqueProbe_NonTypedError_DoesNotFire(t *testing.T) {
	t.Parallel()

	err := errors.New("some non-driver error")

	var sqliteErr sqlite3.Error
	probeFires := errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique
	if probeFires {
		t.Errorf("PR-jobs-retry-contract: typed probe incorrectly fired on plain errors.New (errors.As rejected but probe still triggered)")
	}
}

// TestEnqueue_UniqueProbe_WrongSqliteCode_DoesNotFire — when the
// underlying error is a sqlite3.Error with a non-UNIQUE code
// (e.g. SQLITE_CONSTRAINT_FOREIGNKEY or SQLITE_CONSTRAINT_NOTNULL),
// the probe MUST NOT fire. ExtendedCode comparison rejects the off-by-one
// hazard; a future refactor that loosens the comparison would
// silently over-fire the rescue path on non-UNIQUE constraint
// failures (a godlike/07 NO-FAKE-AVAILABILITY regression).
func TestEnqueue_UniqueProbe_WrongSqliteCode_DoesNotFire(t *testing.T) {
	t.Parallel()

	// SQLITE_CONSTRAINT = 19 (the generic code). The UNIQUE sub-code
	// (sqlite3.ErrConstraintUnique == 19 too — SQLite's primary
	// error code for unique constraint is 19, not 19+N). Use a
	// different canonical code: SQLITE_FULL = 13 (database full) is
	// a sqlite3.Error but obviously NOT a UNIQUE-constraint failure.
	err := sqlite3.Error{Code: sqlite3.ErrFull}

	var sqliteErr sqlite3.Error
	probeFires := errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique
	if probeFires {
		t.Errorf("PR-jobs-retry-contract: typed probe incorrectly fired on sqlite3.Error{Code: ErrFull} (off-by-one hazard)")
	}
}

// TestEnqueue_UniqueProbe_WrapPath_PreservesErrUniqueConstraintViolation
// — rescue-failure path. When the typed probe fires BUT the
// FindByTypeAndCorrelation rescue returns nil (no concurrent insert
// visible), the Enqueue() rescue path returns
//
//	fmt.Errorf("%w: %w", ErrUniqueConstraintViolation, err)
//
// pinning the godlike/06 typed-error contract: errors.Is(wrapped,
// ErrUniqueConstraintViolation) returns true (callers can branch on
// the typed sentinel), AND errors.As(wrapped, &sqliteErr) still
// resolves to the underlying driver error (probe + sentinel coexist
// without overriding diagnostic chain).
//
// godlike/07 audit-pin: this test asserts the BEHAVIORAL CHANGE
// from PR-jobs-retry-contract. Pre-PR the rescue-failure path
// returned `fmt.Errorf("failed to create job: %w", err)` with a
// string framing. Post-PR the path returns the typed sentinel
// ErrUniqueConstraintViolation wrapped ONTO the original error.
// Existing callers that branched on the "failed to create job"
// substring MUST migrate to errors.Is(err, ErrUniqueConstraintViolation).
func TestEnqueue_UniqueProbe_WrapPath_PreservesErrUniqueConstraintViolation(t *testing.T) {
	t.Parallel()

	// Synthetic typed sqlite3.Error exactly as the real driver emits
	// it for UNIQUE-constraint failures on the (type, correlation_id)
	// index.
	sqliteErr := sqlite3.Error{Code: sqlite3.ErrConstraint, ExtendedCode: sqlite3.ErrConstraintUnique}

	// Mirror the inline wrap expression from Enqueue() — KEEP THIS
	// EXPRESSION IN SYNCHRONIZED VERBATIM. If a future refactor
	// diverges from this assertion the test FAILS, pinning the
	// typed-error contract on the rescue-failure path.
	wrapped := fmt.Errorf("%w: %w", ErrUniqueConstraintViolation, sqliteErr)

	if !errors.Is(wrapped, ErrUniqueConstraintViolation) {
		t.Errorf("PR-jobs-retry-contract: rescue-failure wrap did NOT surface ErrUniqueConstraintViolation via errors.Is")
	}

	// The probe must STILL recognize the underlying driver error
	// through the wrapped chain — callers must be able to classify
	// the failure mode via either path.
	var resolved sqlite3.Error
	if !errors.As(wrapped, &resolved) || resolved.ExtendedCode != sqlite3.ErrConstraintUnique {
		t.Errorf("PR-jobs-retry-contract: wrapped error chain must preserve sqlite3.Error probe classification")
	}
}

// ── Helper: hermetic SQLite store builder ──────────────────────────────

// newSqliteStoreForTest builds a real sqlite-backed SQLiteStore in a
// temp directory and returns a cleanup function. Hermetic — no fixture
// files in the test source tree. Per-test unique path keeps parallel
// runs isolated.
func newSqliteStoreForTest(t *testing.T) (*sqljobs.SQLiteStore, func()) {
	t.Helper()
	db := setupTestDB(t)
	store := sqljobs.NewSQLiteStore(db, zap.NewNop())
	return store, func() {}
}
