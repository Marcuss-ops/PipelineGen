package script

import (
	"github.com/gin-gonic/gin"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var legacyRouteInvocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "legacy_route_invocations_total",
	Help: "Monotonic counter for deprecated script-generation route invocations, by route name.",
}, []string{"route"})

// DeprecationCount returns the cumulative invocation count across the active
// legacy script-generation adapter routes.
func DeprecationCount() int64 {
	var total int64
	for _, route := range []string{"generate-from-clips", "generate-with-images"} {
		counter, err := legacyRouteInvocationsTotal.GetMetricWithLabelValues(route)
		if err != nil {
			continue
		}
		var m dto.Metric
		if err := counter.Write(&m); err != nil {
			continue
		}
		total += int64(m.GetCounter().GetValue())
	}
	return total
}

func addDeprecationHeader(c *gin.Context, route string, removalDate string) {
	legacyRouteInvocationsTotal.WithLabelValues(route).Inc()
	c.Header("X-Deprecated", "true")
	c.Header("X-Deprecation-Notice",
		"POST /api/script/generate is the canonical endpoint. "+
			"This route will be removed on "+removalDate+".")
}

const (
	removalDateFromClips  = "2026-12-31"
	removalDateWithImages = "2026-12-31"
)
