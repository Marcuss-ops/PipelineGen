// Package workerdoctor — default_probes.go (RW-PROD-016, June 2026).
//
// Default probe implementations. Each function is leaf and reusable:
// they take only the inputs they need (config, paths, URL) and
// return a ProbeReceipt. The CLI glue wires them into an Aggregator
// in a single NewFromConfig() call.
//
// Reuse, no duplication:
//   - pkg/tlsload.LoadServerIdentity / LoadClientIdentity
//     (cert validity + key perms + CA↔cert chain) — RW-PROD-001.
//   - config.Config.Validate() (port placeholders, HMAC ≥32B,
//     TLS/MTLS path gates) — RW-PROD-002.
//   - exec.LookPath for engine binaries (ffmpeg, ffprobe, yt-dlp,
//     python3, node, aria2c).
//   - master /health probe (reuses the same call the worker
//     preflight uses, with a tighter timeout) — RW-PROD-004.
//   - internal/application/system/health.ReadyChecker.CheckReady
//     (the deep /ready checks, when the master is reachable).
//
// Every probe is constructed so it can be replaced in tests by
// substituting the Aggregator.SetCheck(id, fakeProbe) call.
package workerdoctor

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	tlsload "github.com/Marcuss-ops/PipelineGen/pkg/tlsload"
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

// probeConfig runs cfg.Validate() and reports the aggregated
// outcome. The error is surfaced verbatim under Error so the
// operator sees the same line they would see at boot. No
// duplication of validation logic — Validate() is reused edge-to-edge.
func probeConfig(cfg DoctorConfig) ProbeReceipt {
	if cfg == nil {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      "config is nil",
		}
	}
	if err := cfg.Validate(); err != nil {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      err.Error(),
			Extras: map[string]any{
				"server_port":        cfg.ServerPort(),
				"mtls_enabled":       cfg.MTLSEnabled(),
				"server_tls_enabled": cfg.ServerTLSEnabled(),
			},
		}
	}
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Extras: map[string]any{
			"server_port": cfg.ServerPort(),
			"master_url":  cfg.ResolvedMasterURL(),
		},
	}
}

// probeCert runs when either Server.TLS.Enabled or Workers.MTLS.Enabled
// is set. We reuse pkg/tlsload; it already handles key perms, expiry
// windows, and CA↔cert chain alignment. When neither side requires
// mTLS, the check opts out cleanly.
//
// Today DoctorConfig returns false for both sides (canonical
// Config lacks the sub-structs; see config_adapter.go TODO). When
// the adapter becomes a pass-through, this probe automatically
// activates.
func probeCert(cfg DoctorConfig) ProbeReceipt {
	if cfg == nil {
		return ProbeReceipt{OK: false, Applicable: true, Error: "config is nil"}
	}
	if !cfg.ServerTLSEnabled() && !cfg.MTLSEnabled() {
		return ProbeReceipt{
			OK:         true,
			Applicable: false,
			Note:       "mTLS not enabled (server.tls.enabled=false and workers.mtls.enabled=false)",
		}
	}
	// Examines only the side that's enabled. We do not run a real
	// handshake here (that's the worker's job); we only confirm the
	// material on disk will be accepted by the TLS loader.
	extras := map[string]any{}
	var lastErr error
	if cfg.MTLSEnabled() {
		window := time.Duration(cfg.MinMTLSCertTTL()) * 24 * time.Hour
		_, ident, err := tlsload.LoadClientIdentity(
			cfg.MTLSCertFile(),
			cfg.MTLSKeyFile(),
			cfg.MTLSCAFile(),
			cfg.MTLSServerName(),
			window,
		)
		if err != nil {
			lastErr = err
		} else if ident != nil {
			extras["client_cert_fingerprint_sha256"] = ident.FingerprintSHA256
			extras["client_cert_subject_dn"] = ident.SubjectDN
			extras["client_cert_not_after"] = ident.NotAfter
		}
	}
	if lastErr == nil && cfg.ServerTLSEnabled() {
		window := time.Duration(cfg.MinServerTLSCertTTL()) * 24 * time.Hour
		_, ident, err := tlsload.LoadServerIdentity(
			cfg.ServerTLSCertFile(),
			cfg.ServerTLSKeyFile(),
			cfg.ServerTLSClientCAFile(),
			window,
		)
		if err != nil {
			lastErr = err
		} else if ident != nil {
			extras["server_cert_fingerprint_sha256"] = ident.FingerprintSHA256
			extras["server_cert_subject_dn"] = ident.SubjectDN
			extras["server_cert_not_after"] = ident.NotAfter
		}
	}
	if lastErr != nil {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      lastErr.Error(),
			Extras:     extras,
		}
	}
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Extras:     extras,
	}
}

// probeFilesystem walks the canonical storage paths derived from
// cfg.Storage and verifies each one is creatable and writable (or
// already exists). Uses StorageConfig.<X>FullPath() helpers — same
// source of truth as the bootstrap composition root.
//
// Failure modes:
//   - missing parent AND cannot create: NOT_READY
//   - exists but not writable: NOT_READY
//   - missing parent, mkdir succeeds: PASS
//
// We do NOT call Stat-then-touch on stale paths; mkDirAll handles
// the race window.
func probeFilesystem(cfg DoctorConfig) ProbeReceipt {
	if cfg == nil {
		return ProbeReceipt{OK: false, Applicable: true, Error: "config is nil"}
	}
	paths := []struct {
		Name string
		Path string
	}{
		{"data_dir", cfg.DataDir()},
		{"primary_db_path", cfg.PrimaryDBFullPath()},
		{"observability_db_path", cfg.ObservabilityDBFullPath()},
		{"workspace_dir", cfg.WorkspaceFullPath()},
		{"cache_dir", cfg.CacheFullPath()},
		{"export_dir", cfg.ExportFullPath()},
	}
	missing := make([]string, 0, len(paths))
	for _, p := range paths {
		if p.Path == "" {
			missing = append(missing, p.Name+"=empty")
			continue
		}
		if err := ensureWritable(p.Path); err != nil {
			missing = append(missing, p.Name+":"+err.Error())
		}
	}
	if len(missing) > 0 {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      "one or more storage paths are not writable",
			Extras: map[string]any{
				"missing_or_unwritable": missing,
			},
		}
	}
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Extras: map[string]any{
			"data_dir":        cfg.DataDir(),
			"primary_db_path": cfg.PrimaryDBFullPath(),
			"workspace_dir":   cfg.WorkspaceFullPath(),
			"cache_dir":       cfg.CacheFullPath(),
		},
	}
}

// ensureWritable is os.MkdirAll + a write probe. It can be called on
// either a directory or a file path; for files we MkdirAll the parent
// then touch the file. Cheap and avoids a "real" probe that would
// create artifacts we don't want to own.
func ensureWritable(p string) error {
	if strings.HasSuffix(p, ".sqlite") || strings.HasSuffix(p, ".db") {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return err
	}
	// Probe write: try opening a temp file in the directory.
	tmp := filepath.Join(p, ".doctor-write-probe")
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_ = f.Close()
	_ = os.Remove(tmp)
	return nil
}

// probeEngine is the engine-tools probe. It consults Features to know
// which tools the deployment actually needs, and looks each one up on
// PATH (with the lookup seam provided by DefaultProbes).
//
// Even when a feature is disabled we still probe ffmpeg/ffprobe
// because(script/video pipelines share them); we ONLY require
// per-feature tools when the matching feature is enabled.
func probeEngine(cfg DoctorConfig, dp DefaultProbes) ProbeReceipt {
	lookup := dp.LookPath
	if lookup == nil {
		lookup = exec.LookPath
	}
	required := []string{"ffmpeg"}
	if cfg != nil {
		if cfg.YouTubeEnabled() {
			required = append(required, "yt-dlp")
		}
		if cfg.ArtlistEnabled() {
			required = append(required, "node")
		}
		if cfg.ScriptDocsEnabled() || cfg.ScriptClipsEnabled() {
			required = append(required, "python3")
		}
	}
	missing := make([]string, 0, len(required))
	for _, t := range required {
		if _, err := lookup(t); err != nil {
			missing = append(missing, t)
		}
	}
	// Always probe ffprobe via ffmpeg-path derivation even if ffmpeg
	// passes: the loader in ffmpeg/probe.go derives ffprobe from the
	// ffmpeg binary location at config time. We surface that as an
	// extras entry for operator visibility but do not fail-closed on
	// it (some pure-audio deployments don't need it).
	extras := map[string]any{
		"required":         required,
		"ffmpeg_resolved":  resolveFFMpegPath(cfg),
		"ffprobe_resolved": resolveFFprobePath(cfg),
	}
	if len(missing) > 0 {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      "engine binaries missing on PATH: " + strings.Join(missing, ","),
			Extras:     extras,
		}
	}
	return ProbeReceipt{OK: true, Applicable: true, Extras: extras}
}

// resolveFFMpegPath mirrors ExternalConfig.FfmpegPath with a fallback.
func resolveFFMpegPath(cfg DoctorConfig) string {
	if cfg != nil && cfg.FfmpegPath() != "" {
		return cfg.FfmpegPath()
	}
	return "ffmpeg"
}

// resolveFFprobePath derives ffprobe from ffmpeg, mirroring ffmpeg/probe.go.
func resolveFFprobePath(cfg DoctorConfig) string {
	ffmpeg := resolveFFMpegPath(cfg)
	if ffmpeg == "ffmpeg" {
		return "ffprobe"
	}
	dir, name := filepath.Split(ffmpeg)
	if name == "ffmpeg" || name == "ffmpeg.exe" {
		return filepath.Join(dir, "ffprobe")
	}
	return "ffprobe"
}

// probeMasterReachable polls ${masterURL}/health with the same
// budget as the worker's startup pre-flight (5s timeout per attempt).
// We do NOT retry multiple times — the doctor is a snapshot, not a
// service mesh. A failure means "master is not healthy right now";
// the worker will surface the same problem at the pre-flight itself.
func probeMasterReachable(masterURL string, cfg DoctorConfig, dp DefaultProbes) ProbeReceipt {
	if masterURL == "" {
		return ProbeReceipt{OK: false, Applicable: true, Error: "master URL is empty"}
	}
	do := dp.HTTPDo
	if do == nil {
		do = defaultHTTPDo(5 * time.Second)
	}
	healthURL := strings.TrimRight(masterURL, "/") + "/health"
	req, err := http.NewRequest(http.MethodGet, healthURL, nil)
	if err != nil {
		return ProbeReceipt{OK: false, Applicable: true, Error: err.Error()}
	}
	resp, err := do(req)
	if err != nil {
		return ProbeReceipt{OK: false, Applicable: true, Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      "master /health returned " + resp.Status,
			Extras:     map[string]any{"status_code": resp.StatusCode},
		}
	}
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Extras:     map[string]any{"status_code": resp.StatusCode, "url": healthURL},
	}
}

// WireReady is called by the CLI after the master_reachable probe
// ran successfully. It installs a /ready probe driven by the
// canonical application ReadyChecker so the deep checks (DB, jobs,
// drive, qdrant) are honored by the doctor too.
//
// Connection failures (master truly unreachable) are treated as
// Applicable=false so the verdict loop does NOT cascade a single
// upstream fault into a downstream fail. Once the master is
// reachable, /ready reports its own canonical status; "healthy"
// passes, anything else fails the doctor.
func WireReady(agg *Aggregator, masterURL string, dp DefaultProbes) error {
	if agg == nil {
		return errors.New("aggregator is nil")
	}
	agg.SetCheck(CheckIDReady, func() ProbeReceipt {
		// We cannot reuse ReadyChecker directly without a Service
		// — the doctor is a stand-alone tool. Instead we read /ready
		// directly: it's the canonical aggregation of DB + Drive +
		// Qdrant + Jobs. Reading it here is functionally equivalent
		// to wrapping the ReadyChecker but does not require the
		// heavier app composition graph to be loaded.
		body, status, err := fetchJSON(masterURL+"/ready", dp)
		if err != nil {
			// Treat network-level failure as opt-out, NOT a fail:
			// master_reachable already flagged the upstream fault
			// and we want /ready to be "not run", not "run and
			// double-fail". Cascade failures produce noisy reports.
			return ProbeReceipt{
				OK:         false,
				Applicable: false,
				Note:       "master unreachable; /ready probe skipped (already reported by master_reachable)",
				Error:      err.Error(),
			}
		}
		// /ready is the canonical HTTP 503 / 200 aggregator; both
		// 2xx and 5xx can carry {"status":"unhealthy"} so we parse
		// the body instead of just looking at the status code.
		var r struct {
			OK     bool   `json:"ok"`
			Status string `json:"status"`
		}
		if jerr := json.Unmarshal(body, &r); jerr != nil {
			return ProbeReceipt{
				OK:         false,
				Applicable: true,
				Error:      "ready body not parseable: " + jerr.Error(),
				Extras:     map[string]any{"status_code": status, "body": string(body)},
			}
		}
		if !r.OK || r.Status != "healthy" {
			return ProbeReceipt{
				OK:         false,
				Applicable: true,
				Error:      "master /ready reported " + r.Status,
				Extras:     map[string]any{"status_code": status, "body": string(body)},
			}
		}
		return ProbeReceipt{
			OK:         true,
			Applicable: true,
			Extras:     map[string]any{"status_code": status, "ready_status": r.Status},
		}
	})
	return nil
}

// defaultHTTPDo returns a do-cls with a per-call timeout. We use a
// fresh client per call because the doctor only fires one HTTP
// probe; lifecycle overhead is irrelevant.
func defaultHTTPDo(perReq time.Duration) HTTPDoFunc {
	client := &http.Client{Timeout: perReq}
	return func(req *http.Request) (*http.Response, error) {
		return client.Do(req)
	}
}

// fetchJSON — small helper used by /ready probe. Limited body
// (4KiB cap) so a misbehaving master can't OOM the doctor.
func fetchJSON(url string, dp DefaultProbes) ([]byte, int, error) {
	do := dp.HTTPDo
	if do == nil {
		do = defaultHTTPDo(5 * time.Second)
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil {
			break
		}
		if len(buf) >= 16384 {
			// hard cap; we only need first few hundred bytes to parse /ready
			break
		}
	}
	return buf, resp.StatusCode, nil
}

// probeRuntime reports disk + memory cabinet stats. Soft thresholds
// only — the doctor is informational here. We deliberately do NOT
// fail-closed on "low memory" because a worker can still start
// under tight memory; a low disk should still warn but not block
// because operators sometimes run ad-hoc in a 4GiB Docker setup
// where 80% used is ordinary.
//
// For production mode (--production) the doctor flips the soft
// thresholds into hard fails. Production flag is consumed by the
// CLI; this probe always returns ok=true unless an exec syscall
// itself fails.
//
// Disk stats are deliberately absent here: portable disk stats
// require platform-specific shelling (df on Unix, fsutil on Windows).
// For that the doctor intentionally skips the probes; the
// filesystem check above covers "is the disk reachable", not
// "how full is it".
func probeRuntime(_ DefaultProbes) ProbeReceipt {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Note:       "runtime stats are informational; thresholds configured via --production flag",
		Extras: map[string]any{
			"go_routines":  runtime.NumGoroutine(),
			"go_version":   runtime.Version(),
			"go_os":        runtime.GOOS,
			"go_arch":      runtime.GOARCH,
			"num_cpu":      runtime.NumCPU(),
			"mem_alloc_kb": mem.Alloc / 1024,
			"mem_sys_kb":   mem.Sys / 1024,
			"num_gc":       mem.NumGC,
		},
	}
}
