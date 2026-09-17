package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metrics_render.go owns the PROMETHEUS projection of the overlay render lane:
// the blocking Chronon hand-off PipelineGen performs, and the render/encode
// phases the RenderingGen worker measures on the GPU.
//
// Why this is a separate file rather than metrics_scripts.go: these families
// describe the RENDER boundary, not script generation. The values are
// owner-measured — PipelineGen only reports the wall time it actually spends
// inside the boundary, and the Chronon render/encode phases are relayed from
// the certified artifact (render_ms / encode_ms / frame_count), never re-timed
// here. That keeps one measurement authority per phase.
//
// Bounded labels only. `outcome` is a closed enum (success | failure) and
// `backend` is the render contract's own vocabulary (chronon_vulkan today);
// neither ever carries a job id, a plan id or a free-form error string, so the
// series cardinality stays bounded no matter how many runs execute.
var (
	// OverlayRenderTotal counts every BLOCKING overlay render boundary outcome.
	// A non-zero failure rate is the first signal that the overlay lane is not
	// delivering, independent of whether the run retried.
	OverlayRenderTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "overlay_render_total",
		Help: "Total number of blocking overlay render attempts, by bounded outcome.",
	}, []string{"outcome"})

	// OverlayRenderDurationSeconds is the wall time of the blocking boundary:
	// submit plus the wait for the remote GPU. It is the single longest wait in
	// a script run, and it is NOT the GPU render time (that is
	// chronon_render_seconds); the delta between the two is queue/transport.
	OverlayRenderDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "overlay_render_duration_seconds",
		Help:    "Wall time of the blocking overlay render boundary (submit + remote GPU wait).",
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600},
	})

	// OverlayRenderItems is the size of the semantic plan handed to the
	// renderer. It makes an overlay workload regression visible (for example a
	// plan that suddenly carries only one entity card) without reading a job
	// payload.
	OverlayRenderItems = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "overlay_render_items",
		Help:    "Number of semantic overlay items in the rendered plan.",
		Buckets: []float64{1, 2, 3, 5, 8, 13, 21, 34, 55},
	})

	// ChrononRenderSeconds is the Chronon render phase the worker measured on
	// the certified artifact (render_ms). This is the GPU lane's own time — the
	// number to compare against a change in the Vulkan/NVENC path.
	ChrononRenderSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "chronon_render_seconds",
		Help:    "Chronon-measured render phase per certified overlay artifact, by backend.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"backend"})

	// ChrononEncodeSeconds is the worker-measured encode phase (encode_ms).
	// Reported separately from the render phase so an NVENC regression cannot
	// hide inside a "render got slower" observation.
	ChrononEncodeSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "chronon_encode_seconds",
		Help:    "Worker-measured encode phase per certified overlay artifact, by backend.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"backend"})

	// ChrononFramesRenderedTotal is the number of frames the GPU actually
	// produced. Paired with the render and encode seconds it yields the real
	// frames-per-second of the lane, which is the number the throughput
	// certification is written against.
	ChrononFramesRenderedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chronon_frames_rendered_total",
		Help: "Total frames produced by the GPU render lane, by backend.",
	}, []string{"backend"})
)
