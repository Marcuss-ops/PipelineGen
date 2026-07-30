package files

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"time"

	domainhashutil "github.com/Marcuss-ops/PipelineGen/internal/domain/remote/hashutil"
)

// RandomString generates a cryptographically random hex string of length n.
func RandomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%0*x", n, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:n]
}

// MD5File calculates the MD5 hash of a file.
func MD5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256File calculates the authoritative binary SHA-256 digest of a file.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256Bytes calculates the SHA-256 hash of a byte slice.
func SHA256Bytes(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// SHA256String calculates the SHA-256 hash of a string and returns the hex digest.
func SHA256String(text string) string {
	h := sha256.New()
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
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

// MD5String calculates the MD5 hash of a string.
func MD5String(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// NewSHA256Hasher returns the canonical SHA-256 hasher as a typed-port
// HashFunc value (declared in internal/domain/remote/hashutil). The
// function-value form keeps call-site allocation-free in the production
// hot path (the returned value points to the existing SHA256String
// function verbatim — Go's runtime treats function values as
// reference types, no copy).
//
// godlike/06 one-owner-per-fact: this is the canonical adapter. The
// domain layer uses the typed port (`hashutil.HashFunc`); the
// infrastructure layer adapts to it via this function. No other
// SHA-256-to-string adapter for the same shape should be introduced
// without an explicit follow-up commit.
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
