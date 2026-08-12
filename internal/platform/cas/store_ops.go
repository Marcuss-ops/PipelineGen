package cas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalPath resolves a sha256 address to its canonical file path without
// touching the filesystem. Callers use it for tooling (backup, ingest
// streams) that need the on-disk location.
func (s *Store) LocalPath(sha256 string) (string, error) {
	if s == nil || s.root == "" {
		return "", ErrNotWired
	}
	return shardPath(s.root, sha256)
}

// Open returns a reader for the object at sha256. The reader yields the
// raw bytes; integrity verification is a separate concern (Verify /
// IntegrityScan).
func (s *Store) Open(ctx context.Context, sha256 string) (io.ReadCloser, error) {
	if s == nil || s.root == "" {
		return nil, ErrNotWired
	}
	target, err := shardPath(s.root, sha256)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %q", ErrObjectNotFound, sha256)
		}
		return nil, fmt.Errorf("cas: open %s: %w", target, err)
	}
	return f, nil
}

// Stat returns object metadata without reading content. A missing address
// yields Object{Exists: false} with no error.
func (s *Store) Stat(ctx context.Context, sha256 string) (Object, error) {
	if s == nil || s.root == "" {
		return Object{}, ErrNotWired
	}
	target, err := shardPath(s.root, sha256)
	if err != nil {
		return Object{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Object{SHA256: sha256, Exists: false}, nil
		}
		return Object{}, fmt.Errorf("cas: stat %s: %w", target, err)
	}
	return Object{SHA256: sha256, SizeBytes: info.Size(), Exists: true}, nil
}

// Exists reports whether the address has an object.
func (s *Store) Exists(ctx context.Context, sha256 string) (bool, error) {
	obj, err := s.Stat(ctx, sha256)
	if err != nil {
		return false, err
	}
	return obj.Exists, nil
}

// VerifyResult reports the integrity of one object at an address.
type VerifyResult struct {
	SHA256    string
	Exists    bool
	Verified  bool // on-disk bytes match the address
	SizeBytes int64
}

// Verify re-hashes the on-disk bytes at sha256 and reports whether they
// match the address. A missing object yields VerifyResult{Exists: false}
// with no error (absent is not corruption).
func (s *Store) Verify(ctx context.Context, sha256 string) (VerifyResult, error) {
	if s == nil || s.root == "" {
		return VerifyResult{}, ErrNotWired
	}
	target, err := shardPath(s.root, sha256)
	if err != nil {
		return VerifyResult{}, err
	}
	onDisk, size, err := hashFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return VerifyResult{SHA256: sha256, Exists: false}, nil
		}
		return VerifyResult{}, fmt.Errorf("cas: verify %s: %w", target, err)
	}
	return VerifyResult{SHA256: sha256, Exists: true, Verified: onDisk == sha256, SizeBytes: size}, nil
}

// Delete removes the object at sha256. Idempotent: deleting a missing
// object is a no-op success (physical cleanup only; registry-row cleanup
// belongs to the caller). Empty shard directories are pruned best-effort.
func (s *Store) Delete(ctx context.Context, sha256 string) error {
	if s == nil || s.root == "" {
		return ErrNotWired
	}
	target, err := shardPath(s.root, sha256)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("cas: delete %s: %w", target, err)
	}
	// Best-effort prune of the (now possibly empty) shard dirs.
	pruneShardDirs(target)
	return nil
}

// ScanEntry is one row of the CAS integrity sweep.
type ScanEntry struct {
	SHA256    string
	SizeBytes int64
	Verified  bool   // bytes match the address AND the file sits in the correct shard
	Misplaced bool   // bytes match but the file is not at <root>/<ab>/<cd>/<hash>
	Error     string // non-empty when the object could not be read
}

// ScanResult aggregates the CAS integrity sweep (the cas-verify primitive).
type ScanResult struct {
	Total      int // objects found under the root
	Verified   int // bytes match the address and placement
	Corrupt    int // bytes differ from the address
	Misplaced  int // bytes match but the object is unreachable at its canonical path
	Unreadable int // read failure (I/O error, not corruption)
	Entries    []ScanEntry
}

// IntegrityScan walks the whole CAS root, re-hashes every object, and
// classifies it verified/corrupt/unreadable. This is the recovery/audit
// primitive behind "CAS VERIFY" reporting: per-object hash(file) == address.
// Walk errors are collected per-entry (best-effort continue); only a root
// walk failure aborts.
func (s *Store) IntegrityScan(ctx context.Context) (ScanResult, error) {
	if s == nil || s.root == "" {
		return ScanResult{}, ErrNotWired
	}
	var res ScanResult
	err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // best-effort: keep scanning
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !isValidSHA256(name) {
			return nil // not an object (tmp files, copies, foreign files)
		}
		res.Total++
		onDisk, size, hErr := hashFile(path)
		entry := ScanEntry{SHA256: name, SizeBytes: size}
		// Placement check: a 64-hex-named file only counts as the object if
		// it sits at <root>/<ab>/<cd>/<hash> — a mis-sharded file is
		// unreachable via Open (which resolves by address) even when its
		// bytes match its name.
		expected := filepath.Join(s.root, name[0:2], name[2:4], name)
		misplaced := filepath.Clean(path) != filepath.Clean(expected)
		switch {
		case hErr != nil:
			entry.Error = hErr.Error()
			res.Unreadable++
		case onDisk != name:
			res.Corrupt++
		case misplaced:
			entry.Misplaced = true
			res.Misplaced++
		default:
			entry.Verified = true
			res.Verified++
		}
		res.Entries = append(res.Entries, entry)
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("cas: integrity scan: %w", err)
	}
	return res, nil
}
