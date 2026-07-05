// Package observability — delivery / Drive roots validator metrics (P1.4, July 2026).
//
// SRE surface for startup-time Drive root reachability validation
// (delivery.StartupDriveRootsValidator). Every metric is a
// promauto-registered collector against the default Prometheus
// registry, surfaced via /metrics (api/routes.go:274-284).
//
// Cardinality (bounded, no risk):
//   - destination: 9 DestinationKey values (YouTubeClip, Artlist,
//     Stock, Image, Voiceover, SoundEffect, Book,
//     Script, Document).
//   - outcome: 3 values (success, failure, skipped).
//   - Total series: 9 × 3 = 27 per labelled metric, plus 2
//     run-summary gauges (drive_roots_validator_last_run_*).
//
// Forward-pointer: if operators need transient vs terminal failure
// breakdown, the outcome label can be widened to {success,
// transient_failure, terminal_failure, skipped} (still bounded at
// 9 × 4 = 36 series). The widening is additive — existing
// {destination, outcome="failure"} queries continue to work.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// DriveRootsValidatorProbesTotal counts ProbeFolderAccess invocations
	// during startup Drive validation. Labels:
	//   - destination: canonical DestinationKey
	//   - outcome: "success" | "failure" | "skipped"
	//
	// "skipped" entries correspond to empty RootFolderIDs — operators
	// may intentionally leave sub-destinations unconfigured while
	// their capability stays off. Countering those separately from
	// "failure" lets dashboards distinguish "intentionally disabled"
	// from "configured but broken".
	DriveRootsValidatorProbesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "drive_roots_validator_probes_total",
		Help: "Total number of ProbeFolderAccess invocations during startup Drive validation, partitioned by destination and outcome.",
	}, []string{"destination", "outcome"})

	// DriveRootsValidatorProbeDuration histograms the per-probe wall
	// clock (including retry-with-jitter). Labels partitioned identically
	// to DriveRootsValidatorProbesTotal so dashboards can correlate rate
	// with latency per destination.
	//
	// Buckets cover network-drive probes (sub-100ms typical) up to
	// retry-saturated probes (~30s on a fully broken environment).
	DriveRootsValidatorProbeDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "drive_roots_validator_probe_duration_seconds",
		Help:    "Duration of each ProbeFolderAccess call during startup Drive validation, partitioned by destination and outcome.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"destination", "outcome"})

	// DriveRootsValidatorLastRunTimestamp records when the most recent
	// StartupDriveRootsValidator execution completed. Pairs with the
	// latched gauge below for staleness / recurrence alerting.
	DriveRootsValidatorLastRunTimestamp = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "drive_roots_validator_last_run_timestamp_seconds",
		Help: "Unix timestamp of the most recent StartupDriveRootsValidator execution.",
	})

	// DriveRootsValidatorLastRunSucceeded is the latched binary view
	// (1 = clean run with zero failures, 0 = at least one failure).
	// Composition roots re-run the validator only on hot reconfig;
	// staleness is exposed as the pair
	// (drive_roots_validator_last_run_timestamp_seconds == 0).
	DriveRootsValidatorLastRunSucceeded = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "drive_roots_validator_last_run_succeeded",
		Help: "1 if the most recent validator run succeeded (zero failures), 0 otherwise.",
	})
)
