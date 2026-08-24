// Package workerdoctor — config_adapter.go (RW-PROD-016, June 2026).
//
// AGENTS.md Pattern 0 / Port abstraction layer.
//
// DoctorConfig is the narrow structural port that the diagnostic
// probes depend on. It exposes ONLY the fields/methods that the
// probe consumer surface (default_probes.go + cmd/worker/doctor_main.go)
// actually reads. The interface is the future seam: when canonical
// config grows Server.TLS / Workers.MTLS structs, *config.Config will
// satisfy DoctorConfig directly and the runtime adapter becomes a
// pass-through.
//
// Until then, configDoctorAdapter bridges canonical Config to
// DoctorConfig. Read fields where they exist; return hardcoded
// zeros / false where they don't. ProbeCert and probeFilesystem
// can still call MTLSEnabled() etc.; the values are false / zero
// today, which is the failsafe default for cert probing — the
// probes opt out cleanly when MTLS is not wired.
//
// Forward-pointer (deadline 2026-09-01, owner: platform/config, tracked: RW-PROD-016): once canonical Config grows
//
//	type WorkersConfig struct { ... + MTLSConfig MTLSConfig `yaml:"mtls"` }
//	type ServerConfig struct { ... + TLSConfig TLSConfig `yaml:"tls"` }
//
// the adapter methods become direct delegations. At that point,
// also add `var _ DoctorConfig = (*config.Config)(nil)` as a
// compile-time invariant guarding the seam.
package workerdoctor

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// DoctorConfig is the structural port consumed by every probe in
// default_probes.go. Implementation lives in the runtime adapter
// below (and eventually in *config.Config itself).
type DoctorConfig interface {
	// Validate reports aggregated configuration validation errors.
	// nil config returns nil (nothing to validate).
	Validate() error

	// Server port (Extras only — used for log context).
	ServerPort() int

	// Future-seam mTLS surface (workers/identity side). Returns
	// false / zero today because canonical WorkersConfig has no
	// MTLS sub-struct; once it does, the adapter becomes a
	// direct delegation.
	MTLSEnabled() bool
	MinMTLSCertTTL() int // days; caller converts to time.Duration
	MTLSCertFile() string
	MTLSKeyFile() string
	MTLSCAFile() string
	MTLSServerName() string

	// Future-seam server-side TLS surface. Same caveat as above.
	ServerTLSEnabled() bool
	MinServerTLSCertTTL() int // days
	ServerTLSCertFile() string
	ServerTLSKeyFile() string
	ServerTLSClientCAFile() string

	// Storage path surface (matches StorageConfig from Config).
	DataDir() string
	PrimaryDBFullPath() string
	ObservabilityDBFullPath() string
	WorkspaceFullPath() string
	CacheFullPath() string
	ExportFullPath() string

	// Feature flags consumed by the engine-tools probe.
	YouTubeEnabled() bool
	ArtlistEnabled() bool
	ScriptClipsEnabled() bool

	// External config primitives consumed by master-reachable +
	// engine-binaries probes.
	ResolvedMasterURL() string
	FfmpegPath() string
}

// configDoctorAdapter wraps canonical config.Config and exposes
// its narrow surface through DoctorConfig. Construct via the
// NewDoctorConfig constructor so callers don't accidentally pass
// a bare *config.Config directly to probes (which would defeat
// the decoupling intent).
type configDoctorAdapter struct {
	cfg *config.Config
}

// Compile-time assertion: the runtime adapter satisfies the port.
// Once canonical Config grows Server.TLS / Workers.MTLS we also
// add `var _ DoctorConfig = (*config.Config)(nil)` to mark the
// seam as fixed. See the package doc for the forward-pointer timeline.
var _ DoctorConfig = (*configDoctorAdapter)(nil)

// NewDoctorConfig returns a DoctorConfig that reads through the
// canonical Config. Returns nil when cfg is nil; callers must
// thread through the standard `if cfg == nil` defensive pattern
// (probes already do — see probeConfig / probeFilesystem).
func NewDoctorConfig(cfg *config.Config) DoctorConfig {
	if cfg == nil {
		return nil
	}
	return &configDoctorAdapter{cfg: cfg}
}

// Validate: nil adapter or nil inner config is a no-op (returns
// nil error). This keeps the adapter safe in the nil-DoctorConfig
// fallback path used by NewFromConfigEmpty.
func (a *configDoctorAdapter) Validate() error {
	if a == nil || a.cfg == nil {
		return nil
	}
	return a.cfg.Validate()
}

func (a *configDoctorAdapter) ServerPort() int {
	if a == nil || a.cfg == nil {
		return 0
	}
	return a.cfg.Server.Port
}

// ── Direct delegations (canonical Config has these today) ──

func (a *configDoctorAdapter) DataDir() string {
	return a.cfg.Storage.DataDir
}
func (a *configDoctorAdapter) PrimaryDBFullPath() string {
	return a.cfg.Storage.PrimaryDBFullPath()
}
func (a *configDoctorAdapter) ObservabilityDBFullPath() string {
	return a.cfg.Storage.ObservabilityDBFullPath()
}
func (a *configDoctorAdapter) WorkspaceFullPath() string {
	return a.cfg.Storage.WorkspaceFullPath()
}
func (a *configDoctorAdapter) CacheFullPath() string {
	return a.cfg.Storage.CacheFullPath()
}
func (a *configDoctorAdapter) ExportFullPath() string {
	return a.cfg.Storage.ExportFullPath()
}

func (a *configDoctorAdapter) YouTubeEnabled() bool {
	return a.cfg.Features.YouTubeEnabled
}
func (a *configDoctorAdapter) ArtlistEnabled() bool {
	return a.cfg.Features.ArtlistEnabled
}
func (a *configDoctorAdapter) ScriptClipsEnabled() bool {
	return a.cfg.Features.ScriptClipsEnabled
}

func (a *configDoctorAdapter) ResolvedMasterURL() string {
	return a.cfg.External.ResolvedMasterURL()
}
func (a *configDoctorAdapter) FfmpegPath() string {
	return a.cfg.External.FfmpegPath
}

// ── Stub implementations (canonical Config lacks these) ──
// When canonical Config grows the missing TLS/MTLS sub-structs,
// these become direct delegations matching the field name
// conventions documented in the package README of platform/config.

func (a *configDoctorAdapter) MTLSEnabled() bool      { return false }
func (a *configDoctorAdapter) MinMTLSCertTTL() int    { return 0 }
func (a *configDoctorAdapter) MTLSCertFile() string   { return "" }
func (a *configDoctorAdapter) MTLSKeyFile() string    { return "" }
func (a *configDoctorAdapter) MTLSCAFile() string     { return "" }
func (a *configDoctorAdapter) MTLSServerName() string { return "" }

func (a *configDoctorAdapter) ServerTLSEnabled() bool        { return false }
func (a *configDoctorAdapter) MinServerTLSCertTTL() int      { return 0 }
func (a *configDoctorAdapter) ServerTLSCertFile() string     { return "" }
func (a *configDoctorAdapter) ServerTLSKeyFile() string      { return "" }
func (a *configDoctorAdapter) ServerTLSClientCAFile() string { return "" }
