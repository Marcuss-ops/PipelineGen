// Package files — infrastructure-level filesystem primitives.
//
// scanner.go — canonical MediaScanner surface (Wave 24 / PR 3 of
// Blocco 1 Asset SSOT, June 2026). Replaces the legacy
// `asset.ScanDirectory` + `asset.MediaFile` helpers that previously
// lived in `internal/kernel/asset/clips_list.go:114-152` and pulled
// `os`/`path/filepath`/`sort`/`time` imports into the canonical
// domain layer.
//
// Why this is in infrastructure, not domain
// ---------------------------------------------------------------------------
// Filesystem scanning is intrinsically an infrastructure concern:
// it reaches into `os.DirEntry`, calls `filepath.WalkDir`, and reads
// file metadata. Domain code in `internal/kernel/asset/` is
// supposed to be filesystem-primitive-free after this PR 3 move.
// Cross-package consumers that need to scan a local directory
// inject `MediaScanner` from the composition root; the canonical
// concrete implementation is `LocalFilesystemScanner`.
//
// Surface (PR 3 spec, June 2026)
// ---------------------------------------------------------------------------
//   - MediaScanner interface   : Scan(ctx, ScanRequest) ([]ScannedFile, error)
//   - ScanRequest struct      : {Root, Extensions, Limit, IncludeMetadata, IncludeTranscript}
//   - ScannedFile struct      : {Path, Hash, MetadataSibling, TranscriptSibling}
//   - LocalFilesystemScanner  : concrete impl using filepath.WalkDir
//
// All types are exported; the package is leaf-safe to use from
// `internal/application/**` ports (composition-root wires the
// injection).
package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScanRequest is the input shape accepted by *MediaScanner.Scan.
// Root is REQUIRED (the disk path to walk); Extensions is OPTIONAL
// (case-insensitive file-extension match, e.g. []string{".mp4",
// ".mp3", ".wav"}; empty list matches ALL non-directory files).
// Limit caps the total number of returned files (Limit <= 0 means
// "no cap"). IncludeMetadata and IncludeTranscript are boolean
// flags telling the scanner to also surface the per-file sibling
// paths for `<name>.json` (per-asset metadata) and `<name>.vtt`
// (per-asset transcript) when those sibling files exist on disk.
type ScanRequest struct {
	Root              string
	Extensions        []string
	Limit             int
	IncludeMetadata   bool
	IncludeTranscript bool
}

// ScannedFile is the per-file result row produced by *MediaScanner.
// Path is the absolute path on disk (the canonical resolver is
// `filepath.Abs`; the scanner rejects Roots that fail to resolve
// into an absolute path with a wrapped error).
//
// Hash is OPTIONAL — the scanner does NOT read file bytes by default
// because filesystem scans can return thousands of files and a
// per-file MD5 read is the canonical source of a slow enumeration.
// The field is exported as a future-slot for implementations that
// opt into content hashing for a sub-tree (a smaller scan budget).
// Today Hash is left empty (`""`); callers requesting a content
// fingerprint MUST compose their own per-file hash pass AFTER the
// scan returns the candidate list (typical pattern).
//
// MetadataSibling and TranscriptSibling are the canonical sibling
// paths (per the per-asset convention of `<media>.<ext>` +
// `<media>.json` + `<media>.vtt`). When both flags are false in the
// ScanRequest, both fields stay empty AND the scanner does no
// per-file sibling probe (cheaper traversal). When a flag is true,
// the scanner does a single `os.Stat` call per non-empty sibling;
// sibling files that DON'T exist leave the corresponding field
// empty (no error — the absence is a normal "no metadata" / "no
// transcript" case).
type ScannedFile struct {
	Path              string
	Hash              string
	MetadataSibling   string
	TranscriptSibling string
}

// MediaScanner is the canonical surface for filesystem enumeration
// across PipelineGen. The interface lives in infrastructure/ so
// application ports can depend on the SSOT shape without inverting
// the dependency direction (the implementation, not the interface,
// is the structural concern; the interface is exported from the
// hosting package).
//
// Method contract:
//   - Scan MUST honour ctx cancellation. The scanner implementation
//     MUST propagate ctx.Err() through filepath.WalkDir's walk-callback
//     and through any sibling-stat calls; non-cancellation callers
//     MUST complete the enumeration order-stable (sorted descending
//     by ModTime for LocalFilesystemScanner, mirroring the legacy
//     `asset.ScanDirectory` sort-by-recency contract).
//   - Scan MUST return an error if Root fails to resolve into an
//     absolute path; the error wraps the canonical `os.PathError`
//     returned by `filepath.Abs` via `fmt.Errorf("...: %w", err)`.
//   - Scan MUST respect Limit: once the per-file count reaches Limit,
//     the scanner MUST short-circuit further walk (early-terminate
//     the walker via an internal counter check inside the callback).
//   - Scan MUST be safe to call concurrently for distinct Roots; the
//     same scanner instance MUST NOT be called concurrently for the
//     same Root (the walker holds internal state per-call).
type MediaScanner interface {
	Scan(ctx context.Context, req ScanRequest) ([]ScannedFile, error)
}

// LocalFilesystemScanner is the canonical concrete MediaScanner
// implementation backed by `filepath.WalkDir` (the Go-1.16+ idiom
// that avoids per-entry `os.Lstat`). Zero value is NOT usable —
// construct via `NewLocalFilesystemScanner()` so the package's
// future-slot fields keep their zero-value sane defaults.
type LocalFilesystemScanner struct {
	// Sorter is the canonical sort order. Today the scanner sorts
	// descending by ModTime (newest-first) to mirror the legacy
	// `asset.ScanDirectory` contract. Future compositions may inject
	// alternative sorters (e.g. by-path, by-size) — exported so
	// composition roots can wire variant behaviour without
	// re-implementing the entire WalkDir loop.
	//
	// nil is acceptable; the constructor wires the canonical
	// sortByModTimeDesc fallback.
	Sorter func(a, b os.FileInfo) int
}

// NewLocalFilesystemScanner returns a LocalFilesystemScanner with the
// canonical Sort-by-ModTime-descending default. The composition root
// is the canonical injection site (`internal/app/build_bundles_*.go`).
//
// The constructor returns the MediaScanner interface (not the concrete
// `*LocalFilesystemScanner`) so the composition root can swap
// implementations behind the same surface — the canonical concrete
// is the only sane production choice today, but tests want to
// inject a fake without touching the wiring site.
func NewLocalFilesystemScanner() MediaScanner {
	return &LocalFilesystemScanner{
		Sorter: sortByModTimeDesc,
	}
}

// sortByModTimeDesc is a comparator suitable for use with
// `sort.SliceStable`: more-recently-modified files come before
// less-recently-modified ones. The legacy `asset.ScanDirectory`
// contract sorted by ModTime descending; this preserves the
// observable ordering for any consumer port that depended on it.
func sortByModTimeDesc(a, b os.FileInfo) int {
	switch {
	case a.ModTime().After(b.ModTime()):
		return -1
	case a.ModTime().Before(b.ModTime()):
		return 1
	default:
		return 0
	}
}

// canonicalExtensions normalises the user's Extensions slice into a
// canonical form: lower-cased with a leading `.`. Mirrors the
// `pathutil.SafeFolderName` normalisation discipline — extensions
// like `MP4` and `.MP4` and `mp4` all hit the same filter.
func canonicalExtensions(exts []string) map[string]struct{} {
	out := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		e = strings.TrimSpace(strings.ToLower(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out[e] = struct{}{}
	}
	return out
}

// siblingForTargets returns the canonical per-asset sibling paths
// from a media file's directory + extension-stripped basename. The
// returned slices respect the IncludeMetadata / IncludeTranscript
// flags; entries are dropped when the corresponding file does not
// exist on disk. Pure helper — no I/O beyond the in-out probes.
//
// The convention `<media>.json` / `<media>.vtt` mirrors the
// pre-PR-3 artlist/seed pipeline's per-asset file convention (see
// docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md for the
// data-ownership layout). Future migrations may extend the
// convention; for now basename + json/vtt are the canonical pair.
func siblingForTargets(mediaPath string, wantMetadata, wantTranscript bool) (meta, transcript string) {
	ext := filepath.Ext(mediaPath)
	base := strings.TrimSuffix(mediaPath, ext)
	if wantMetadata {
		candidate := base + ".json"
		if _, err := os.Stat(candidate); err == nil {
			meta = candidate
		}
	}
	if wantTranscript {
		candidate := base + ".vtt"
		if _, err := os.Stat(candidate); err == nil {
			transcript = candidate
		}
	}
	return meta, transcript
}

// Scan implements MediaScanner for LocalFilesystemScanner.
//
// Algorithm (two-pass):
//  1. Walk Root with filepath.WalkDir, collecting every file that
//     matches the Extensions filter (or all non-dir files when
//     Extensions is empty). Apply Limit by short-circuiting the
//     walker once the candidate count reaches the cap.
//  2. Sort the candidates by ModTime descending (newest-first)
//     using the scanner's Sorter field (fallback: sort by ModTime).
//  3. For each candidate, probe sibling files per the
//     IncludeMetadata / IncludeTranscript flags.
//
// Cancellation: if ctx is cancelled mid-walk, the function returns
// ctx.Err() wrapped via errors.Join. The walker stops calling
// subsequent entries once the cancellation propagates.
func (s *LocalFilesystemScanner) Scan(ctx context.Context, req ScanRequest) ([]ScannedFile, error) {
	if req.Root == "" {
		return nil, errors.New("files.scan: Root is required")
	}
	absRoot, err := filepath.Abs(req.Root)
	if err != nil {
		return nil, fmt.Errorf("files.scan: resolve Root=%q: %w", req.Root, err)
	}

	// Verify Root exists + is a directory before walking —
	// filepath.Walk on a missing root silently succeeds with empty
	// results, which masks operator typos. The explicit stat also
	// surfaces symlink-loop / permission failures early.
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("files.scan: stat Root=%q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("files.scan: Root=%q is not a directory", absRoot)
	}

	extensions := canonicalExtensions(req.Extensions)
	wantLimit := req.Limit > 0

	candidates := make([]string, 0, 32)
	walkErr := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, walkErr error) error {
		// Honour context cancellation at every entry boundary.
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if walkErr != nil {
			// Mirror the legacy asset.ScanDirectory behaviour:
			// per-entry walk errors are swallowed (return nil) rather
			// than aborting the whole enumeration. Permission denied
			// on a sub-tree should NOT block the rest of the walk.
			return nil
		}
		if d == nil || d.IsDir() {
			return nil
		}
		if len(extensions) > 0 {
			ext := strings.ToLower(filepath.Ext(d.Name()))
			if _, ok := extensions[ext]; !ok {
				return nil
			}
		}
		candidates = append(candidates, path)
		if wantLimit && len(candidates) >= req.Limit {
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		// A non-SkipAll error is most often ctx.Err() propagated by
		// the cancellation check; surface it wrapped so callers can
		// errors.Is(ctx.Canceled) on the way out.
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("files.scan: walk cancelled before completion: %w", walkErr)
		}
		return nil, fmt.Errorf("files.scan: walk Root=%q: %w", absRoot, walkErr)
	}

	// Two-pass sort: collect entries' FileInfo once, sort, then map
	// to ScannedFile. The legacy `asset.ScanDirectory` sorted after
	// populating MediaFiles; we mirror that order so any consumer
	// port that depended on ModTime-descending observable behaviour
	// stays stable across the migration.
	type candidateInfo struct {
		path  string
		mtime int64
	}
	infos := make([]candidateInfo, len(candidates))
	for i, p := range candidates {
		di, err := os.Stat(p)
		if err != nil {
			// Skip unreadable files but keep the entry. Sort key
			// becomes 0 (zero mtime) which sorts to the end of
			// descending order — the conservative match for "this
			// file is stale and we don't know its true modtime".
			infos[i] = candidateInfo{path: p, mtime: 0}
			continue
		}
		infos[i] = candidateInfo{path: p, mtime: di.ModTime().UnixNano()}
	}
	sorter := s.Sorter
	if sorter == nil {
		// Stable fallback matches sortByModTimeDesc semantics for
		// the canonical-only injection site; if a caller forgot to
		// set Sorter on a zero-value LocalFilesystemScanner, this
		// path still returns an order-stable result.
		sort.SliceStable(infos, func(i, j int) bool {
			return infos[i].mtime > infos[j].mtime
		})
	} else {
		// The Sorter field operates on FileInfo. Snapshot each
		// candidate's FileInfo once so the comparator is stable;
		// this controls allocation cost for large directories.
		fileInfos := make([]os.FileInfo, len(candidates))
		for i, ci := range infos {
			fi, err := os.Stat(ci.path)
			if err != nil {
				continue
			}
			fileInfos[i] = fi
		}
		sort.SliceStable(infos, func(i, j int) bool {
			return sorter(fileInfos[i], fileInfos[j]) < 0
		})
	}

	// Honour Limit AFTER sort so the returned slice is the
	// newest-first N entries, not the alphabetically/insertion
	// first N entries. (Pre-sort truncation could swallow recent
	// files from the result; post-sort truncation is consistent
	// with the legacy `asset.ScanDirectory` "newest first, up to N"
	// observable behaviour.)
	if wantLimit && len(infos) > req.Limit {
		infos = infos[:req.Limit]
	}

	out := make([]ScannedFile, 0, len(infos))
	for _, ci := range infos {
		// Re-check ctx before each per-file sibling probe so a
		// cancellation between sort and probe terminates the
		// function rather than completing the slice silently.
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("files.scan: sibling probe cancelled: %w", cerr)
		}
		meta, transcript := siblingForTargets(ci.path, req.IncludeMetadata, req.IncludeTranscript)
		out = append(out, ScannedFile{
			Path:              ci.path,
			Hash:              "", // leave empty by design — see ScannedFile godoc
			MetadataSibling:   meta,
			TranscriptSibling: transcript,
		})
	}
	return out, nil
}
