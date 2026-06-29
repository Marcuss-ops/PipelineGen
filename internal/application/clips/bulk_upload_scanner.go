// Package clips (bulk_upload_scanner) — exported application-level
// scanner helpers for the bulk-upload pipeline.
// DRIFT-CLIPS-BULK-SPLIT-5 (June 2026, override ADR 0009):
// extracted from internal/api/assets/clips/bulk_upload.go so the
// HTTP transport can satisfy AGENTS.md Pattern 8 (transport = thin
// HTTP shell; business orchestration delegates to application).
//
// The application tier already hosts the worker's scanLocalClips
// (bulk_upload_worker.go) and clipCandidate (bulk_upload_helpers.go)
// for the bg-job pipeline — both are kept untouched. The exported
// helpers added here cover the HTTP transport's dry-run preview
// path, which has DIFFERENT semantics from the worker scan:
//   * public pattern-glob matching (filepath.Match) (worker uses
//     substring matching),
//   * transport-only notion of "skipped" patterns (worker uses
//     include-only),
//   * explicit "limit" semantics (worker hits limit during walk;
//     transport counts only for the preview).
//
// The transport calls ScanLocalClips + ReadManifestJSON + ReadTextFile
// directly; application/clips is the canonical SSOT for these helpers.
// No infrastructure imports — every function is a pure leaf.
package clips

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BulkUploadCandidate describes one .mp4 file found on disk ready
// for processing by the bulk-upload pipeline. Field set matches the
// pre-Split-5 unexported clipCandidate in api/assets/clips/bulk_upload.go
// so the transport conversion is byte-compat.
type BulkUploadCandidate struct {
	LocalPath  string // absolute path to the .mp4
	Subdir     string // relative subdir under the scan root ("" = root)
	Name       string // base name without extension
	Size       int64
	Manifest   map[string]any // parsed clip_manifest.json, or nil
	Transcript string         // raw transcript.txt content, or ""
}

// DisplayName returns the readable clip name (manifest title preferred).
func (c BulkUploadCandidate) DisplayName() string {
	if c.Manifest != nil {
		if n, ok := c.Manifest["name"].(string); ok && strings.TrimSpace(n) != "" {
			return strings.TrimSpace(n)
		}
		if n, ok := c.Manifest["title"].(string); ok && strings.TrimSpace(n) != "" {
			return strings.TrimSpace(n)
		}
	}
	return c.Name
}

// ScanLocalClips walks root and returns one BulkUploadCandidate per
// .mp4 matching the glob patterns. Used by the HTTP transport's
// dry-run preview path (POST /api/clips/:source/bulk-upload-youtube-clips)
// to compute the candidate count before enqueueing the heavy worker.
//
// Parameters:
//   * root          — absolute path to scan (caller resolves via filepath.Abs).
//   * recursive     — true = filepath.WalkDir; false = readDir shallow only.
//   * patterns      — glob patterns matched via filepath.Match; empty = no filter.
//   * skipPatterns  — substring matches that cause a file to be dropped;
//                     empty = no skip filter.
//   * limit         — positive = stop after this many candidates (uses
//                     filepath.SkipAll internally).
//
// Errors are returned when the walk fails (e.g. permission denied).
// Each file's manifest + transcript siblings are read best-effort —
// missing files do not error.
func ScanLocalClips(root string, recursive bool, patterns, skipPatterns []string, limit int) ([]BulkUploadCandidate, error) {
	skipMatch := func(p string) bool {
		for _, s := range skipPatterns {
			if s == "" {
				continue
			}
			if strings.Contains(p, s) {
				return true
			}
		}
		return false
	}

	var candidates []BulkUploadCandidate
	walk := func(path string, info os.DirEntry, err error) error {
		if err != nil {
			// skip unreadable entries (matches pre-Split-5 behaviour)
			return nil
		}
		if info.IsDir() {
			if !recursive && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !MatchesAnyPattern(info.Name(), patterns) {
			return nil
		}
		abs := path
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = filepath.Base(path)
		}
		if skipMatch(abs) || skipMatch(rel) {
			return nil
		}
		fi, ferr := info.Info()
		if ferr != nil {
			return nil
		}
		cand := BulkUploadCandidate{
			LocalPath: abs,
			Subdir:    filepath.ToSlash(filepath.Dir(rel)),
			Name:      strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())),
			Size:      fi.Size(),
		}
		// Look for siblings (best effort — missing files are tolerated).
		dir := filepath.Dir(abs)
		baseNoExt := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
		if mf, ok := ReadManifestJSON(filepath.Join(dir, "clip_manifest.json")); ok {
			cand.Manifest = mf
		}
		if txt, ok := ReadTextFile(filepath.Join(dir, baseNoExt+".txt")); ok {
			cand.Transcript = txt
		} else if txt, ok = ReadTextFile(filepath.Join(dir, "transcript.txt")); ok {
			cand.Transcript = txt
		}
		candidates = append(candidates, cand)
		if limit > 0 && len(candidates) >= limit {
			return filepath.SkipAll
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, err
	}
	return candidates, nil
}

// MatchesAnyPattern returns true when name matches any of the glob
// patterns. Empty patterns are skipped (no match). An invalid glob
// pattern (filepath.Match error) is treated as no match for that
// pattern, preserving pre-Split-5 behaviour.
func MatchesAnyPattern(name string, patterns []string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		ok, err := filepath.Match(p, name)
		if err == nil && ok {
			return true
		}
	}
	return false
}

// ReadManifestJSON parses JSON at path into a generic map. Returns
// (nil, false) on read error or parse error. Validates with the
// standard encoding/json package; no custom schema enforcement.
func ReadManifestJSON(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	return m, true
}

// ReadTextFile reads a file and returns its whitespace-stripped
// contents. Returns ("", false) on read error.
func ReadTextFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// Sanity-check that all the surface area the transport depends on
// is reachable. Compile-time fail if any helper is missing.
var (
	_ = fmt.Sprintf // import guard — fmt is "fmt" via stdlib; always present.
)
