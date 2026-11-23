package jobs

// m2m_quota.go enforces the two caps an operator configures on an M2M client:
// quota_max_scenes (submitted jobs) and quota_max_total_secs (sum of declared
// scene seconds). Usage is read from the jobs table by the M2M store; this file
// only decides whether the NEXT enqueue fits.
//
// Zero means unlimited, matching the store's default handling. A payload whose
// seconds cannot be determined contributes 0 rather than a guessed value: the
// scene cap still applies, and a quota must never reject a job because an
// optional field was absent.

import (
	"fmt"
	"strings"

	mwm2m "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
)

// m2mQuotaDecision is the human-readable rejection body.
type m2mQuotaDecision struct {
	Quota   string
	Limit   int64
	Used    int64
	Request int64
}

func (d m2mQuotaDecision) Error() string {
	return fmt.Sprintf("%s quota exceeded: used %d + requested %d > limit %d", d.Quota, d.Used, d.Request, d.Limit)
}

// checkM2MQuota rejects an enqueue that would exceed either configured cap.
func checkM2MQuota(client *mwm2m.M2MClient, payload any) *m2mQuotaDecision {
	if client == nil {
		return nil
	}
	if client.QuotaMaxScenes > 0 && client.QuotaUsedScenes+1 > client.QuotaMaxScenes {
		return &m2mQuotaDecision{Quota: "scenes", Limit: client.QuotaMaxScenes, Used: client.QuotaUsedScenes, Request: 1}
	}
	if client.QuotaMaxTotalSecs > 0 {
		requested := estimatePayloadSeconds(payload)
		if requested > 0 && client.QuotaUsedTotalSecs+requested > client.QuotaMaxTotalSecs {
			return &m2mQuotaDecision{Quota: "total_secs", Limit: client.QuotaMaxTotalSecs, Used: client.QuotaUsedTotalSecs, Request: requested}
		}
	}
	return nil
}

// estimatePayloadSeconds looks for the canonical duration fields a scene plan
// uses. Any schema that does not declare a duration is valid and yields 0.
func estimatePayloadSeconds(payload any) int64 {
	root, ok := payload.(map[string]any)
	if !ok {
		return 0
	}
	for _, key := range []string{"duration_sec", "duration_seconds", "total_duration_sec", "total_duration_secs"} {
		if seconds, ok := numericSeconds(root[key]); ok {
			return seconds
		}
	}
	for _, key := range []string{"scenes", "segments", "items"} {
		if list, ok := root[key].([]any); ok {
			var total int64
			for _, item := range list {
				entry, ok := item.(map[string]any)
				if !ok {
					continue
				}
				for _, durationKey := range []string{"duration_sec", "duration_seconds", "seconds"} {
					if seconds, ok := numericSeconds(entry[durationKey]); ok {
						total += seconds
						break
					}
				}
			}
			return total
		}
	}
	return 0
}

func numericSeconds(v any) (int64, bool) {
	switch value := v.(type) {
	case float64:
		if value > 0 {
			return int64(value), true
		}
	case int64:
		if value > 0 {
			return value, true
		}
	case int:
		if value > 0 {
			return int64(value), true
		}
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return 0, false
		}
		var parsed float64
		if _, err := fmt.Sscanf(trimmed, "%f", &parsed); err == nil && parsed > 0 {
			return int64(parsed), true
		}
	}
	return 0, false
}
