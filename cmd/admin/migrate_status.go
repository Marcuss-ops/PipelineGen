package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
)

func runMigrateStatus(args []string) error {
	fs := flag.NewFlagSet("migrate-status", flag.ExitOnError)
	dbPath := fs.String("db", "data/media/media.db.sqlite", "path to SQLite database")
	migrationsDir := fs.String("dir", "migrations/sqlite", "path to migrations directory")
	fs.Parse(args)

	if _, err := os.Stat(*migrationsDir); os.IsNotExist(err) {
		return fmt.Errorf("migrations directory not found: %s", *migrationsDir)
	}

	var db *sql.DB
	if _, err := os.Stat(*dbPath); err == nil {
		var openErr error
		db, openErr = sql.Open("sqlite3", *dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
		if openErr != nil {
			return fmt.Errorf("open database: %w", openErr)
		}
		defer db.Close()
	} else {
		log, _ := zap.NewProduction()
		defer log.Sync()
		log.Info("database not found, reporting all migrations as pending",
			zap.String("path", *dbPath),
		)
	}

	report, err := storage.GetMigrationStatus(db, *migrationsDir)
	if err != nil {
		return fmt.Errorf("migration status: %w", err)
	}

	fmt.Print(storage.FormatMigrateStatus(report))

	if report.PendingN > 0 {
		return fmt.Errorf("%d pending migrations", report.PendingN)
	}
	return nil
}
