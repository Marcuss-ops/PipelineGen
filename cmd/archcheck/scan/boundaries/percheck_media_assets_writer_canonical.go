// Package scan — percheck_media_assets_writer_canonical is the
// forward-prevention gate that bans direct SQL writes to media_assets
// from ANY package outside the canonical AssetCommitter
// (data-layer unification, August 2026).
//
// godlike/06 SSOT: the canonical owner of media_assets writes is
// persistence.AssetCommitter (implemented by SQLiteAssetCommitter at
// internal/platform/sqlite/assets/imagesregistry/asset_committer.go).
// Every code path that durably creates, updates, or deletes a
// media_assets row MUST route through this port — YouTube, Artlist,
// local clips, voiceover, images, recovery.
//
// This gate is the codebase-wide fence (a generalisation of
// percheck_finalizer_no_direct_sql which scopes only the finalizer
// package). It walks the ENTIRE internal/ tree (excluding test files,
// the canonical owner, the archcheck scanner, and SQL migration files)
// and emits a violation for any non-canonical SQL write to media_assets.
//
// Exemptions:
//   - internal/platform/sqlite/assets/imagesregistry/asset_committer.go
//     (the canonical SOLE owner — it IS the SSOT).
//   - internal/platform/sqlite/assets/imagesregistry/asset_store_batch.go
//     (BatchGetByIDs — read-only SELECT, never a write).
//   - internal/platform/sqlite/assets/imagesregistry/asset_store.go
//     (Save — legacy diagnostic-only path, exempt per QDRANT-002 comment).
//   - internal/platform/sqlite/assets/imagesregistry/clips_repository.go
//     (UpsertClipTx — the tx-scoped variant used by the dispatcher fallback;
//     the fail-closed gate in EnqueueAndIndex blocks production use).
//   - *_test.go files (regression-guard surface).
//   - migrations/sqlite/*.sql (canonical schema migration files).
//   - cmd/archcheck/scan/** (this scanner references the forbidden literals).
//
// Comment-only references to media_assets in prose are WARNed
// (residue accounting, godlike/07), not violated.
//
// matched rule_id: `percheck_media_assets_writer_canonical`.
package boundaries

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// mediaAssetsWriterCanonicalOwners lists the files that are the
// canonical SOLE owners of SQL that writes media_assets. The gate does
// NOT inspect these files — they ARE the SSOT.
var mediaAssetsWriterCanonicalOwners = map[string]bool{
	"internal/platform/sqlite/assets/imagesregistry/asset_committer.go":      true,
	"internal/platform/sqlite/assets/imagesregistry/asset_store.go":          true,
	"internal/platform/sqlite/assets/imagesregistry/asset_store_batch.go":    true,
	"internal/platform/sqlite/assets/imagesregistry/clips_repository.go":    true,
	"internal/platform/sqlite/assets/imagesregistry/clips_crud.go":           true,
	"internal/platform/sqlite/assets/imagesregistry/primitives.go":           true,
	"internal/platform/sqlite/assets/imagesregistry/clip_atomic_writer.go":  true,
	"internal/platform/sqlite/assets/imagesregistry/asset_location_committer.go": true,
	"internal/platform/sqlite/assets/imagesregistry/media_committer.go":     true,
	"internal/platform/sqlite/assets/imagesregistry/clip_atomic_writer_cue_repair.go": true,
}

// mediaAssetsWriterScanRoots are the directory roots the gate walks.
// Every Go file under these roots (except tests + canonical owners +
// archcheck) is inspected for forbidden SQL patterns.
var mediaAssetsWriterScanRoots = []string{
	"internal",
}

// mediaAssetsWriterForbiddenRe matches a Go line that contains a
// direct SQL write to media_assets (INSERT, UPDATE, DELETE, REPLACE).
// Case-insensitive, substring match for forward-prevention.
var mediaAssetsWriterForbiddenRe = regexp.MustCompile(
	`(?i)\b(INSERT\s+INTO|UPDATE|DELETE\s+FROM|REPLACE\s+INTO)\s+media_assets\b`,
)

// mediaAssetsWriterSkipDirs mirrors the standard skip-dir set.
var mediaAssetsWriterSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
	"testdata":     true,
	"test":         true,
}

// mediaAssetsWriterSkipPathPrefixes are exemptions for scanner files +
// SQL migration files.
var mediaAssetsWriterSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
	"migrations/sqlite",
}

// mediaAssetsWriterRule is the rule-family id the scanner emits.
const mediaAssetsWriterRule = "percheck_media_assets_writer_canonical"

// mediaAssetsWriterNote is the violation Note string.
const mediaAssetsWriterNote = "forbidden direct SQL write to media_assets outside the canonical AssetCommitter (data-layer unification, August 2026): the canonical owner is persistence.AssetCommitter (SQLiteAssetCommitter at internal/platform/sqlite/assets/imagesregistry/asset_committer.go). Every asset commit (YouTube, Artlist, local, voiceover, images, recovery) MUST route through AssetCommitter.CommitAndIndex or CommitTx. The Dispatcher.EnqueueAndIndex fail-closed gate blocks the legacy UpsertClipTx fallback in production."

// mediaAssetsWriterWarn is the WARN-bucket emitter for residue accounting.
func mediaAssetsWriterWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, mediaAssetsWriterRule+" "+label+" "+msg)
}

// ScanMediaAssetsWriterCanonical walks the internal/ tree and inspects
// every non-test Go file for direct SQL writes to media_assets. Each
// forbidden write emits a violation; comment-only references are
// residue-accounted (WARNed) per godlike/07.
//
// The canonical AssetCommitter + its sibling repository files are
// exempt — they ARE the SSOT. SQL migration files (.sql under
// migrations/sqlite/) are exempt (canonical schema evolution).
func ScanMediaAssetsWriterCanonical(root string, _ *policy.Policy, r *report.Report) {
	for _, scanRoot := range mediaAssetsWriterScanRoots {
		absRoot := filepath.Join(root, scanRoot)
		filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				base := filepath.Base(path)
				if mediaAssetsWriterSkipDirs[base] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			inspectMediaAssetsWriterFile(root, path, r)
			return nil
		})
	}
}

// inspectMediaAssetsWriterFile opens a single Go file and scans each
// line for forbidden SQL patterns. Canonical owner files are exempt.
func inspectMediaAssetsWriterFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)

	if mediaAssetsWriterCanonicalOwners[relPath] {
		return
	}
	for _, prefix := range mediaAssetsWriterSkipPathPrefixes {
		if strings.HasPrefix(relPath, prefix) {
			return
		}
	}

	f, err := os.Open(absPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        0,
			Rule:        mediaAssetsWriterRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unreadable",
			Note:        mediaAssetsWriterNote + " | cannot open file: " + err.Error(),
		})
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			if mediaAssetsWriterForbiddenRe.MatchString(line) {
				commentOnly++
			}
			continue
		}

		m := mediaAssetsWriterForbiddenRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		verb := strings.ToLower(strings.TrimSpace(m[1]))
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNo,
			Rule:        mediaAssetsWriterRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "forbidden_sql_" + verb,
			Note: mediaAssetsWriterNote +
				" | file: " + relPath +
				" | matched verb: " + verb +
				" | route through persistence.AssetCommitter (canonical SSOT: internal/platform/sqlite/assets/imagesregistry/asset_committer.go)",
		})
	}
	if commentOnly > 0 {
		mediaAssetsWriterWarn(r, "forbidden-sql:",
			"comment-only reference(s) to direct SQL writes to media_assets in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}
