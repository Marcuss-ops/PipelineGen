package files

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"time"

	domainhashutil "github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote/hashutil"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
)

// RandomString generates a cryptographically random hex string of length n.
func RandomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%0*x", n, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:n]
}

// LegacyMD5File streams the MD5 digest of a file. COMPATIBILITY-ONLY
// (godlike/07 + mediaregistry semantics): MD5 is never an identity or dedup
// signal — content identity is SHA-256 (internal/kernel/digest). This
// function exists solely for legacy DB columns, legacy import paths, and
// pre-existing local fingerprints, and delegates to the canonical MD5 owner
// internal/platform/checksum.
func LegacyMD5File(path string) (string, error) {
	return checksum.LegacyMD5File(path)
}

// SHA256File calculates the authoritative binary SHA-256 digest of a file by
// streaming it through the kernel digest SSOT (internal/kernel/digest). It
// never buffers the whole file in memory.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return digest.SHA256Reader(f)
}

// SHA256Bytes calculates the SHA-256 hash of a byte slice, delegating to the
// kernel digest SSOT (internal/kernel/digest).
func SHA256Bytes(data []byte) string {
	return digest.SHA256Bytes(data)
}

// SHA256String calculates the SHA-256 hash of a string and returns the hex
// digest, delegating to the kernel digest SSOT (internal/kernel/digest).
func SHA256String(text string) string {
	return digest.SHA256String(text)
}

// HashFile calculates the hash of a file using the specified hash function.
func HashFile(path string, h hash.Hash) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// LegacyMD5String returns the MD5 digest of a string. COMPATIBILITY-ONLY:
// MD5 is never an identity or dedup signal — see LegacyMD5File. Delegates to
// the canonical MD5 owner internal/platform/checksum.
func LegacyMD5String(s string) string {
	return checksum.LegacyMD5String(s)
}

// NewSHA256Hasher returns the canonical SHA-256 hasher as a typed-port
// HashFunc value (declared in internal/domain/remote/hashutil). The
// function-value form keeps call-site allocation-free in the production
// hot path (the returned value points to the existing SHA256String
// function verbatim — Go's runtime treats function values as
// reference types, no copy).
//
// godlike/06 one-owner-per-fact: the ALGORITHM is owned by
// internal/kernel/digest; SHA256String (and therefore this adapter) only
// delegates to it. The domain layer uses the typed port
// (`hashutil.HashFunc`); the infrastructure layer adapts to it via this
// function. No other SHA-256-to-string adapter for the same shape should
// be introduced without an explicit follow-up commit.
//
// Domain callers receive this via constructor injection:
//
//	derive := remote.MakeArtifactIdempotencyKey(files.NewSHA256Hasher())
//	key := derive(jobID, artifactID, sha256Hex)
//
// Test callers inject a FAKE for unit-test isolation (per Commit D
// spec literal: "Aggiungi un test unit con fake `HashFunc`").
func NewSHA256Hasher() domainhashutil.HashFunc {
	return SHA256String
}
