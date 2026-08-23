// Package digest owns the pure SHA-256 primitive used by leaf packages.
// Domain meaning and higher-level fingerprints remain owned by their callers.
package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
)

func NewSHA256() hash.Hash { return sha256.New() }

func SHA256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func SHA256String(text string) string { return SHA256Bytes([]byte(text)) }

func SHA256Reader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func SHA256File(path string) (string, int64, error) {
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
