package jobs

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestPreparationLegacyCleanupBackfillsCanonicalFields(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE preparation_units (unit_fingerprint TEXT NOT NULL, fingerprint TEXT NOT NULL DEFAULT ''); CREATE TABLE preparation_attempts (workload_dimension TEXT NOT NULL DEFAULT '', workload_amount REAL NOT NULL DEFAULT 0, status TEXT NOT NULL, finished_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO preparation_units(unit_fingerprint) VALUES ('canonical-fp'); INSERT INTO preparation_attempts(workload_amount,status) VALUES (-1,'READY')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE preparation_units SET fingerprint=unit_fingerprint WHERE fingerprint='' AND unit_fingerprint<>''; UPDATE preparation_attempts SET workload_dimension='', workload_amount=0 WHERE workload_amount<0`); err != nil {
		t.Fatal(err)
	}
	var fingerprint string
	if err := db.QueryRowContext(context.Background(), `SELECT fingerprint FROM preparation_units`).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if fingerprint != "canonical-fp" {
		t.Fatalf("fingerprint=%q", fingerprint)
	}
	var amount float64
	if err := db.QueryRow(`SELECT workload_amount FROM preparation_attempts`).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if amount != 0 {
		t.Fatalf("workload_amount=%v, want 0", amount)
	}
}
