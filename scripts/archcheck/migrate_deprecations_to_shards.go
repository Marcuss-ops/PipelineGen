package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// One-shot migration tool: split the legacy single-file form
// architecture/deprecations.yaml into the planned sharded directory
// layout under architecture/deprecations/.
//
// Output tree:
//
//	architecture/deprecations/
//	  index.yaml          # machine-consumed aggregate manifest
//	  records/<bucket>.yaml   # one shard per subsystem bucket
//	  audit.yaml          # aggregated audit block (re-derived from records)
//	  open_questions.yaml # preserved verbatim block from the legacy file
//
// Per AGENTS.md / godlike/06 SSOT, the records (NOT the audit metadata)
// are the source of truth; this migrator re-derives the audit counts
// from the records it ships so the layout cannot drift away from the
// ground-truth.
//
// Operator entry point: scripts/archcheck/main.go dispatches to
// migrate() when the --migrate-deprecations flag is set. This file
// does NOT carry its own main() because scripts/archcheck/ already
// has one (and a package can only have one entrypoint).
const (
	defaultLegacyPath = "architecture/deprecations.yaml"
	defaultTargetDir  = "architecture/deprecations"
	indexFileName     = "index.yaml"
	auditFileName     = "audit.yaml"
	openQFileName     = "open_questions.yaml"
	recordsDirName    = "records"
)

// shardDoc is the canonical on-disk shape of a single shard file under
// records/. Each record carries every required field (godlike/07 SSOT);
// the validator's loader reads shard → deprecationManifest silently
// because the top-level key matches the deprecationManifest field
// tag.
type shardDoc struct {
	Deprecations []deprecationRecord `yaml:"deprecations"`
}

type auditDoc struct {
	Audit auditBlock `yaml:"audit"`
}

type indexEntry struct {
	Bucket string `yaml:"bucket"`
	File   string `yaml:"file"`
	Count  int    `yaml:"count"`
}

type indexDoc struct {
	SchemaVersion     int          `yaml:"schema_version"`
	SourceDirectory   string       `yaml:"source_directory"`
	TotalRecords      int          `yaml:"total_records"`
	Records           []indexEntry `yaml:"records"`
	AuditFile         string       `yaml:"audit_file"`
	OpenQuestionsFile string       `yaml:"open_questions_file"`
}

func migrate(legacyPath, targetDir string) error {
	if _, err := os.Stat(legacyPath); err != nil {
		return fmt.Errorf("legacy source %s: %w", legacyPath, err)
	}

	manifest, dupKeys, err := loadDeprecationManifest(legacyPath)
	if err != nil {
		return fmt.Errorf("load legacy: %w", err)
	}
	if len(dupKeys) > 0 {
		return fmt.Errorf("legacy has %d duplicate YAML key(s); fix them first",
			len(dupKeys))
	}

	grouped := groupDeprecationsByBucket(manifest.Deprecations)

	recordsDir := filepath.Join(targetDir, recordsDirName)
	if err := os.MkdirAll(recordsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir records: %w", err)
	}

	// Sort bucket names so the shards are emitted deterministically
	// across runs (otherwise Go's map-iteration order would scramble
	// the on-disk layout).
	bucketNames := make([]string, 0, len(grouped))
	for b := range grouped {
		bucketNames = append(bucketNames, string(b))
	}
	sort.Strings(bucketNames)

	// Emit each bucket shard.
	var total int
	var indexEntries []indexEntry
	for _, bn := range bucketNames {
		records := grouped[deprecationBucket(bn)]
		sort.SliceStable(records, func(i, j int) bool { return records[i].ID < records[j].ID })
		out, err := yaml.Marshal(shardDoc{Deprecations: records})
		if err != nil {
			return fmt.Errorf("marshal shard %s: %w", bn, err)
		}
		shardPath := filepath.Join(recordsDir, bn+".yaml")
		if err := atomicWriteFile(shardPath, out, 0o644); err != nil {
			return fmt.Errorf("write shard %s: %w", bn, err)
		}
		indexEntries = append(indexEntries, indexEntry{
			Bucket: bn,
			File:   filepath.Join(recordsDirName, bn+".yaml"),
			Count:  len(records),
		})
		total += len(records)
	}

	// Aggregate audit counts from records (re-derived; godlike/06 SSOT).
	// Status field maps to the canonical 3 buckets (Removed/InProgress/Keep)
	// plus an Other bucket that captures non-canonical statuses the legacy
	// file has surfaced verbatim ("pending-removal",
	// "open (team vote pending)"). Without Other, the re-derived audit
	// would silently drop ~2 records and the operator would not see the
	// drift.
	counts := auditBlock{
		ManifestVersion:  "sharded-migration-v1",
		ByMigrationPhase: make(map[string]int),
	}
	for _, rec := range manifest.Deprecations {
		counts.TotalRecords++
		switch rec.Status {
		case "removed":
			counts.ByStatus.Removed++
		case "in_progress":
			counts.ByStatus.InProgress++
		case "keep":
			counts.ByStatus.Keep++
		default:
			counts.ByStatus.Other++
		}
		counts.ByMigrationPhase[rec.MigrationPhase]++
	}
	if counts.ByStatus.Other > 0 {
		fmt.Fprintf(os.Stderr,
			"migrate_deprecations_to_shards: WARN audit.by_status.other=%d "+
				"(non-canonical statuses preserved under 'other')\n",
			counts.ByStatus.Other)
	}
	if manifest.Audit.TotalRecords > 0 &&
		manifest.Audit.TotalRecords != counts.TotalRecords {
		fmt.Fprintf(os.Stderr,
			"migrate_deprecations_to_shards: WARN upstream drift "+
				"audit.total_records=%d != len(records)=%d\n",
			manifest.Audit.TotalRecords, counts.TotalRecords)
	}
	auditBytes, err := yaml.Marshal(auditDoc{Audit: counts})
	if err != nil {
		return fmt.Errorf("marshal audit: %w", err)
	}
	if err := atomicWriteFile(filepath.Join(targetDir, auditFileName),
		auditBytes, 0o644); err != nil {
		return fmt.Errorf("write audit: %w", err)
	}

	// Index manifest (machine-consumed aggregate).
	idx := indexDoc{
		SchemaVersion:     1,
		SourceDirectory:   normalizeTargetDir(targetDir),
		TotalRecords:      total,
		Records:           indexEntries,
		AuditFile:         auditFileName,
		OpenQuestionsFile: openQFileName,
	}
	idxBytes, err := yaml.Marshal(idx)
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	if err := atomicWriteFile(filepath.Join(targetDir, indexFileName),
		idxBytes, 0o644); err != nil {
		return fmt.Errorf("write index: %w", err)
	}

	// Extract open_questions block via line-range text extraction:
	// simpler than generic yaml.v3 round-tripping and preserves any
	// comment-only context (godlike/07 honest-limitation disclosure).
	if err := writeOpenQuestionsBlock(legacyPath,
		filepath.Join(targetDir, openQFileName)); err != nil {
		fmt.Fprintf(os.Stderr,
			"migrate_deprecations_to_shards: WARN open_questions: %v\n", err)
	}

	fmt.Fprintf(os.Stderr,
		"migrate_deprecations_to_shards: wrote %d shards (%d records) under %s/\n",
		len(bucketNames), total, targetDir)
	return nil
}

// writeOpenQuestionsBlock extracts the `open_questions:` YAML block
// from the legacy file by line range (between the `open_questions:`
// header and the next top-level key, conventionally `audit:`). The
// extracted text is written verbatim so comments and anchor structure
// survive byte-for-byte.
func writeOpenQuestionsBlock(srcPath, targetPath string) error {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	lines := bytes.Split(raw, []byte("\n"))
	oqStart, auditStart := -1, -1
	for i, l := range lines {
		s := strings.TrimLeft(string(l), " ")
		if s == "open_questions:" && oqStart == -1 {
			oqStart = i
		}
		if s == "audit:" && auditStart == -1 {
			auditStart = i
		}
	}
	if oqStart < 0 {
		return nil // no open_questions block — leave the file unwritten
	}
	end := len(lines)
	if auditStart > oqStart {
		end = auditStart
	}
	body := lines[oqStart:end]
	return atomicWriteFile(targetPath,
		bytes.Join(body, []byte("\n")), 0o644)
}

// atomicWriteFile writes `data` to `path` via a temp-file + rename: any
// write/close failure (disk full, permission, etc.) leaves the
// destination path unchanged. Per the IMPORTANT review finding on the
// prior round, partial writes must NEVER leave a corrupted shard
// pointing to bogus records; the rename is durable on POSIX.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}

// normalizeTargetDir returns the migrator's canonical representation
// of targetDir for the index.yaml manifest. Production calls use a
// relative path (architecture/deprecations) and pass through
// unchanged; absolute inputs (e.g. tests under t.TempDir()) collapse
// to a stable relative-or-basename form so the manifest content is
// byte-deterministic across reruns. This is the property the
// TestMigrate_Determinism golden test asserts.
func normalizeTargetDir(targetDir string) string {
	if !filepath.IsAbs(targetDir) {
		return filepath.Clean(targetDir)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.Base(targetDir)
	}
	rel, err := filepath.Rel(cwd, targetDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Base(targetDir)
	}
	return rel
}
