package filesystem

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// RandomString generates a cryptographically random hex string of length n.
func RandomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%0*x", n, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:n]
}
