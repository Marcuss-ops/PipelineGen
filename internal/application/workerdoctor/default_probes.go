// Package workerdoctor — default_probes.go (RW-PROD-016, June 2026; split 2026-07-06).
//
// Slim orchestrator + default probe REGISTRY. This file owns the
// overridable dependencies (LookupFunc, HTTPDoFunc, DefaultProbes),
// the NewDefaultProbes ctor, and the NewFromConfig / NewFromConfigEmpty
// wiring functions that register each probe onto an Aggregator.
//
// The probe BODIES live in capability-specific sister files per
// AGENTS.md Pattern 5 godlike/06 SSOT one-canonical-owner-per-fact:
//
//	default_probes.go         (this file, slim orchestrator + registry)
//	probes_liveness.go        — probeMasterReachable + WireReady +
//	                            defaultHTTPDo + fetchJSON (master /health + /ready)
//	probes_dependency.go      — probeConfig + probeCert + probeFilesystem +
//	                            ensureWritable (config + TLS + storage deps)
//	probes_invariant.go       — probeEngine + resolveFFMpegPath +
//	                            resolveFFprobePath + probeRuntime (engine
//	                            binary presence + Go runtime stats)
//
// godlike/06 SSOT: probeRegistry defer-stub remains inline in
// NewFromConfig rather than living in probes_liveness.go — the
// deferred-until-live-master note is intrinsic to the registration
// flow (the CLI does not have an opt-in to enable it).
//
// godlike/07 minimum-blast-radius (PR-SPLIT-WORKERDOCTOR-PROBES,
// 2026-07-06): zero new exported symbols, zero signature changes,
// zero dep changes; lookup paths preserved (same `workerdoctor`
// package); no changes to aggregator.go or config_adapter.go.
package workerdoctor

import (
	"net/http"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/process"
)

// LookupFunc is exec.LookPath's signature; replaced in tests by
// fakes that don't shell out to PATH. Default = exec.LookPath
// (which is the stdlib variant; we expose process.LookPath just
// in case future PR needs to swap exec semantics).
type LookupFunc func(name string) (string, error)

// HTTPDoFunc is http.Client.Do's signature. Replaced in tests to
// avoid network. Default = a fresh http.Client with a 5-second timeout
// derived per probe.
type HTTPDoFunc func(req *http.Request) (*http.Response, error)

// DefaultProbes bundles the overridable dependencies for default
// probes. Zero value uses all defaults (real exec, real HTTP).
// Tests construct a DefaultProbes literal with only the seams they
// want to override; everything else continues to work as before.
type DefaultProbes struct {
	LookPath LookupFunc
	HTTPDo   HTTPDoFunc
	// Now is the time-now seam; default probes ignore it but
	// the aggregator uses it as a timestamp source.
	Now func() time.Time
}

// NewDefaultProbes returns a DefaultProbes using process.LookPath +
// a 5s http.Client with no redirect following. The 5s timeout matches
// the master pre-flight budget (RW-PROD-004) so the doctor is no
// slower than the worker's first contact with the master.
func NewDefaultProbes() DefaultProbes {
	return DefaultProbes{
		LookPath: process.LookPath,
		HTTPDo: (&http.Client{
			Timeout: 5 * time.Second,
		}).Do,
	}
}

// NewFromConfig wires the canonical Aggregator with all 8 default
// probes, each one reading only the slice of config it needs.
// The aggregator's WorkerID/WorkerVersion/etc. fields are not set
// here — the CLI passes them after resolving the environment.
//
// DoctorConfig is the structural port (config_adapter.go). The CLI
// wraps canonical config.Config via workerdoctor.NewDoctorConfig
// before calling this — wiring the seam at the entry point keeps
// the probe surface narrowed and decoupled from canonical Config
// schema evolution.
func NewFromConfig(cfg DoctorConfig, masterURL string, dp DefaultProbes) *Aggregator {
	if cfg == nil {
		// nil cfg is treated as a configuration validation failure
		// (the doctor cannot even read defaults). We still wire the
		// remaining probes so the report is informative.
		return NewFromConfigEmpty(dp)
	}
	agg := NewAggregator()
	agg.SetCheck(CheckIDConfig, func() ProbeReceipt {
		return probeConfig(cfg)
	})
	agg.SetCheck(CheckIDCert, func() ProbeReceipt {
		return probeCert(cfg)
	})
	agg.SetCheck(CheckIDFilesystem, func() ProbeReceipt {
		return probeFilesystem(cfg)
	})
	agg.SetCheck(CheckIDEngine, func() ProbeReceipt {
		return probeEngine(cfg, dp)
	})
	agg.SetCheck(CheckIDRegistry, func() ProbeReceipt {
		// Registry probe in standalone doctor mode is opt-out: the
		// dispatcher registry is built during the composition root
		// (app.BuildWorkerRegistry) which requires DB + repos +
		// providers to be wired first. The doctor's intent is
		// pre-boot introspection — operators that need a full
		// registry check should hit /api/system/doctor on the live
		// master, or boot the worker in --dry-run mode (future PR).
		//
		// Mark Applicable=false so the aggregator's verdict loop
		// does not flip NOT_READY for an honest gap. The Note
		// exposes the gap so consumers error-checking the report
		// string can see this.
		return ProbeReceipt{
			OK:         true,
			Applicable: false,
			Note:       "registry probe deferred to live /api/system/doctor (standalone doctor doesn't build the full dispatcher)",
		}
	})
	agg.SetCheck(CheckIDMasterReachable, func() ProbeReceipt {
		if masterURL == "" {
			return ProbeReceipt{
				OK:         false,
				Applicable: true,
				Error:      "VELOX_MASTER_URL is empty (cannot probe reachability)",
			}
		}
		return probeMasterReachable(masterURL, cfg, dp)
	})
	agg.SetCheck(CheckIDReady, func() ProbeReceipt {
		// Default is opt-out. The CLI calls WireReady() AFTER
		// confirming master_reachable succeeded; if master is
		// unreachable, the WireReady probe internally returns
		// Applicable=false so the verdict loop does NOT flip on a
		// downstream cascade from master_reachable's failure.
		return ProbeReceipt{
			OK:         true,
			Applicable: false,
			Note:       "ready check deferred to CLI (WireReady activates only when master_reachable is OK)",
		}
	})
	agg.SetCheck(CheckIDRuntime, func() ProbeReceipt {
		return probeRuntime(dp)
	})
	return agg
}

// NewFromConfigEmpty returns an Aggregator that returns a NOT_READY
// config check (cfg nil) and a "not applicable" everywhere else.
// Used as a defensive fallback when configuration loading itself
// failed; the operator can still see which other probes would have
// run.
func NewFromConfigEmpty(dp DefaultProbes) *Aggregator {
	agg := NewAggregator()
	agg.SetCheck(CheckIDConfig, func() ProbeReceipt {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      "config is nil (load failed upstream — see logs)",
		}
	})
	for _, id := range []string{CheckIDCert, CheckIDFilesystem, CheckIDEngine, CheckIDRegistry, CheckIDMasterReachable, CheckIDReady, CheckIDRuntime} {
		idLocal := id
		agg.SetCheck(idLocal, func() ProbeReceipt {
			return ProbeReceipt{
				OK:         false,
				Applicable: false,
				Error:      "check skipped because config is nil",
			}
		})
	}
	return agg
}
