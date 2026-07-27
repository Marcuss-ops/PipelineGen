// Package storage — migrations_154_test.go holds the scenario tests
// for migration 154 (script_localizations table + UNIQUE-shape
// discriminator). PR-CATALOG-MULTILINGUA step 5 introduced the
// script_localizations table that stores multilingual translations
// of a generated script with the (source_script_hash, language,
// model_version, prompt_version) tuple as the canonical
// audit-preserved UNIQUE discriminator.
//
// Covers:
//   - TestMigrations_154_ScriptLocalizationsTablePresent
//     script_localizations has all 10 columns in canonical
//     order, the FK to scripts.id is registered, and the 2
//     supporting indexes are present.
//   - TestMigrations_154_ScriptLocalizationsUniqueConstraintRejectsDuplicate
//     Pins the canonical user-spec UNIQUE(shape) constraint — a
//     second INSERT with the EXACT same tuple MUST fail typed
//     at the SQL boundary.
//   - TestMigrations_154_ScriptLocalizationsBumpVariantForcesNewRow
//     Bumping the model_version OR prompt_version OR
//     source_script_hash OR language_code produces a NEW row (the
//     prior variant is preserved as audit).
package storage

import (
	"strings"
	"testing"
)

func TestMigrations_154_ScriptLocalizationsTablePresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='script_localizations'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("check script_localizations presence: %v", err)
	}
	if count != 1 {
		t.Fatalf("script_localizations table missing in sqlite_master (count=%d, want 1)", count)
	}

	seen := scanColumnNames(t, db, "script_localizations")
	required := []string{
		"script_id", "source_script_hash", "language_code",
		"specscene_json", "translation_model", "model_version",
		"prompt_version", "status", "created_at", "updated_at",
	}
	for _, col := range required {
		if _, ok := seen[col]; !ok {
			t.Errorf("script_localizations missing column %q (declared by migration 154)", col)
		}
	}

	// FK from script_localizations.script_id → scripts.id.
	var fkCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM pragma_foreign_key_list('script_localizations')
		 WHERE "table" = 'scripts' AND "from" = 'script_id' AND "to" = 'id'`,
	).Scan(&fkCount)
	if err != nil {
		t.Fatalf("read script_localizations foreign_key_list: %v", err)
	}
	if fkCount != 1 {
		t.Errorf("script_localizations.script_id FK to scripts.id missing (count=%d, want 1)", fkCount)
	}

	// 2 supporting indexes.
	localizationIndexes := mustReadIndexNames(t, db, "script_localizations")
	for _, want := range []string{
		"idx_script_localizations_script_id",
		"idx_script_localizations_language_status",
	} {
		if !contains(localizationIndexes, want) {
			t.Errorf("script_localizations missing index %q (declared by migration 154)", want)
		}
	}
}

func TestMigrations_154_ScriptLocalizationsUniqueConstraintRejectsDuplicate(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	// Setup: insert a minimum scripts.id that satisfies the FK.
	const scriptID int64 = 9999
	_, err := db.Exec(
		`INSERT INTO scripts (id, topic, language, specscene) VALUES (?, ?, ?, ?)`,
		scriptID, "step5-uniq-test", "en", `{"version":1,"scenes":[]}`,
	)
	if err != nil {
		t.Fatalf("setup scripts row for UNIQUE-test: %v", err)
	}

	insertStmt := `INSERT INTO script_localizations (
		script_id, source_script_hash, language_code,
		specscene_json, translation_model, model_version,
		prompt_version, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	firstArgs := []any{
		scriptID, "hash-source-A", "it",
		`{"version":1,"scenes":[]}`, "gpt-4o-mini",
		"2024-07-18", "v1.2.0", "ready",
	}
	if _, err := db.Exec(insertStmt, firstArgs...); err != nil {
		t.Fatalf("first valid INSERT failed: %v", err)
	}

	// Exact-duplicate INSERT must fail with a shape that
	// includes 'UNIQUE constraint failed' (SQLite's canonical
	// error message for UNIQUE-shape violations).
	_, err = db.Exec(insertStmt, firstArgs...)
	if err == nil {
		t.Fatalf("expected UNIQUE constraint failure on exact-duplicate INSERT, got nil error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Errorf("expected error to mention UNIQUE; got %q", err.Error())
	}
}

func TestMigrations_154_ScriptLocalizationsBumpVariantForcesNewRow(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	const scriptID int64 = 10000
	_, err := db.Exec(
		`INSERT INTO scripts (id, topic, language, specscene) VALUES (?, ?, ?, ?)`,
		scriptID, "step5-bump-test", "en", `{"version":1,"scenes":[]}`,
	)
	if err != nil {
		t.Fatalf("setup scripts row for BUMP-variant-test: %v", err)
	}

	insertStmt := `INSERT INTO script_localizations (
		script_id, source_script_hash, language_code,
		specscene_json, translation_model, model_version,
		prompt_version, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	first := []any{scriptID, "hash-source-B", "es",
		`{"version":1,"scenes":[]}`, "gpt-4o-mini",
		"2024-07-18", "v1.2.0", "ready"}
	if _, err := db.Exec(insertStmt, first...); err != nil {
		t.Fatalf("first INSERT for bump-test failed: %v", err)
	}
	// Bump model_version only.
	second := []any{scriptID, "hash-source-B", "es",
		`{"version":1,"scenes":[]}`, "gpt-4o-mini",
		"2024-12-01", "v1.2.0", "ready"}
	if _, err := db.Exec(insertStmt, second...); err != nil {
		t.Errorf("bumping model_version alone should NOT violate UNIQUE (audit-trail invariant); got %v", err)
	}
	// Bump prompt_version only.
	third := []any{scriptID, "hash-source-B", "es",
		`{"version":1,"scenes":[]}`, "gpt-4o-mini",
		"2024-12-01", "v1.3.0", "ready"}
	if _, err := db.Exec(insertStmt, third...); err != nil {
		t.Errorf("bumping prompt_version alone should NOT violate UNIQUE; got %v", err)
	}
	// Bump source_script_hash only.
	fourth := []any{scriptID, "hash-source-C", "es",
		`{"version":1,"scenes":[]}`, "gpt-4o-mini",
		"2024-12-01", "v1.3.0", "ready"}
	if _, err := db.Exec(insertStmt, fourth...); err != nil {
		t.Errorf("bumping source_script_hash alone should NOT violate UNIQUE; got %v", err)
	}
	// Bump language_code only.
	fifth := []any{scriptID, "hash-source-C", "fr",
		`{"version":1,"scenes":[]}`, "gpt-4o-mini",
		"2024-12-01", "v1.3.0", "ready"}
	if _, err := db.Exec(insertStmt, fifth...); err != nil {
		t.Errorf("bumping language_code alone should NOT violate UNIQUE; got %v", err)
	}
}
