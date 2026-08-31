// Package observability — voiceover metrics (P0 #4 commit B/2, July 2026).
//
// Voiceover-related Prometheus counters/gauges/histograms live here
// per the pkg observability "split-by-feature" pattern documented in
// metrics_registry.go: each domain (jobs, qdrant, scripts, media)
// owns its own metrics_*.go file. Voiceover's metrics_voiceover.go
// declares the orphan-sweeper counters plus the future humanizer
// (alias for legibility on the application side).
//
// Backing metrics for the orphan sweeper (consumed in
// internal/capabilities/voiceover/orphan_sweeper.go):
//   - orphan_sweeper_runs_total (counter, no labels)
//   - orphan_sweeper_reconciled_total (counter, label: outcome)
//
// Production wiring: the orphan sweeper accepts a *Metrics struct
// (see voiceover.Metrics) constructed from these package-level
// vars. Tests construct local prometheus.NewCounter (NOT promauto)
// to avoid polluting the default registry.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// OrphanSweeperRunsTotal is incremented ONCE per OrphanSweeper.Run
	// invocation (per process boot, NOT per sweep tick). "ad ogni
	// start" of the goroutine per user-spec for the orphan sweeper.
	OrphanSweeperRunsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "orphan_sweeper_runs_total",
		Help: "Total number of OrphanSweeper.Run() goroutine starts (once per process boot, NOT per sweep tick).",
	})

	// OrphanSweeperReconciledTotal is incremented per stale-row
	// compensation. Labels:
	//   outcome="uploaded_cleanup" — Drive.Trash + MarkFailed on
	//                                a stale 'uploaded' row (Drive-side
	//                                orphan recovered).
	//   outcome="pending_timeout"  — MarkFailed (NO Drive action)
	//                                on a stale 'pending' row (the
	//                                Drive file was never created).
	OrphanSweeperReconciledTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orphan_sweeper_reconciled_total",
		Help: "Total number of stale upload_intents rows reconciled by the orphan sweeper, partitioned by outcome.",
	}, []string{"outcome"})

	// ── FASE 5 — VO-OPERATIONAL-READINESS pipeline metrics (July 2026) ──

	// VoiceoverJobsTotal is incremented once per voiceover job completion
	// (ProcessSegmentUseCase.Execute exit). Labels:
	//   status="completed" — Execute returned StatusCompleted
	//   status="failed"    — Execute returned StatusFailed (any stage)
	VoiceoverJobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "voiceover_jobs_total",
		Help: "Total number of voiceover pipeline executions, partitioned by final status.",
	}, []string{"status"})

	// DriveUploadFailuresTotal counts Stage 3 (Publisher.Publish) failures.
	// godlike/07 NO-FAKE-AVAILABILITY: incremented ONLY when the publisher
	// returns a non-nil error — the counter is the canonical metric for
	// orphan-Drive-file risk (upload succeeded but the DB tx hasn't started
	// yet, so a cleanup event is the only recovery path).
	DriveUploadFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "drive_upload_failures_total",
		Help: "Total number of Drive upload (VoiceoverPublisher.Publish) failures in the voiceover pipeline.",
	})

	// TTSFailuresTotal counts Stage 1 (TTSProvider.Synthesize) failures.
	TTSFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "tts_failures_total",
		Help: "Total number of TTS synthesis (TTSProvider.Synthesize) failures in the voiceover pipeline.",
	})

	// TranslationFailuresTotal counts voiceover translation failures
	// (translator closure in the promo generation path). This metric is
	// incremented from the translator closure wired in
	// build_bundles_voiceover.go when the translation port returns an error.
	TranslationFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "translation_failures_total",
		Help: "Total number of voiceover text translation failures in the promo generation path.",
	})
)
