package storage

import "testing"

// TestMigrations_200_SourceIdentityRegistry verifies that migration
// 200_source_identity_registry.sql creates the source -> content SHA-256
// mapping table with the (source_type, source_key) composite primary key,
// and that the upsert + reverse lookup behave on a fresh primary-DB apply.
func TestMigrations_200_SourceIdentityRegistry(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	// 1. Table exists with the canonical column set.
	cols := scanColumnNames(t, db, "source_identity_registry")
	for _, want := range []string{
		"source_type",
		"source_key",
		"content_sha256",
		"source_version",
		"discovered_at",
		"last_seen_at",
		"verification_status",
	} {
		if _, ok := cols[want]; !ok {
			t.Fatalf("source_identity_registry missing column %q after migration 200 (present: %v)", want, cols)
		}
	}

	// 2. (source_type, source_key) is the composite primary key.
	var pkColumns []string
	rows, err := db.Query(`SELECT name FROM pragma_table_info('source_identity_registry') WHERE pk > 0 ORDER BY pk`)
	if err != nil {
		t.Fatalf("read source_identity_registry pk: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		pkColumns = append(pkColumns, name)
	}
	if len(pkColumns) != 2 || pkColumns[0] != "source_type" || pkColumns[1] != "source_key" {
		t.Fatalf("source_identity_registry primary key = %v, want [source_type source_key]", pkColumns)
	}

	// 3. Record + upsert: re-inserting the same (type, key) refreshes the
	//    mapping instead of duplicating.
	insert := `INSERT INTO source_identity_registry
		(source_type, source_key, content_sha256, source_version, discovered_at, last_seen_at, verification_status)
		VALUES ('drive', '1Gp1ue8', 'a1f8c72e', 'etag-v1', '2026-08-12T00:00:00Z', '2026-08-12T00:00:00Z', 'UNVERIFIED')
		ON CONFLICT(source_type, source_key) DO UPDATE SET
			content_sha256 = excluded.content_sha256,
			source_version = excluded.source_version,
			last_seen_at    = excluded.last_seen_at,
			verification_status = excluded.verification_status`
	if _, err := db.Exec(insert); err != nil {
		t.Fatalf("record identity: %v", err)
	}
	if _, err := db.Exec(`UPDATE source_identity_registry SET content_sha256 = 'b2f8d999', source_version = 'etag-v2', verification_status = 'VERIFIED' WHERE source_type = 'drive' AND source_key = '1Gp1ue8'`); err != nil {
		t.Fatalf("refresh identity: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_identity_registry WHERE source_type = 'drive' AND source_key = '1Gp1ue8'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("source_identity_registry rows for one source = %d, want 1", count)
	}

	var (
		sha    string
		ver    string
		status string
	)
	if err := db.QueryRow(`SELECT content_sha256, source_version, verification_status FROM source_identity_registry WHERE source_type = 'drive' AND source_key = '1Gp1ue8'`).
		Scan(&sha, &ver, &status); err != nil {
		t.Fatal(err)
	}
	if sha != "b2f8d999" || ver != "etag-v2" || status != "VERIFIED" {
		t.Fatalf("refreshed identity = (%s, %s, %s), want (b2f8d999, etag-v2, VERIFIED)", sha, ver, status)
	}

	// 4. Reverse lookup index: content -> known sources.
	if _, err := db.Exec(`INSERT INTO source_identity_registry
		(source_type, source_key, content_sha256, discovered_at, last_seen_at)
		VALUES ('artlist', '9281', 'b2f8d999', '2026-08-12T00:00:00Z', '2026-08-12T00:00:00Z')`); err != nil {
		t.Fatalf("record second identity: %v", err)
	}
	var byContent int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_identity_registry WHERE content_sha256 = 'b2f8d999'`).Scan(&byContent); err != nil {
		t.Fatal(err)
	}
	if byContent != 2 {
		t.Fatalf("sources resolved by content = %d, want 2 (two sources, one content)", byContent)
	}
}
