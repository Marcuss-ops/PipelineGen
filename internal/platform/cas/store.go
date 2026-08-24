package cas

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/staging"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Directory and file permissions for the CAS layout. 0700/0600 mirrors the
// LocalStore staging discipline (minimal exposure, fail-closed).
const (
	// RootDirMode is the permission for the CAS root and shard directories.
	RootDirMode = 0o700
	// ObjectFileMode is the permission for stored objects.
	ObjectFileMode = 0o600
)

// Object describes a stored (or attempted) content object.
type Object struct {
	SHA256    string // canonical 64-hex address
	SizeBytes int64  // size of the object bytes
	Exists    bool   // true when the address is present in the store
	Dedup     bool   // Put hit an existing object with identical bytes
}

// Config is the CAS store constructor envelope.
type Config struct {
	// Root is the canonical CAS directory. Objects land at
	// <Root>/<ab>/<cd>/<sha256>.
	Root string

	// Stager is the canonical atomic write path being reused: the existing
	// LocalStore implementation of staging.Stager. Its workspace should be
	// on the same filesystem as Root (typically <Root>/.staging) so the
	// final os.Link stays on one device; a cross-device workspace falls
	// back to a copy-into-shard + link.
	Stager staging.Stager
}

// Store is the immutable, content-addressed CAS store. Concurrency-safe
// post-construction: the only mutable state is the filesystem itself, and
// all write paths are guarded by atomic no-overwrite primitives.
type Store struct {
	root   string
	stager staging.Stager
}

// NewStore constructs the CAS store. Fail-fast (godlike/07): a missing root
// or a nil Stager is a composition error, never a silent fallback.
func NewStore(cfg Config) (*Store, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, fmt.Errorf("%w: Config.Root is empty", ErrInvalidConfig)
	}
	if cfg.Stager == nil {
		return nil, fmt.Errorf("%w: wire the LocalStore (staging.Stager) as the atomic write path", ErrStagerRequired)
	}
	if err := os.MkdirAll(cfg.Root, RootDirMode); err != nil {
		return nil, fmt.Errorf("cas: mkdir root %s: %w", cfg.Root, err)
	}
	return &Store{root: filepath.Clean(cfg.Root), stager: cfg.Stager}, nil
}

// Put streams content into the CAS store and returns the object addressed
// by its SHA-256. The write path reuses the LocalStore staging discipline
// (stream-through-hash + fsync + atomic rename) and lands the object at
// <root>/<ab>/<cd>/<sha256> via os.Link, which cannot overwrite an existing
// address:
//
//   - first writer wins (committed);
//   - a concurrent/prior writer with identical bytes yields a verified
//     dedup hit (Dedup=true) — no duplicate bytes are stored;
//   - different bytes at an existing address yield ErrCorruption.
//
// After commit the on-disk bytes are re-hashed and MUST equal the address;
// a mismatch removes the object and returns ErrCorruption (verify hash
// after write, fail-closed).
func (s *Store) Put(ctx context.Context, content io.Reader) (Object, error) {
	if s == nil || s.root == "" || s.stager == nil {
		return Object{}, ErrNotWired
	}
	if content == nil {
		return Object{}, fmt.Errorf("%w: nil content reader", ErrInvalidInput)
	}

	id, err := newStagingID()
	if err != nil {
		return Object{}, err
	}
	receipt, err := s.stager.Stage(ctx, staging.StageInput{
		ArtifactID: id,
		MIME:       "application/octet-stream",
		Content:    content,
	})
	if err != nil {
		return Object{}, fmt.Errorf("cas: stage: %w", err)
	}
	// The staged file lives in the stager's workspace; the object's
	// canonical location is the sharded address. Remove the workspace
	// artifact on every path (including dedup hit and corruption).
	defer func() { _ = os.Remove(receipt.LocalPath) }()

	if !isValidSHA256(receipt.Hash) {
		// A stager returning garbage is a stager-contract violation, not
		// content corruption (corruption means bytes != address).
		return Object{}, fmt.Errorf("%w: stager returned malformed hash %q", ErrInvalidInput, receipt.Hash)
	}
	if receipt.Size <= 0 {
		return Object{}, fmt.Errorf("%w: stager returned a zero-byte object", ErrEmptyContent)
	}

	target, err := shardPath(s.root, receipt.Hash)
	if err != nil {
		return Object{}, err
	}

	// Immutability under concurrency: os.Link is atomic and fails with
	// EEXIST when the address already exists (a no-overwrite primitive).
	committed, err := s.linkIntoPlace(receipt.LocalPath, target)
	if err != nil {
		return Object{}, err
	}
	if !committed {
		// Address already present: identical bytes are a dedup hit;
		// different bytes at the same address are corruption.
		match, size, vErr := s.verifyAddress(target, receipt.Hash)
		if vErr != nil {
			return Object{}, vErr
		}
		if !match {
			return Object{}, fmt.Errorf("%w: existing object at %s differs from %q", ErrCorruption, target, receipt.Hash)
		}
		return Object{SHA256: receipt.Hash, SizeBytes: size, Exists: true, Dedup: true}, nil
	}

	// Post-write verification (DoD "verify hash after write"): re-read the
	// bytes at the canonical address and confirm digest + size.
	onDisk, size, vErr := hashFile(target)
	if vErr != nil {
		_ = os.Remove(target)
		pruneShardDirs(target)
		return Object{}, fmt.Errorf("cas: post-write verify: %w", vErr)
	}
	if onDisk != receipt.Hash || size != receipt.Size {
		_ = os.Remove(target)
		pruneShardDirs(target)
		return Object{}, fmt.Errorf("%w: post-write verify failed (wrote hash=%q size=%d, read hash=%q size=%d)",
			ErrCorruption, receipt.Hash, receipt.Size, onDisk, size)
	}
	return Object{SHA256: receipt.Hash, SizeBytes: size, Exists: true}, nil
}

// linkIntoPlace links the staged file to its canonical address with
// no-overwrite semantics. Returns committed=true when this call created the
// object, committed=false when the address already existed.
func (s *Store) linkIntoPlace(staged, target string) (bool, error) {
	// Ensure the shard directory exists before linking; os.Link fails with
	// ENOENT when the target's parent is absent.
	if err := os.MkdirAll(filepath.Dir(target), RootDirMode); err != nil {
		return false, fmt.Errorf("cas: mkdir shard dir %s: %w", filepath.Dir(target), err)
	}
	err := os.Link(staged, target)
	if err == nil {
		// Best-effort directory fsync so the new directory entry is durable
		// (mirrors the LocalStore staging discipline this package reuses).
		syncDirBestEffort(filepath.Dir(target))
		return true, nil
	} else if errors.Is(err, os.ErrExist) {
		return false, nil
	} else if errors.Is(err, syscall.EXDEV) {
		// Cross-device workspace: copy into the shard directory (same
		// filesystem as the target) and link from there, preserving the
		// EEXIST no-overwrite semantics.
		tmp, copyErr := copyFile(staged, filepath.Dir(target))
		if copyErr != nil {
			return false, copyErr
		}
		defer func() { _ = os.Remove(tmp) }()
		if err := os.Link(tmp, target); err == nil {
			syncDirBestEffort(filepath.Dir(target))
			return true, nil
		} else if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("cas: link %s: %w", target, err)
	}
	return false, fmt.Errorf("cas: link %s: %w", target, err)
}

// verifyAddress hashes the object at an existing address and reports
// whether the bytes match the expected digest.
func (s *Store) verifyAddress(target, expected string) (bool, int64, error) {
	onDisk, size, err := hashFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, 0, fmt.Errorf("%w: object vanished at %s", ErrObjectNotFound, target)
		}
		return false, 0, fmt.Errorf("cas: verify %s: %w", target, err)
	}
	return onDisk == expected, size, nil
}

// shardPath resolves a sha256 address to its canonical file path.
func shardPath(root, sha256 string) (string, error) {
	if !isValidSHA256(sha256) {
		return "", fmt.Errorf("%w: %q", ErrInvalidSHA256, sha256)
	}
	return filepath.Join(root, sha256[0:2], sha256[2:4], sha256), nil
}

// isValidSHA256 reports whether s is exactly 64 lowercase hex characters.
// Delegates to the canonical kernel/asset.ValidateSHA256 (godlike/06 SSOT for
// the 64-lowercase-hex SHA-256 shape) so the CAS shard layout cannot drift
// from the canonical digest contract.
func isValidSHA256(s string) bool {
	_, err := asset.ValidateSHA256(s)
	return err == nil
}

// newStagingID generates a unique workspace staging ID in the canonical
// `art_<...>` shape required by staging.ValidateArtifactIDFormat.
func newStagingID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cas: generate staging id: %w", err)
	}
	return "art_cas_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + hex.EncodeToString(b), nil
}

// hashFile computes the SHA-256 of the file at path and its size.
func hashFile(path string) (sha256hex string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// syncDirBestEffort fsyncs a directory so a newly linked/renamed entry is
// durable. Advisory: some filesystems reject directory sync; failures are
// ignored, matching the boot-recovery posture of the LocalStore helpers.
func syncDirBestEffort(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Sync()
}

// pruneShardDirs best-effort removes the (now possibly empty) ab/cd shard
// directories after an object was removed.
func pruneShardDirs(target string) {
	_ = os.Remove(filepath.Dir(target))
	_ = os.Remove(filepath.Dir(filepath.Dir(target)))
}

// copyFile copies src into a fresh temp file in dir (fsync + close), used
// for the cross-device fallback. The caller owns the returned path.
func copyFile(src, dir string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("cas: open %s for copy: %w", src, err)
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".cas-copy-*")
	if err != nil {
		return "", fmt.Errorf("cas: create copy tmp in %s: %w", dir, err)
	}
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }
	if _, err := io.Copy(tmp, in); err != nil {
		cleanup()
		return "", fmt.Errorf("cas: copy %s: %w", src, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("cas: fsync copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("cas: close copy: %w", err)
	}
	_ = os.Chmod(tmp.Name(), ObjectFileMode)
	return tmp.Name(), nil
}
