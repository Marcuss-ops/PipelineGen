// Command migrate-status prints the status of all SQLite migrations:
// which are applied and which are pending.
//
// Usage:
//
//	go run ./cmd/migrate-status [flags]
//
// Flags:
//
//	-db string    Path to the SQLite database (default "data/media/media.db.sqlite")
//	-dir string   Path to migrations directory (default "migrations/sqlite")
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/storage"
)

func main() {
	dbPath := flag.String("db", "data/media/media.db.sqlite", "path to SQLite database")
	migrationsDir := flag.String("dir", "migrations/sqlite", "path to migrations directory")
	flag.Parse()

	if _, err := os.Stat(*migrationsDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: migrations directory not found: %s\n", *migrationsDir)
		os.Exit(1)
	}

	var db *sql.DB
	if _, err := os.Stat(*dbPath); err == nil {
		var openErr error
		db, openErr = sql.Open("sqlite3", *dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
		if openErr != nil {
			fmt.Fprintf(os.Stderr, "Error: open database: %v\n", openErr)
			os.Exit(1)
		}
		defer db.Close()
	} else {
		// Database doesn't exist — report all pending (dry-run mode)
		log, _ := zap.NewProduction()
		defer log.Sync()
		log.Info("database not found, reporting all migrations as pending",
			zap.String("path", *dbPath),
		)
	}

	report, err := storage.GetMigrationStatus(db, *migrationsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(storage.FormatMigrateStatus(report))

	if report.PendingN > 0 {
		os.Exit(2) // non-zero exit indicates pending migrations
	}
}
