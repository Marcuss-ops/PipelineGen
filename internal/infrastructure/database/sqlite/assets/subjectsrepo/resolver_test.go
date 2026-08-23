package subjectsrepo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/subjects"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// openTestDB opens a SQLite DB on a t.TempDir() disk file and applies
// the canonical migrations up to and including migration 180 (the
// subjects-promotion migration this PR introduces).
//
// We use a file-backed DB (not `:memory:`) because `sql.Open(...)
// + RunMigrations` on `:memory:` creates a new in-memory DB on
// every connection roundtrip in the runner; the canonical runner
// needs a stable on-disk file to atomically apply the ledger.
//
// `*storage.SQLiteDB.RunMigrations` is the canonical entry point —
// it owns the migration runner, the schema_migrations ledger, and
// the `targetDB="primary"` / `targetDB="observability"` scope
// gating. The signature was confirmed against
// internal/infrastructure/database/migrations.go:60.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		t.Fatalf("ping test db: %v", err)
	}

	// Apply the production migrations up through 180 (the migration
	// that defines the canonical subjects table shape). The runner
	// takes a *zap.Logger; we use zap.NewNop() to silence the
	// migration-by-migration chatter in the test surface.
	migDir := "../../../../../../migrations/sqlite"
	if mg := os.Getenv("PG_MIGRATIONS_DIR"); mg != "" {
		migDir = mg
	}
	sqldb := &storage.SQLiteDB{DB: db}
	if err := sqldb.RunMigrations(zap.NewNop(), migDir, "primary"); err != nil {
		t.Fatalf("apply migrations via RunMigrations: %v", err)
	}
	return db
}

// ── Tests ───────────────────────────────────────────────────────────────────

// TestResolver_Canonical_CasingAndWhitespaceInvariants asserts the
// canonical Sugar-Ray-Robinson invariant: every variant spelling
// (case, whitespace, leading/trailing space) resolves to the SAME
// UUID (and hence the same Subject).
//
// This is the regression test for the diagnostic that motivated
// the migration: "Sugar Ray Robinson" / "sugar ray robinson" /
// "SUGAR RAY ROBINSON" / "  sugar ray robinson  " each produced
// a separate count because there was no canonical identity layer.
func TestResolver_Canonical_CasingAndWhitespaceInvariants(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	r := NewResolver(db)

	variants := []string{
		"Sugar Ray Robinson",
		"sugar ray robinson",
		"SUGAR RAY ROBINSON",
		"  sugar ray robinson  ",
		"sugar  ray  robinson",   // double-spaces collapse
		"\tSugar Ray Robinson\t", // tabs collapse to "Sugar Ray Robinson"
	}

	// First call: LookupOrCreate.
	first, err := r.LookupOrCreate(ctx, variants[0])
	if err != nil {
		t.Fatalf("LookupOrCreate(%q): %v", variants[0], err)
	}
	if first.UUID == "" {
		t.Fatalf("first LookupOrCreate returned empty UUID")
	}
	expectedUUID := first.UUID

	for _, v := range variants {
		got, lerr := r.Lookup(ctx, v)
		if lerr != nil {
			t.Errorf("Lookup(%q): %v", v, lerr)
			continue
		}
		if got.UUID != expectedUUID {
			t.Errorf("Lookup(%q) UUID = %q, want %q", v, got.UUID, expectedUUID)
		}
		// Re-create on LookupOrCreate must NOT produce a second row.
		re, loerr := r.LookupOrCreate(ctx, v)
		if loerr != nil {
			t.Errorf("LookupOrCreate(%q) [re-call]: %v", v, loerr)
			continue
		}
		if re.UUID != expectedUUID {
			t.Errorf("LookupOrCreate(%q) [re-call] UUID = %q, want %q", v, re.UUID, expectedUUID)
		}
	}

	// Sanity: exactly one row in DB for Sugar Ray Robinson.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM subjects WHERE display_name_norm LIKE '%sugar%ray%robinson%'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("subjects rows for canonical Sugar Ray Robinson = %d, want 1", count)
	}
}

// TestResolver_Lookup_NotFound asserts Lookup(MISSING) returns the
// typed ErrSubjectNotFound (godlike/07 typed-error contract).
func TestResolver_Lookup_NotFound(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	r := NewResolver(db)

	_, err := r.Lookup(ctx, "This Subject Does Not Exist")
	if !errors.Is(err, subjects.ErrSubjectNotFound) {
		t.Fatalf("Lookup(missing) err = %v, want ErrSubjectNotFound", err)
	}
}

// TestResolver_LookupOrCreate_EmptyFailsClosed asserts the empty-input
// invariant: LookupOrCreate("  ") MUST NOT create a row and MUST
// return ErrSubjectNotFound (godlike/07 NO-FAKE-AVAILABILITY).
func TestResolver_LookupOrCreate_EmptyFailsClosed(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	r := NewResolver(db)

	for _, in := range []string{"", "   ", "\t", " ", "  \t  "} {
		got, err := r.LookupOrCreate(ctx, in)
		if !errors.Is(err, subjects.ErrSubjectNotFound) {
			t.Errorf("LookupOrCreate(%q) err = %v, want ErrSubjectNotFound", in, err)
			continue
		}
		if got != nil {
			t.Errorf("LookupOrCreate(%q) returned non-nil Subject on miss: %+v", in, got)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM subjects`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("subjects rows after empty-input attempts = %d, want 0", count)
	}
}

// TestResolver_LookupOrCreate_DistinctSubjects asserts two distinct
// canonical names produce two distinct UUIDs.
func TestResolver_LookupOrCreate_DistinctSubjects(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	r := NewResolver(db)

	a, err := r.LookupOrCreate(ctx, "Mike Tyson")
	if err != nil {
		t.Fatalf("LookupOrCreate(Mike Tyson): %v", err)
	}
	b, err := r.LookupOrCreate(ctx, "Floyd Mayweather")
	if err != nil {
		t.Fatalf("LookupOrCreate(Floyd Mayweather): %v", err)
	}

	if a.UUID == b.UUID {
		t.Errorf("Mike Tyson and Floyd Mayweather share UUID %q — should be distinct", a.UUID)
	}
	if a.Slug == b.Slug {
		t.Errorf("Mike Tyson and Floyd Mayweather share slug %q", a.Slug)
	}
	if a.DisplayNameNorm == b.DisplayNameNorm {
		t.Errorf("Mike Tyson and Floyd Mayweather share display_name_norm %q", a.DisplayNameNorm)
	}

	// Variant casing of the second must resolve to the SAME UUID as
	// the original Floyd Mayweather.
	c, err := r.LookupOrCreate(ctx, "FLOYD MAYWEATHER")
	if err != nil {
		t.Fatalf("LookupOrCreate(FLOYD MAYWEATHER): %v", err)
	}
	if c.UUID != b.UUID {
		t.Errorf("FLOYD MAYWEATHER variant UUID = %q, want %q (Floyd Mayweather)", c.UUID, b.UUID)
	}
}

// TestResolver_LookupOrCreate_IdempotentReCalls asserts the race-safe
// UPSERT semantics: a second LookupOrCreate with the same canonical
// name MUST NOT produce a second row.
func TestResolver_LookupOrCreate_IdempotentReCalls(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	r := NewResolver(db)

	first, err := r.LookupOrCreate(ctx, "Muhammad Ali")
	if err != nil {
		t.Fatalf("first LookupOrCreate: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := r.LookupOrCreate(ctx, "Muhammad Ali")
		if err != nil {
			t.Fatalf("LookupOrCreate #%d: %v", i, err)
		}
		if again.UUID != first.UUID {
			t.Errorf("LookupOrCreate #%d UUID = %q, want %q", i, again.UUID, first.UUID)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM subjects`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("subjects rows after 6 LookupOrCreate(Muhammad Ali) = %d, want 1", count)
	}
}
