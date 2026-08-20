package storage

import "testing"

func TestMigrations_228_EntityImageRecertificationState(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	columns := scanColumnNames(t, db, "entity_image_catalog_candidates")
	for _, column := range []string{"validation_attempts", "last_validation_at", "next_retry_at", "last_validation_error"} {
		if _, ok := columns[column]; !ok {
			t.Fatalf("migration 228 missing %q", column)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO entity_image_catalog_entities(canonical_entity_id, entity_type, canonical_name)
		VALUES ('person:michael-jordan', 'PERSON', 'Michael Jordan')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO entity_image_catalog_candidates(
		 canonical_entity_id, provider, rank, source_url, status, semantic_status,
		 validation_attempts, last_validation_error)
		VALUES ('person:michael-jordan', 'duckduckgo', 1,
		 'https://images.example/mj.png', 'broken', 'accepted', 2, 'HTTP 503')
	`); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var validationError string
	if err := db.QueryRow(`SELECT validation_attempts, last_validation_error FROM entity_image_catalog_candidates`).Scan(&attempts, &validationError); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || validationError != "HTTP 503" {
		t.Fatalf("retry metadata = attempts:%d error:%q", attempts, validationError)
	}
	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_entity_image_catalog_candidates_recertification'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatal("recertification index missing")
	}
}
