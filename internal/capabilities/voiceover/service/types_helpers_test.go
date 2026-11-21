package voiceover

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Test-only oracles moved out of production per the dead-code sweep
// acceptance criterion: a helper called only by tests belongs in _test.go.

// fail normalises a failure to StatusFailed and preserves the specific code
// in Errors for the forensic trail.
func (i *BatchItem) fail(code FailureCode, err error) BatchItem {
	i.Status = Status(StatusFailed)
	if err != nil {
		i.Error = err.Error()
	}
	i.Errors = append(i.Errors, code)
	return *i
}

func randomSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	size := (n + 1) / 2
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().Format("150405")
	}
	return hex.EncodeToString(buf)[:n]
}
