// Command gen_baseline regenerates the plane-pure SQLite baselines.
//
// A consolidated baseline is the single idempotent dump a fresh database
// applies instead of replaying the historical migrations. The original dump
// (`000_baseline_268.sql`, `-- database: all`) is the post-298 schema of the
// primary database merged with the observability schema: it is FROZEN. Every
// existing database records its checksum in `schema_migrations`, so editing
// its bytes would turn `ledger matches disk` into a false alarm.
//
// DB-03 shows why a single shared dump is not enough: a fresh observability or
// cache database was built with the business schema and only then had it
// dropped by 307/308. The plane dumps generated here are taken from a clean
// database that ran the WHOLE corpus (including 307/308), so they contain
// exactly the objects the plane owns. The runner gives them precedence over the
// shared dump (see migrations_discovery.go::selectBaselineForPlane).
//
// Run from the refactored module root:
//
//	go run ./cmd/gen_baseline
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
	"go.uber.org/zap"
)

// planeBaseline is one plane-pure dump the generator owns. The shared
// `000_baseline_268.sql` is deliberately absent: it stays frozen.
type planeBaseline struct {
	plane    string
	filename string
}

var planeBaselines = []planeBaseline{
	{plane: "observability", filename: "000_baseline_268_observability.sql"},
	{plane: "cache", filename: "000_baseline_268_cache.sql"},
}

func main() {
	targetDir, _ := filepath.Abs("migrations/sqlite")
	if _, err := os.Stat(targetDir); err != nil {
		for _, c := range []string{"migrations/sqlite", "refactored/migrations/sqlite"} {
			if _, err := os.Stat(c); err == nil {
				abs, _ := filepath.Abs(c)
				targetDir = abs
				break
			}
		}
	}
	fmt.Println("targetDir", targetDir)

	// The dump is taken from the corpus WITHOUT the plane dumps: a fresh plane
	// database then bootstraps from the frozen shared dump exactly as today, and
	// the dump records the state after 307/308. A copy keeps the real directory
	// untouched while the dumps are being derived.
	planeFiles := map[string]bool{}
	for _, pb := range planeBaselines {
		planeFiles[pb.filename] = true
	}
	incrementalDir := corpusWithoutPlaneBaselines(targetDir, planeFiles)
	defer os.RemoveAll(incrementalDir)

	covers := maxMigrationVersion(targetDir)
	tmpDir, _ := os.MkdirTemp("", "baseline-gen-*")
	defer os.RemoveAll(tmpDir)

	for _, pb := range planeBaselines {
		dbPath := filepath.Join(tmpDir, pb.plane+".db")
		mustRunMigrations(dbPath, incrementalDir, pb.plane)
		stmts := schemaFromDB(dbPath)
		writePlaneBaseline(targetDir, pb, covers, stmts)
		fmt.Printf("wrote %s: %d statements (covers 1..%d)\n", pb.filename, len(stmts), covers)
	}

	log := zap.NewNop()
	for _, pb := range planeBaselines {
		verifyDir := filepath.Join(tmpDir, "verify-"+pb.plane)
		if err := os.MkdirAll(verifyDir, 0o755); err != nil {
			panic(err)
		}
		dbPath := filepath.Join(verifyDir, pb.plane+".db")
		if err := sqlite.RunMigrationsOnDB(dbPath, log, targetDir, pb.plane); err != nil {
			fmt.Printf("verify %s failed: %v\n", pb.plane, err)
			os.Exit(1)
		}
		want := schemaFromDB(filepath.Join(tmpDir, pb.plane+".db"))
		got := schemaFromDB(dbPath)
		if !sameStatements(want, got) {
			fmt.Printf("verify %s: fresh database schema differs from the generated dump\nwant %d statements, got %d\n",
				pb.plane, len(want), len(got))
			printStatementDiff(want, got)
			os.Exit(1)
		}
		fmt.Printf("verify %s OK (%d statements, no business schema)\n", pb.plane, len(got))
	}
}

func writePlaneBaseline(targetDir string, pb planeBaseline, covers int, stmts []string) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "-- %s — plane-pure SQLite baseline (%s)\n", pb.filename, pb.plane)
	fmt.Fprintf(&sb, "-- database: %s\n", pb.plane)
	fmt.Fprintf(&sb, "-- baseline-covers: %d\n", covers)
	sb.WriteString("--\n")
	sb.WriteString("-- Generated from a clean database that ran the WHOLE incremental corpus\n")
	sb.WriteString("-- (including 307/308), so this dump contains exactly the objects the plane\n")
	sb.WriteString("-- owns: no business table is created and then dropped on a fresh install.\n")
	sb.WriteString("-- The runner applies it in preference to the frozen shared dump\n")
	sb.WriteString("-- 000_baseline_268.sql (see migrations_discovery.go::selectBaselineForPlane).\n")
	sb.WriteString("--\n")
	sb.WriteString("-- DO NOT EDIT MANUALLY — regenerate via `go run ./cmd/gen_baseline`\n")
	sb.WriteString("-- and verify with `go test ./internal/platform/sqlite -run TestMigrations_PlaneBaselines`.\n\n")
	for _, s := range stmts {
		sb.WriteString(rewriteIdempotent(s) + "\n")
	}
	if err := atomicwrite.WriteFile(filepath.Join(targetDir, pb.filename), []byte(sb.String()), 0644); err != nil {
		panic(err)
	}
}

func sameStatements(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if strings.TrimSpace(want[i]) != strings.TrimSpace(got[i]) {
			return false
		}
	}
	return true
}

func printStatementDiff(want, got []string) {
	wantSet := map[string]bool{}
	for _, s := range want {
		wantSet[strings.TrimSpace(s)] = true
	}
	gotSet := map[string]bool{}
	for _, s := range got {
		gotSet[strings.TrimSpace(s)] = true
	}
	for _, s := range want {
		if !gotSet[strings.TrimSpace(s)] {
			fmt.Printf("  missing: %s\n", s)
		}
	}
	for _, s := range got {
		if !wantSet[strings.TrimSpace(s)] {
			fmt.Printf("  extra:   %s\n", s)
		}
	}
}
