package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var RecorderFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "pipelinegen_observability_recorder_failures_total",
	Help: "Total number of best-effort canonical observability recorder failures.",
})
