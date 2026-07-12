package operations

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func defaultJobIDGen() string {
	return generateID("job")
}

func defaultOperationIDGen() string {
	return generateID("op")
}

func generateID(prefix string) string {
	now := time.Now().UnixNano()
	suffix := randomHexSuffix(4)
	return fmt.Sprintf("%s_%d_%s", prefix, now, suffix)
}

// randomHexSuffix falls back to time-derived bytes when crypto/rand is
// unavailable so operational IDs remain non-empty.
func randomHexSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		ns := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte(ns >> (uint(i) * 8))
		}
	}
	return hex.EncodeToString(buf)
}
