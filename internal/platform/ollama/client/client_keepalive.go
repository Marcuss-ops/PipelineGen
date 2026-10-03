package client

import (
	"os"
	"strings"
	"sync"

	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"

	"go.uber.org/zap"
)

// residentKeepAliveDefault is the historical residency window every request
// gets when the caller does not pass an explicit keep_alive: Ollama unloads a
// resident model after this idle window. 30 minutes is the certified default
// and stays the fallback — an explicit operator override must be deliberate.
const residentKeepAliveDefault = "30m"

// EnvResidentKeepAlive is the operator override for the resident keep_alive
// window (PipelineGen-owned PIPELINEGEN_* env namespace). Set it to a Go
// duration (e.g. "12h") or "-1" (keep resident until the server stops) to stop
// paying the measured model reload between jobs: the timing snapshot recorded
// 129 `ollama/warm` operations averaging 27.3s (max 600s) plus 38s cold loads
// inside the first generate of a job, all of which are idle-unload reloads a
// longer residency window makes disappear.
const EnvResidentKeepAlive = "PIPELINEGEN_OLLAMA_KEEP_ALIVE"

var (
	envKeepAliveOnce sync.Once
	envKeepAlive     string
)

// residentKeepAlive returns the effective default keep_alive window: the env
// override when it is a valid Ollama duration, the certified 30m default
// otherwise. A malformed value is ignored with a warning rather than failing
// every later request at the Ollama endpoint.
func residentKeepAlive() string {
	envKeepAliveOnce.Do(func() {
		envKeepAlive = residentKeepAliveDefault
		raw := strings.TrimSpace(os.Getenv(EnvResidentKeepAlive))
		if raw == "" {
			return
		}
		if !validOllamaKeepAlive(raw) {
			logger.Warn("ignoring invalid ollama keep_alive override",
				zap.String("env", EnvResidentKeepAlive),
				zap.String("value", raw),
				zap.String("fallback", residentKeepAliveDefault))
			return
		}
		envKeepAlive = raw
	})
	return envKeepAlive
}

// validOllamaKeepAlive accepts the values Ollama's keep_alive contract allows
// at the request level: "-1" (resident until server stop), "0" (unload
// immediately), a bare integer (seconds) or a Go duration with one of the
// ms/s/m/h units.
func validOllamaKeepAlive(value string) bool {
	if value == "-1" || value == "0" {
		return true
	}
	if allDigits(value) {
		return true
	}
	for _, unit := range []string{"ms", "s", "m", "h"} {
		if !strings.HasSuffix(value, unit) {
			continue
		}
		numeric := strings.TrimSuffix(value, unit)
		return allDigits(numeric) || fractionalSeconds(numeric)
	}
	return false
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func fractionalSeconds(value string) bool {
	dot := strings.IndexByte(value, '.')
	if dot < 1 || dot == len(value)-1 {
		return false
	}
	return allDigits(value[:dot]) && allDigits(value[dot+1:])
}

// resetEnvKeepAliveForTest re-arms the once-guard so a test that changes the
// environment observes the new value. Production callers never need this.
func resetEnvKeepAliveForTest() {
	envKeepAliveOnce = sync.Once{}
	envKeepAlive = ""
}
