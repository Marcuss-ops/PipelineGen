package digest

// verifier.go — the single process-lifetime authority for "this exact file has
// already been content-verified".
//
// Why this lives in the kernel: the SHA-256 of a file is the canonical content
// identity of the whole system, and more than one layer needs to confirm it
// (the Drive materializer before trusting a cache hit, the clip.render
// prepared-asset resolver before reusing a sealed source, the overlay resolver
// before uploading a watermark). When each layer grew its own memo the file got
// hashed once per layer per artifact, and the duplicate caches were free to
// disagree about invalidation. Here there is one cache and one invalidation
// rule.
//
// The contract: `Verify` returns the digest recorded when the bytes entered the
// system and re-reads the file only when the (size, modTime) pair no longer
// matches. Content-addressed artifacts are immutable once written, so in the
// steady state a batch of N jobs over one source performs ONE full read.
//
// Invalidation is deliberately conservative: any change to size or modification
// time forces a fresh hash, and a failure of the stat itself (missing file,
// directory, permission) is a typed error rather than a cache hit. The verifier
// never reports a digest it has not computed from the bytes it can currently
// see.

import (
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

// DefaultVerifierCapacity bounds the number of memoized file verifications. The
// working set is the distinct content addresses touched by one process; an
// eviction only costs one re-hash of the next artifact that touches the entry,
// so the bound can stay small.
const DefaultVerifierCapacity = 1024

// FileHasher computes the SHA-256 hex digest and byte size of the file at path.
// It mirrors SHA256File, which is the production implementation; callers may
// inject a counting hasher in tests to pin the "one read per file" contract.
type FileHasher func(path string) (sha256Hex string, size int64, err error)

// fileVerification is the memoized verification of one file.
type fileVerification struct {
	size    int64
	modTime time.Time
	sha256  string
}

// Verifier memoizes SHA-256 file verification behind a size+modtime validity
// check. A nil *Verifier is safe to call: Verify falls back to hashing through
// the canonical implementation, so the type can be embedded as an optional
// dependency without a nil guard at every call site.
type Verifier struct {
	hash  FileHasher
	cache *cacheutil.LRU
}

// NewVerifier constructs the verifier. hash may be nil, in which case
// SHA256File (the canonical implementation) is used.
func NewVerifier(hash FileHasher) *Verifier {
	if hash == nil {
		hash = SHA256File
	}
	return &Verifier{hash: hash, cache: cacheutil.NewLRU(DefaultVerifierCapacity)}
}

// Verify returns the SHA-256 hex digest and byte size of the file at path. A
// file already verified in this process whose size and modification time are
// unchanged is served from the memo without a second read; any change to the
// file invalidates the entry and forces a fresh hash.
func (v *Verifier) Verify(path string) (string, int64, error) {
	if path == "" {
		return "", 0, fmt.Errorf("digest verifier: path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	if info.IsDir() {
		return "", 0, fmt.Errorf("digest verifier: %q is a directory", path)
	}
	if v != nil && v.cache != nil {
		if cached, ok := v.cache.Get(path); ok {
			if entry, ok := cached.(fileVerification); ok &&
				entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
				return entry.sha256, entry.size, nil
			}
		}
	}
	sha, size, err := v.hashFile(path)
	if err != nil {
		return "", 0, err
	}
	if v != nil && v.cache != nil {
		v.cache.Put(path, fileVerification{size: size, modTime: info.ModTime(), sha256: sha})
	}
	return sha, size, nil
}

// hashFile applies the configured hasher, defaulting to the canonical
// implementation when the verifier (or its hasher) is nil.
func (v *Verifier) hashFile(path string) (string, int64, error) {
	if v == nil || v.hash == nil {
		return SHA256File(path)
	}
	return v.hash(path)
}
