package iobinder

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
) // exceptionList enumerates the known direct-IO bindings in
// internal/application/ that the sub-PRs (PR-REFACTOR-P0-IO-BINDER-SQLITE,
// -FS, -FINALIZERS) will address. When the list reaches zero, the
// unconditional "0 hits" assertion activates. Entries are file:line
// keys observed live on origin/main at audit time (2026-08-10) plus
// the new hits triaged in July 2026.
//
// The spec's `rg 'os\.Open'` is a substring match (no word boundary),
// so it also catches `os.OpenFile` calls and comment references. The
// os.Open / os.OpenFile hits break down as:
//   - actual `os.Open(...)` call sites
//   - `os.OpenFile(...)` call sites (substring match)
//   - comment references to `os.Open` (staged/resolver.go:30/84/195)
var exceptionList = map[string]bool{
	// ── os.Open / os.OpenFile hits (16) ────────────────────────────────
	"internal/application/jobs/assets/service.go:100":                                 true,
	"internal/application/assets/artifacts/resolvers/resolvers.go:118":                true,
	"internal/application/assets/artifacts/resolvers/resolvers.go:168":                true,
	"internal/application/images/sync_generation.go:138":                              true,
	"internal/application/assets/staged/resolver.go:275":                              true,
	"internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:123": true,
	"internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:126": true, // os.OpenFile
	"internal/application/assets/staged/resolver.go:30":                               true, // comment reference
	"internal/application/assets/staged/resolver.go:84":                               true, // comment reference
	"internal/application/assets/staged/resolver.go:195":                              true, // comment reference
	"internal/application/workerdoctor/probes_dependency.go:211":                      true, // os.OpenFile
	"internal/application/workerdoctor/probes_dependency.go:222":                      true, // os.OpenFile
	"internal/application/assets/ingest/image.go:52":                                  true,
	"internal/application/document/service.go:219":                                    true,
	"internal/application/assets/artifacts/local_blob.go:72":                          true,
	"internal/application/assets/artifacts/local_blob.go:128":                         true,

	// PR-REFACTOR-P0-IO-BINDER-FS (July 2026): new os.Open hits
	// discovered after the audit baseline. These are production
	// file-system bindings that will be moved behind a typed port
	// in the FS sub-PR.
	"internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:157": true,
	"internal/application/assets/providers/stock/stockpipeline/stager_adapter.go:160": true,
	"internal/application/assets/texttracks/vtt_parser.go:51":                         true,
	"internal/application/images/generated_image_ingest.go:57":                        true,
	"internal/application/images/storage_download.go:56":                              true,
	"internal/application/images/visual_validate/visual_validate.go:101":              true,
	"internal/application/jobs/assets/service.go:312":                                 true,
	"internal/application/publish_outbox/handler.go:226":                              true,
	"internal/application/staging/service.go:142":                                     true,

	// ── database/sql import hits (36) ─────────────────────────────────
	"internal/application/qdrant/maintenance/service.go:32":                           true,
	"internal/application/jobs/finalizer/job_finalizer.go:52":                         true,
	"internal/application/jobs/finalizer/reconciler.go:40":                            true,
	"internal/application/jobs/finalizer/artifact_writer.go:32":                       true,
	"internal/application/jobs/finalizer/lease_fence.go:40":                           true,
	"internal/application/jobs/finalizer/job_completion_writer.go:32":                 true,
	"internal/application/jobs/outbox/registry.go:26":                                 true,
	"internal/application/jobs/outbox/indexing_handle.go:5":                           true,
	"internal/application/jobs/outbox/delivery.go:52":                                 true,
	"internal/application/scripts/adapters/gemmamemory.go:7":                          true,
	"internal/application/books/service.go:37":                                        true,
	"internal/application/assets/maintenance/deep_cleanup.go:5":                       true,
	"internal/application/assets/providers/artlist/service.go:5":                      true,
	"internal/application/assets/providers/artlist/search_cache.go:5":                 true,
	"internal/application/assets/providers/stock/enrichment/handler_repository.go:36": true,
	"internal/application/assets/providers/stock/stockpipeline/service.go:54":         true,
	"internal/application/assets/providers/stock/enrichment/outbox_emitter.go:60":     true,
	"internal/application/assets/maintenance/run_cleanup.go:5":                        true,
	"internal/application/assets/maintenance/service.go:5":                            true,
	"internal/application/assets/providers/stock/stockpipeline/service_types.go:26":   true,
	"internal/application/document/usecase.go:17":                                     true,
	"internal/application/assets/finalizer/tx_adapter.go:9":                           true,
	"internal/application/assets/lifecycle/service_voiceover.go:11":                   true,
	"internal/application/assets/ingest/adapter_clip.go:5":                            true,
	"internal/application/voiceover/verify_media_assets.go:45":                        true,
	"internal/application/assets/artifacts/repository.go:5":                           true,
	"internal/application/assets/artifacts/clips_adapter.go:5":                        true,
	"internal/application/assets/artifacts/resolvers/resolvers.go:8":                  true,
	"internal/application/execution/steps/sqlite_store.go:44":                         true,
	"internal/application/voiceover/dedupe.go:26":                                     true,
	"internal/application/voiceover/finalizer_execute.go:5":                           true,
	"internal/application/voiceover/finalizer.go:50":                                  true,
	"internal/application/voiceover/upload_intent.go:67":                              true,
	"internal/application/voiceover/persistence/repository.go:5":                      true,
	"internal/application/voiceover/finalizer_cleanup_outbox.go:42":                   true,
	"internal/application/voiceover/ports.go:38":                                      true,

	// PR-REFACTOR-P0-IO-BINDER-SQLITE (July 2026): new database/sql
	// import hits discovered after the audit baseline. These are
	// transitional direct SQL references that will be abstracted
	// behind typed ports in the SQLite sub-PR.
	"internal/application/assets/persistence/committer.go:26":      true,
	"internal/application/assets/processing/asset_committer.go:15": true,
	"internal/application/assets/texttracks/materializer.go:23":    true,
	"internal/application/operations/ports.go:20":                  true,
	"internal/application/voiceover/ports_finalization.go:5":       true,
}

// disallowedPatterns are the 3 verbatim patterns from the
// PR-REFACTOR-P0-IO-BINDER spec verification. The spec uses
// `rg 'os\.Open|sql\.Open|database/sql'` which is a substring match
// (no word boundary), so `os.Open` also matches `os.OpenFile`,
// `os.Openat`, etc., and the literal `"database/sql"` also matches
// any quoted reference. This matches the canonical spec behavior.
var disallowedPatterns = []string{
	"os.Open",
	"sql.Open",
	`"database/sql"`,
}

// TestNoDirectIOBindingsInApplicationLayer is the forward-prevention
// regression-guard for PR-REFACTOR-P0-IO-BINDER. It walks
// internal/application/ recursively, scans each .go production file
// (excluding _test.go and the iobinder/ directory itself), and fails
// if any of the 3 disallowed patterns appears at a file:line that is
// NOT in the exception list.
//
// Per AGENTS.md Pattern 5 + godlike/07 minimum-blast-radius: the test
// is hermetic (no subprocess, no network, no DB); runtime < 100ms on
// the canonical application-layer tree.
func TestNoDirectIOBindingsInApplicationLayer(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	appDir := filepath.Join(repoRoot, "internal", "application")

	var violations []string
	err = filepath.WalkDir(appDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Per spec literal: "fuori dai test" (outside tests). Test
		// files use the same patterns the gate forbids (sqlx, httpmock
		// direct DB fixtures, etc.) and are out of scope for this
		// refactor.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Exclude the iobinder/ directory itself: the test scans files
		// and the doc file legitimately references the patterns.
		if strings.Contains(filepath.ToSlash(path), "/iobinder/") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		defer f.Close()

		rel, _ := filepath.Rel(repoRoot, path)
		relSlash := filepath.ToSlash(rel)
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1 MiB line cap for big generated files
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			for _, pat := range disallowedPatterns {
				if !strings.Contains(line, pat) {
					continue
				}
				key := relSlash + ":" + strconv.Itoa(lineNum)
				if exceptionList[key] {
					continue
				}
				violations = append(violations, key+" : "+pat)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scan %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk error: %v", err)
	}

	if len(violations) > 0 {
		t.Fatalf(
			"NEW direct-IO bindings detected in internal/application/.\n"+
				"\n"+
				"Per PR-REFACTOR-P0-IO-BINDER spec, the application layer MUST NOT\n"+
				"directly import `database/sql` or call `os.Open` / `sql.Open`.\n"+
				"All such I/O must route through typed ports (Pattern 0) implemented\n"+
				"in internal/infrastructure/.\n"+
				"\n"+
				"New violations (refactor to a typed port, OR add to the exception\n"+
				"list ONLY with an explicit sub-PR forward-pointer in a code-review\n"+
				"comment):\n"+
				"  %s\n"+
				"\n"+
				"Exception list currently contains %d entries (the canonical\n"+
				"baseline at audit time 2026-08-10). Sub-PRs that will shrink the\n"+
				"list:\n"+
				"  - PR-REFACTOR-P0-IO-BINDER-SQLITE (database/sql adapters)\n"+
				"  - PR-REFACTOR-P0-IO-BINDER-FS (os.Open in business logic)\n"+
				"  - PR-REFACTOR-P0-IO-BINDER-FINALIZERS (sql.Tx leaks)\n",
			strings.Join(violations, "\n  "),
			len(exceptionList),
		)
	}

	t.Logf("OK: 0 NEW direct-IO bindings. Exception list contains %d entries (canonical baseline, will shrink as sub-PRs land).", len(exceptionList))
}

// TestExceptionListStaleEntries surfaces exception-list entries that no
// longer correspond to a live hit (informational, not a failure).
// When a sub-PR lands and removes a `database/sql` import or an
// `os.Open` call, the exception-list entry for that file:line is now
// stale and can be removed in a follow-up housekeeping commit.
func TestExceptionListStaleEntries(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	appDir := filepath.Join(repoRoot, "internal", "application")

	// Build a set of file:line keys that ACTUALLY hit one of the
	// 3 patterns, so we can diff against the exception list.
	hits := make(map[string]bool)
	walkErr := filepath.WalkDir(appDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "/iobinder/") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		rel, _ := filepath.Rel(repoRoot, path)
		relSlash := filepath.ToSlash(rel)
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			for _, pat := range disallowedPatterns {
				if strings.Contains(line, pat) {
					hits[relSlash+":"+strconv.Itoa(lineNum)] = true
				}
			}
		}
		return scanner.Err()
	})
	if walkErr != nil {
		t.Fatalf("walk error: %v", walkErr)
	}

	var stale []string
	for key := range exceptionList {
		if !hits[key] {
			stale = append(stale, key)
		}
	}
	if len(stale) > 0 {
		t.Logf("Informational: %d exception-list entries are stale (sub-PRs already removed the underlying hit). Safe to remove in a housekeeping commit:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// findRepoRoot walks up from the test file's directory until it finds
// a go.mod. Mirrors the canonical Go runtime.Caller(0) idiom used in
// scripts that need to resolve the project root.
func findRepoRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found walking up from %s", thisFile)
		}
		dir = parent
	}
}
