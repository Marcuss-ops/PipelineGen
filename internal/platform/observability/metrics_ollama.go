// metrics_ollama.go owns the PROMETHEUS projection of the Ollama in-flight
// admission budget (translation-bottleneck fix, Sept 2026).
//
// Why these exist: the budget in internal/platform/ollama/client/admission.go
// is the ONLY thing that keeps every pool (script narration, NLP, translation,
// cues, embeddings) inside the server's real capacity (OLLAMA_NUM_PARALLEL).
// Without a metric, an operator cannot tell "the server is the bottleneck" from
// "my pool is slow" — which is exactly the diagnosis that was missing when the
// translation phase looked slow. A saturated budget shows up as
// `ollama_admission_in_flight == ollama_admission_limit` with
// `ollama_admission_deferred_total` climbing.
//
// Bounded labels only: `endpoint` is the configured Ollama URL (one or two
// values in any deployment, never a job id, a model tag or a free-form error).
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// OllamaAdmissionLimit is the resolved ceiling for an endpoint: the
	// operator's VELOX_OLLAMA_MAX_INFLIGHT, else the server's
	// OLLAMA_NUM_PARALLEL, else the certified default. It is set once per
	// endpoint when the limiter is created.
	OllamaAdmissionLimit = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ollama_admission_limit",
		Help: "Resolved in-flight Ollama admission ceiling per endpoint (the server's runner slots).",
	}, []string{"endpoint"})

	// OllamaAdmissionInFlight is how many Ollama requests are admitted right
	// now. Reading it against ollama_admission_limit is the direct answer to
	// "is the model server saturated?".
	OllamaAdmissionInFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ollama_admission_in_flight",
		Help: "Ollama requests currently admitted (holding a runner slot) per endpoint.",
	}, []string{"endpoint"})

	// OllamaAdmissionDeferredTotal counts requests that had to queue because
	// the endpoint was already at its ceiling. A non-zero rate means the pools
	// are overlapping more than the server can serve: their extra width buys
	// queueing, not throughput.
	OllamaAdmissionDeferredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ollama_admission_deferred_total",
		Help: "Ollama requests that had to wait for an admission slot, per endpoint.",
	}, []string{"endpoint"})
)
