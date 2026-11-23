// Command cliprender-gc applies bounded retention to clip.render local state
// and (optionally) stale clip_render_cache rows. It defaults to dry-run and
// never touches media_assets, so a cache delete only forces a future miss.
//
//	go run ./cmd/cliprender-gc -staging-root ... -sequence-root ...
//	go run ./cmd/cliprender-gc -apply -media-dsn "$PIPELINEGEN_MEDIA_POSTGRES_DSN"
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	clipgc "github.com/Marcuss-ops/PipelineGen/internal/platform/cliprender/gc"
	pgcliprender "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/cliprender"
)

func main() {
	apply := flag.Bool("apply", false, "delete eligible files/cache rows (default is dry-run)")
	dryRun := flag.Bool("dry-run", false, "explicit dry-run (the default)")
	stagingRoot := flag.String("staging-root", defaultRoot("staging"), "clip.render async Drive staging root")
	stagingRetention := flag.Duration("staging-retention", 48*time.Hour, "staging retention")
	sequenceRoot := flag.String("sequence-root", defaultRoot("sequences"), "sequence assembly root")
	sequenceRetention := flag.Duration("sequence-retention", 24*time.Hour, "sequence output retention")
	cacheRetention := flag.Duration("cache-retention", 30*24*time.Hour, "clip_render_cache row retention")
	mediaDSN := flag.String("media-dsn", strings.TrimSpace(os.Getenv("PIPELINEGEN_MEDIA_POSTGRES_DSN")), "PostgreSQL media DSN (empty = skip cache retention)")
	casRoot := flag.String("cas-root", defaultCASRoot(), "CAS root holding clip.render resume documents (empty = skip resume retention)")
	resumeRetention := flag.Duration("resume-retention", 7*24*time.Hour, "resume document retention")
	jobsDB := flag.String("jobs-db", defaultJobsDBPath(), "SQLite jobs DB used to keep resume documents referenced by active jobs (required with -cas-root)")
	flag.Parse()

	if *apply && *dryRun {
		fail("-apply and -dry-run are mutually exclusive")
	}

	roots := make([]clipgc.Root, 0, 2)
	if strings.TrimSpace(*stagingRoot) != "" {
		roots = append(roots, clipgc.Root{Name: "staging", Path: *stagingRoot, Retention: *stagingRetention})
	}
	if strings.TrimSpace(*sequenceRoot) != "" {
		roots = append(roots, clipgc.Root{Name: "sequences", Path: *sequenceRoot, Retention: *sequenceRetention})
	}

	ctx := context.Background()
	var cache clipgc.CachePruner
	var resume *clipgc.ResumeConfig
	var db *sql.DB
	if strings.TrimSpace(*mediaDSN) != "" {
		var err error
		db, err = sql.Open("pgx", *mediaDSN)
		if err != nil {
			fail("open media PostgreSQL: %v", err)
		}
		defer db.Close()
		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := db.PingContext(pingCtx); err != nil {
			cancel()
			fail("ping media PostgreSQL: %v", err)
		}
		cancel()
		cache = pgcliprender.NewRenderCachePruner(db)
	}

	if strings.TrimSpace(*casRoot) != "" {
		if strings.TrimSpace(*jobsDB) == "" {
			fail("-cas-root requires -jobs-db (or VELOX_JOBS_DB_PATH) to keep active resume documents")
		}
		refs, err := loadActiveResumeRefs(ctx, *jobsDB)
		if err != nil {
			fail("load active resume references: %v", err)
		}
		resume = &clipgc.ResumeConfig{
			Root:       *casRoot,
			Retention:  *resumeRetention,
			ActiveRefs: refs,
			Parse: func(raw []byte) (bool, error) {
				return cliprender.IsResumeDocument(raw), nil
			},
		}
	}

	report, err := clipgc.Run(ctx, clipgc.Config{
		Roots:          roots,
		Cache:          cache,
		CacheRetention: *cacheRetention,
		Resume:         resume,
		Apply:          *apply,
	})
	if err != nil {
		fail("clip.render GC: %v", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fail("encode report: %v", err)
	}
	if report.HasErrors() {
		os.Exit(1)
	}
}

func loadActiveResumeRefs(ctx context.Context, path string) (map[string]struct{}, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open jobs DB: %w", err)
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("ping jobs DB: %w", err)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(p.payload, '')
		 FROM jobs j
		 LEFT JOIN job_payloads p ON p.job_id = j.id
		 WHERE j.status NOT IN ('SUCCEEDED','FAILED','CANCELLED','PARTIALLY_SUCCEEDED')`)
	if err != nil {
		return nil, fmt.Errorf("query active jobs: %w", err)
	}
	defer rows.Close()
	var payloads []string
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan active job payload: %w", err)
		}
		payloads = append(payloads, payload)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active job payloads: %w", err)
	}
	return clipgc.RefsInJSONPayloads(payloads), nil
}

func defaultDataDir() string {
	dataDir := strings.TrimSpace(os.Getenv("VELOX_DATA_DIR"))
	if dataDir == "" {
		dataDir = "./data"
	}
	return dataDir
}

func defaultCASRoot() string {
	root := filepath.Join(defaultDataDir(), "cas")
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}

func defaultJobsDBPath() string {
	if explicit := strings.TrimSpace(os.Getenv("VELOX_JOBS_DB_PATH")); explicit != "" {
		return explicit
	}
	path := filepath.Join(defaultDataDir(), "jobs", "jobs.db.sqlite")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func defaultRoot(sub string) string {
	dataDir := strings.TrimSpace(os.Getenv("PIPELINEGEN_DATA_DIR"))
	if dataDir == "" {
		dataDir = "./data"
	}
	tempDir := strings.TrimSpace(os.Getenv("VELOX_TEMP_DIR"))
	if tempDir == "" {
		tempDir = "tmp"
	}
	if filepath.IsAbs(tempDir) {
		return filepath.Join(tempDir, "cliprender", sub)
	}
	return filepath.Join(dataDir, tempDir, "cliprender", sub)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cliprender-gc: "+format+"\n", args...)
	os.Exit(1)
}
