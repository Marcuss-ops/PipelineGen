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
const residentKeepAliveExtended = "24h"

// EnvResidentKeepAlive is the operator override for the resident keep_alive
// window (PipelineGen-owned PIPELINEGEN_* env namespace). Set it to a Go
// duration (e.g. "12h") to stop paying the measured model reload between jobs:
// the timing snapshot recorded
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

// validOllamaKeepAlive accepts durations supported by the deployed Ollama
// request parser: "0" (unload immediately), a bare integer (seconds) or a Go
// duration with one of the ms/s/m/h units. The documented "-1" sentinel is
// intentionally rejected: the production Ollama endpoint used by this service
// parses keep_alive as a duration and returned "missing unit" for that value.
func validOllamaKeepAlive(value string) bool {
	if value == "0" {
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

// effectiveOllamaKeepAlive maps the documented indefinite sentinel to the
// longest duration accepted by the Ollama version deployed here.
func effectiveOllamaKeepAlive(value string) string {
	value = strings.TrimSpace(value)
	if value == "-1" {
		return residentKeepAliveExtended
	}
	return value
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
