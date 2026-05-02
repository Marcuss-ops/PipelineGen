package hashutil

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
)

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

// SHA256File calculates the SHA-256 hash of a file.
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

// SHA256Reader calculates the SHA-256 hash by reading from an io.Reader.
// Returns the hash string and the raw bytes read.
func SHA256Reader(r io.Reader) (string, []byte, error) {
	h := sha256.New()
	data, err := io.ReadAll(r)
	if err != nil {
		return "", nil, err
	}
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), data, nil
}

// SHA256Bytes calculates the SHA-256 hash of a byte slice.
func SHA256Bytes(data []byte) string {
	h := sha256.New()
	h.Write(data)
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
