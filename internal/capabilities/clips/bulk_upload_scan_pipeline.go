// Package clips (bulk_upload_scan_pipeline) — internal scanner section of
// the "bulk_upload_youtube_clips" bg-job pipeline.
//
// P1.7 (July 2026): the scanner was extracted from
// internal/application/clips/bulk_upload_worker.go as part of the
// 7-file worker-pipeline split. Each step of the per-clip pipeline
// (scan / publish / sidecar-publish / register / enrich / result) is
// now its own file; worker.go carries only the struct + ctor + HandleJob
// orchestrator + processOneClip stitch.
//
// DIFFERENT from the EXPORTED bulk_upload_scanner.go in this package:
// that file serves the HTTP transport's dry-run preview path
// (BulkUploadCandidate + ScanLocalClips + MatchesAnyPattern + …).
// The functions in THIS file serve the WORKER pipeline only and use
// the private clipCandidate struct (defined in bulk_upload_helpers.go)
// with substring-based include/skip matching (the worker pipeline
// does not pre-glob like the transport does).
//
// No new abstractions — top-level helper functions only.
package clips

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// scanLocalClips walks root and returns one clipCandidate per .mp4
// matching the include / skip filters. Used by HandleJob to produce
// the per-clip work queue before the goroutine fan-out.
//
// Parameters mirror the worker's payload field set verbatim:
//   - root           — absolute path to scan
//   - recursive      — true = filepath.WalkDir; false = readDirShallow
//   - include        — substring patterns that match includes
//   - skip           — substring patterns that match skips
//   - limit          — positive = stop after this many candidates
func scanLocalClips(root string, recursive bool, include, skip []string, limit int) ([]clipCandidate, error) {
	if root == "" {
		return nil, fmt.Errorf("root is empty")
	}
	out := []clipCandidate{}
	count := 0

	walk := func(path string, info os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".mp4") {
			return nil
		}
		if len(include) > 0 && !matchAny(info.Name(), include) {
			return nil
		}
		if len(skip) > 0 && matchAny(info.Name(), skip) {
			return nil
		}
		if limit > 0 && count >= limit {
			return filepath.SkipDir
		}
		subdir, _ := filepath.Rel(root, filepath.Dir(path))
		manifest := readManifest(filepath.Join(filepath.Dir(path), "clip_manifest.json"))
		transcript := readTranscript(filepath.Join(filepath.Dir(path), strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))+".txt"), path)
		out = append(out, clipCandidate{
			Name:       strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())),
			LocalPath:  path,
			Subdir:     subdir,
			Manifest:   manifest,
			Transcript: transcript,
		})
		count++
		return nil
	}
	var err error
	if recursive {
		err = filepath.WalkDir(root, walk)
	} else {
		err = readDirShallow(root, walk)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// readDirShallow reads root non-recursively and invokes walk for each
// immediate entry. Used when payload.Recursive is false.
func readDirShallow(root string, walk func(path string, info os.DirEntry, err error) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(root, e.Name())
		if err := walk(full, e, nil); err != nil {
			if err == filepath.SkipDir {
				continue
			}
			return err
		}
	}
	return nil
}

// matchAny returns true when name contains (case-insensitive) any
// of the provided patterns. Worker-pipeline semantics: include and
// skip filters are substring matches, NOT globs (the EXPORTED
// bulk_upload_scanner.go provides glob matching for the HTTP
// preview path).
func matchAny(name string, patterns []string) bool {
	lower := strings.ToLower(name)
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// readManifest reads a clip_manifest.json and unmarshals into a
// generic map. Missing or malformed file returns nil — manifest
// is best-effort context, NOT a hard requirement.
//
// Uses the package's `jsonUnmarshal` helper (a thin wrapper over
// encoding/json in json_helpers.go) instead of stdlib json.Unmarshal
// directly to keep the alias system intact — it owns the canonical
// "decode a clip manifest" surface for the package; future callers
// should use the alias too.
func readManifest(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := jsonUnmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// readTranscript reads a per-clip transcript.txt sibling. Falls back
// to a generic "transcript.txt" if the per-name one is missing.
// Empty result is OK (enrichment section decides what to do with it).
func readTranscript(txtPath, mp4Path string) string {
	for _, p := range []string{txtPath, filepath.Join(filepath.Dir(mp4Path), "transcript.txt")} {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return string(b)
		}
	}
	return ""
}
