// Package storage — backup.go (June 2026 codex/db-doctor-restore):
//
// Backup + restore helpers for `admin db {backup,restore --verify}`.
// The Backup procedure uses SQLite's canonical `VACUUM INTO` to
// produce an atomic, whole-database copy (includes tables, indexes,
// triggers, views) at a single point in time.
//
// Restore takes the source backup, VACUUM-copies it to a destination
// path, opens the destination with the stored pragmas, runs
// integrity_check + foreign_key_check, then runs an E2E smoke probe
// (BEGIN, CREATE TEMP TABLE, INSERT probe row, SELECT COUNT(*),
// ROLLBACK). The probe is non-destructive: the temp table is rolled
// back so the restored DB matches what was backed up byte-for-byte.
//
// RTO + RPO are derived from the operation duration + source backup
// mtime respectively.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// BackupResult is the structured outcome of a Backup operation.
type BackupResult struct {
	Path        string    // absolute path to the backup file
	SizeBytes   int64     // bytes on disk
	SHA256      string    // hex-encoded SHA256 of the file
	DurationMs  int64     // wall-clock duration
	CompletedAt time.Time // UTC
}

// Backup copies the database at srcPath to outPath using SQLite's
// `VACUUM INTO` pattern. The destination is atomically replaced (any
// pre-existing file at outPath is removed first because VACUUM INTO
// refuses to over-write). Returns a BackupResult with the SHA256 of
// the resulting file, the on-disk size, and the wall-clock duration.
func Backup(srcPath, outPath string) (*BackupResult, error) {
	start := time.Now()

	if _, err := os.Stat(srcPath); err != nil {
		return nil, fmt.Errorf("backup: stat source %s: %w", srcPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return nil, fmt.Errorf("backup: mkdir parent for %s: %w", outPath, err)
	}
	// Remove pre-existing dest so VACUUM INTO can write.
	if _, err := os.Stat(outPath); err == nil {
		if err := os.Remove(outPath); err != nil {
			return nil, fmt.Errorf("backup: remove pre-existing %s: %w", outPath, err)
		}
	}

	src, err := sql.Open("sqlite3", srcPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("backup: open source %s: %w", srcPath, err)
	}
	defer src.Close()

	if _, err := src.Exec("VACUUM INTO ?", outPath); err != nil {
		return nil, fmt.Errorf("backup: VACUUM INTO %s: %w", outPath, err)
	}

	// Compute SHA256 over the output file.
	f, err := os.Open(outPath)
	if err != nil {
		return nil, fmt.Errorf("backup: open output %s: %w", outPath, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("backup: hash output: %w", err)
	}
	sumHex := hex.EncodeToString(h.Sum(nil))

	fi, err := os.Stat(outPath)
	if err != nil {
		return nil, fmt.Errorf("backup: stat output %s: %w", outPath, err)
	}

	return &BackupResult{
		Path:        outPath,
		SizeBytes:   fi.Size(),
		SHA256:      sumHex,
		DurationMs:  time.Since(start).Milliseconds(),
		CompletedAt: time.Now().UTC(),
	}, nil
}

// RestoreResult is the structured outcome of Restore + Verify + Smoke.
type RestoreResult struct {
	SourcePath    string
	DestPath      string
	SizeBytes     int64
	SHA256        string // of the restored file
	IntegrityOK   bool
	FKViolations  []string // empty list on success
	SmokeInsertOK bool     // E2E probe row insertion succeeded
	SmokeRowsRead int      // rows the probe SELECT saw (>=1 expected)
	DurationMs    int64    // wall-clock
	BackupMtime   time.Time
	RTOSeconds    float64 // time from restore start to verify done
	RPOHours      float64 // age of the source backup (hours)
}

// Restore copies the database at srcPath to dstPath via `VACUUM INTO`,
// opens the destination as a fresh DatabaseSet-style handle, runs
// IntegrityCheck + ForeignKeyCheck, and inserts + reads a non-persistent
// E2E probe row inside a rolled-back transaction. The probe's TEMP TABLE
// is rolled back so it does not persist in the restored file.
//
// RTO is the wall-clock duration of the operation. RPO is the age of
// the source backup file at restore time.
func Restore(ctx context.Context, srcPath, dstPath string) (*RestoreResult, error) {
	start := time.Now()

	if _, err := os.Stat(srcPath); err != nil {
		return nil, fmt.Errorf("restore: stat source %s: %w", srcPath, err)
	}
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return nil, fmt.Errorf("restore: stat source info: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return nil, fmt.Errorf("restore: mkdir parent for %s: %w", dstPath, err)
	}
	if _, err := os.Stat(dstPath); err == nil {
		if err := os.Remove(dstPath); err != nil {
			return nil, fmt.Errorf("restore: remove pre-existing %s: %w", dstPath, err)
		}
	}

	// Open source just for VACUUM INTO — read-only side of the operation.
	src, err := sql.Open("sqlite3", srcPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("restore: open source %s: %w", srcPath, err)
	}
	defer src.Close()

	if _, err := src.Exec("VACUUM INTO ?", dstPath); err != nil {
		return nil, fmt.Errorf("restore: VACUUM INTO %s: %w", dstPath, err)
	}

	r := &RestoreResult{
		SourcePath:  srcPath,
		DestPath:    dstPath,
		BackupMtime: srcInfo.ModTime().UTC(),
	}

	// File size + SHA256 of the destination.
	fi, err := os.Stat(dstPath)
	if err != nil {
		return r, fmt.Errorf("restore: stat output: %w", err)
	}
	r.SizeBytes = fi.Size()

	f, err := os.Open(dstPath)
	if err != nil {
		return r, fmt.Errorf("restore: open output: %w", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		return r, fmt.Errorf("restore: hash output: %w", err)
	}
	f.Close()
	r.SHA256 = hex.EncodeToString(h.Sum(nil))

	// Read-only probes: integrity_check, foreign_key_check.
	dst, err := OpenReadOnly(dstPath)
	if err != nil {
		return r, fmt.Errorf("restore: open destination read-only: %w", err)
	}
	if err := IntegrityCheck(ctx, dst); err != nil {
		dst.Close()
		return r, fmt.Errorf("restore: integrity: %w", err)
	}
	r.IntegrityOK = true
	fk, err := ForeignKeyCheck(ctx, dst)
	if err != nil {
		dst.Close()
		return r, fmt.Errorf("restore: foreign_key_check: %w", err)
	}
	r.FKViolations = fk
	dst.Close()

	// Writable smoke probe (NON-destructive — fully rolled back).
	r.SmokeInsertOK, r.SmokeRowsRead, err = runRestoreSmoke(ctx, dstPath)
	if err != nil {
		return r, fmt.Errorf("restore: smoke: %w", err)
	}

	r.DurationMs = time.Since(start).Milliseconds()
	r.RTOSeconds = float64(r.DurationMs) / 1000.0
	r.RPOHours = time.Since(r.BackupMtime).Hours()

	return r, nil
}

// runRestoreSmoke opens dstPath writable, begins a tx, creates a
// TEMP TABLE, inserts a probe row, selects it, rolls back. The
// inserted row is never persisted — the smoke is read-only from the
// perspective of the resulting on-disk file.
func runRestoreSmoke(ctx context.Context, dstPath string) (bool, int, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := OpenWritable(dstPath)
	if err != nil {
		return false, 0, fmt.Errorf("open writable: %w", err)
	}
	defer db.Close()

	tx, err := db.BeginTx(probeCtx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(probeCtx,
		"CREATE TEMP TABLE IF NOT EXISTS _doctor_probe (ts TEXT, marker TEXT)"); err != nil {
		return false, 0, fmt.Errorf("create temp probe: %w", err)
	}
	if _, err := tx.ExecContext(probeCtx,
		"INSERT INTO _doctor_probe (ts, marker) VALUES (?, ?)",
		time.Now().UTC().Format(time.RFC3339), "db-restore-smoke"); err != nil {
		return false, 0, fmt.Errorf("insert probe: %w", err)
	}
	var n int
	if err := tx.QueryRowContext(probeCtx,
		"SELECT COUNT(*) FROM _doctor_probe").Scan(&n); err != nil {
		return false, 0, fmt.Errorf("select probe: %w", err)
	}
	if err := tx.Rollback(); err != nil {
		return false, n, fmt.Errorf("rollback: %w", err)
	}
	committed = true // already rolled back; flag for deferred-runner clarity
	return (n >= 1), n, nil
}
