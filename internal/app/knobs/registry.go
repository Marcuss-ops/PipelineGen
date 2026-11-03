// Package knobs is the ONE registry of the VELOX_* runtime knobs the
// composition root (internal/app/wiring) reads, declares or prints.
//
// Before this table the same knob was named in a runbook, in an example
// config, in an os.Getenv call and in a failure message, and nothing proved
// the four agreed: 33 names appeared across the wiring package, 12 were read
// directly, 15 were described in runbooks, and a knob could be added to the
// code without ever being documented (or documented without existing).
//
// The registry is that single source:
//
//   - the composition root reads env knobs through String/Bool instead of
//     scattering os.Getenv calls;
//   - Startup logging (LogNonDefault) reports every knob whose effective value
//     differs from its declared default, so a production host states its
//     overrides at boot instead of leaving them to be guessed;
//   - the example config (config/velox-knobs.generated.yaml) and the runbook
//     (docs/operations/VELOX-KNOBS.md) are GENERATED from this table by
//     cmd/velox-knobs, and a test fails when either drifts;
//   - TestEveryWiringKnobIsRegistered fails when a VELOX_* name appears in the
//     wiring package without a registry entry.
//
// A Kind of marker/test-only is part of the table on purpose: those tokens
// look like knobs (VELOX_ASSEMBLY_READY_V1 is an assembly contract id,
// VELOX_ENQUEUE an execution step) and an undocumented look-alike is exactly
// what made the inventory untrustworthy. They are excluded from the generated
// config and from the non-default boot log.
package knobs

import (
	"os"
	"sort"
	"strings"

	"go.uber.org/zap"
)

// Kind classifies how a name is consumed.
type Kind string

const (
	// KindEnv is read from the process environment by Go code (directly by the
	// composition root, or by the owning capability).
	KindEnv Kind = "env"
	// KindConfig is bound through a config struct `env:` tag; the composition
	// root reads the parsed Config field, and the env var is the override.
	KindConfig Kind = "config"
	// KindTestOnly is a test harness variable, never production configuration.
	KindTestOnly Kind = "test-only"
	// KindMarker is a contract/step identifier or a retired knob that is never
	// read as configuration. Kept so the inventory is complete.
	KindMarker Kind = "marker"
)

// Knob is one row of the registry.
type Knob struct {
	// Name is the exact environment variable name.
	Name string
	// Kind says how the value reaches the code.
	Kind Kind
	// Default is the effective value when the env var is unset.
	Default string
	// Owner is the package/file that consumes the knob.
	Owner string
	// SafeProduction is the value (or range) a production host should run.
	SafeProduction string
	// Description is one sentence: what the knob changes.
	Description string
}

// registry is the table. Keep it sorted by Name: the generated artifacts and
// the boot log inherit this order, and a stable diff is part of reviewability.
var registry = []Knob{
	{
		Name: "VELOX_ASSEMBLY_READY_V1", Kind: KindMarker, Default: "",
		Owner:          "internal/kernel/media/assembly_contract.go",
		SafeProduction: "contract id — not an env var",
		Description:    "Assembly contract identifier (one audio stream, closed GOP), printed by media handoff logs; never read as configuration.",
	},
	{
		Name: "VELOX_CONCURRENT_OLLAMA_CALLS", Kind: KindConfig, Default: "4",
		Owner:          "internal/platform/config/types.go",
		SafeProduction: "4",
		Description:    "Parallel Ollama calls allowed per host.",
	},
	{
		Name: "VELOX_CONCURRENT_VIDEO_EXTRACTS", Kind: KindConfig, Default: "5",
		Owner:          "internal/platform/config/types.go",
		SafeProduction: "5",
		Description:    "Parallel stock-video extraction jobs.",
	},
	{
		Name: "VELOX_CREDENTIALS_FILE", Kind: KindConfig, Default: "credentials.json",
		Owner:          "internal/platform/config/types_misc.go",
		SafeProduction: "/etc/pipelinegen/credentials.json",
		Description:    "OAuth client credentials file used by the Drive publisher.",
	},
	{
		Name: "VELOX_DRIVE_STRICT_STARTUP_VALIDATION", Kind: KindConfig, Default: "true",
		Owner:          "internal/app/wiring/build_bundles_drive_gates.go",
		SafeProduction: "true",
		Description:    "Fail boot when Drive credentials/roots are invalid; false is the soft-mode escape hatch.",
	},
	{
		Name: "VELOX_EDITORIAL_BACKGROUNDS_DIR", Kind: KindEnv, Default: "",
		Owner:          "internal/app/wiring/media/editorial_assets_bootstrap.go",
		SafeProduction: "unset (plates are fetched and verified from Drive)",
		Description:    "Local directory of certified editorial plate fixtures; an optimization, not the authority.",
	},
	{
		Name: "VELOX_ENQUEUE", Kind: KindMarker, Default: "",
		Owner:          "internal/capabilities/scripts/performance/resolver.go",
		SafeProduction: "step name — not an env var",
		Description:    "Execution step name used by the render-enqueue performance phase; never read as configuration.",
	},
	{
		Name: "VELOX_FAKE_FINAL_JOB", Kind: KindEnv, Default: "",
		Owner:          "internal/app/wiring/fake_final_job.go",
		SafeProduction: "unset",
		Description:    "Simulate the remote final-job handoff so a run measures only the local pipeline.",
	},
	{
		Name: "VELOX_FEATURE_CLIP_INDEXER_ENABLED", Kind: KindMarker, Default: "",
		Owner:          "internal/app/wiring/build_bundles_qdrant_gates.go",
		SafeProduction: "config clip_indexer.enabled (the env name is printed, not read)",
		Description:    "Escape-hatch name quoted by the Qdrant/ClipIndexer boot gate; the effective toggle is config clip_indexer.enabled.",
	},
	{
		Name: "VELOX_FEATURE_QDRANT_ENABLED", Kind: KindMarker, Default: "",
		Owner:          "internal/app/wiring/build_bundles_qdrant_gates.go",
		SafeProduction: "config qdrant.enabled (the env name is printed, not read)",
		Description:    "Escape-hatch name quoted by the Qdrant/ClipIndexer boot gate; the effective toggle is config qdrant.enabled.",
	},
	{
		Name: "VELOX_FINAL_AUDIO_CACHE_DIR", Kind: KindEnv, Default: "",
		Owner:          "internal/app/wiring/script_generation_runtime.go",
		SafeProduction: "unset (default cache root)",
		Description:    "Relocates the per-run final-audio cache.",
	},
	{
		Name: "VELOX_FINAL_JOB_ATTACH_SECONDS", Kind: KindEnv, Default: "3000",
		Owner:          "internal/app/wiring/remote_final_job.go",
		SafeProduction: "3000",
		Description:    "Maximum seconds a script.generate attempt stays attached to the remote render (0 disables the hand-off wait).",
	},
	{
		Name: "VELOX_FINAL_JOB_COPY_CERTIFIED_WINDOWS", Kind: KindEnv, Default: "1",
		Owner:          "internal/app/wiring/final_job_segment_prepare.go",
		SafeProduction: "1",
		Description:    "Prefer packet copy for certified windows; 0 routes windows through the verified encoder lane.",
	},
	{
		Name: "VELOX_FINAL_JOB_COPY_ONLY", Kind: KindMarker, Default: "",
		Owner:          "internal/app/wiring/final_job_segment_prepare.go",
		SafeProduction: "retired — packet copy is preferred and verified encoding remains available",
		Description:    "Retired compatibility marker; local preparation may use verified encoding when a source window is not copy-compatible.",
	},
	{
		Name: "VELOX_FINAL_JOB_CREDENTIALS_FILE", Kind: KindEnv, Default: "",
		Owner:          "internal/app/wiring/remote_final_job.go",
		SafeProduction: "/etc/pipelinegen/final_job_credentials.json",
		Description:    "Credentials file carrying the final-job bearer token when VELOX_FINAL_JOB_M2M_SECRET is unset.",
	},
	{
		Name: "VELOX_FINAL_JOB_FRAGMENT_CACHE_DIR", Kind: KindEnv, Default: "",
		Owner:          "internal/app/wiring/final_job_fragment_io.go",
		SafeProduction: "unset (default cache root)",
		Description:    "Relocates the prepared-fragment cache used by the final-job handoff.",
	},
	{
		Name: "VELOX_FINAL_JOB_FRAGMENT_CODEC", Kind: KindTestOnly, Default: "",
		Owner:          "internal/app/wiring/final_job_segment_prepare_test.go",
		SafeProduction: "unset",
		Description:    "Test harness selector for the fragment codec path; not read by production code.",
	},
	{
		Name: "VELOX_FINAL_JOB_M2M_SECRET", Kind: KindEnv, Default: "",
		Owner:          "internal/app/wiring/remote_final_job.go",
		SafeProduction: "64-hex secret from the host env file",
		Description:    "Per-client bearer secret for job.submit against the Master.",
	},
	{
		Name: "VELOX_FINAL_JOB_MASTER_URL", Kind: KindEnv, Default: "",
		Owner:          "internal/app/wiring/remote_final_job.go",
		SafeProduction: "https://<master-host>",
		Description:    "Master URL for the remote final-job handoff; unset falls back to the configured base URL.",
	},
	{
		Name: "VELOX_FINAL_JOB_PREPARE_LANES", Kind: KindEnv, Default: "4",
		Owner:          "internal/app/wiring/final_job_segment_prepare.go",
		SafeProduction: "4 (1..16)",
		Description:    "Fragments prepared in parallel per handoff.",
	},
	{
		Name: "VELOX_FINAL_JOB_QUEUE_SECONDS", Kind: KindEnv, Default: "180",
		Owner:          "internal/app/wiring/remote_final_job.go",
		SafeProduction: "180",
		Description:    "How long a handoff may sit NOT-STARTED before it is handed back (0 disables the budget).",
	},
	{
		Name: "VELOX_FINAL_JOB_RESOLVE_CONCURRENCY", Kind: KindEnv, Default: "1",
		Owner:          "internal/app/wiring/remote_final_job_resolve.go",
		SafeProduction: "4 (1..16)",
		Description:    "Drive sources resolved in parallel while preparing a handoff.",
	},
	{
		Name: "VELOX_FINAL_JOB_STOCK_CACHE_DIR", Kind: KindEnv, Default: "",
		Owner:          "internal/app/wiring/remote_final_job_resolve.go",
		SafeProduction: "unset (default cache root)",
		Description:    "Relocates the resolved stock-asset cache used by the handoff.",
	},
	{
		Name: "VELOX_FINAL_JOB_WORKER_ID", Kind: KindEnv, Default: "",
		Owner:          "internal/app/wiring/remote_final_job.go",
		SafeProduction: "unset (Master places the job)",
		Description:    "Pins the handoff to one worker; the retired worker-51 id is rejected loudly.",
	},
	{
		Name: "VELOX_MEDIA_MANIFEST_PATH", Kind: KindEnv, Default: "",
		Owner:          "internal/capabilities/scripts/adapters/media_manifest_shim.go",
		SafeProduction: "unset",
		Description:    "Opt-in media_manifest fast-path used by the composition root.",
	},
	{
		Name: "VELOX_OVERLAY_VIDEO_ONLY_V1", Kind: KindMarker, Default: "",
		Owner:          "internal/kernel/media/assembly_contract.go",
		SafeProduction: "contract id — not an env var",
		Description:    "Legacy overlay video-only assembly contract identifier; never read as configuration.",
	},
	{
		Name: "VELOX_POLL_MAX_BACKOFF", Kind: KindConfig, Default: "60s",
		Owner:          "internal/platform/config/jobs.go",
		SafeProduction: "60s",
		Description:    "Ceiling of the job-runner poll backoff.",
	},
	{
		Name: "VELOX_PROGRESS_COALESCE_WINDOW", Kind: KindConfig, Default: "100ms",
		Owner:          "internal/platform/config/jobs.go",
		SafeProduction: "100ms",
		Description:    "Window over which job progress writes are coalesced (0 = passthrough).",
	},
	{
		Name: "VELOX_RUST_SEMANTIC_ENGINE_PATH", Kind: KindTestOnly, Default: "",
		Owner:          "internal/app/wiring/semantic_blend_live_test.go",
		SafeProduction: "unset",
		Description:    "Rust semantic-engine binary used by the opt-in live blend test.",
	},
	{
		Name: "VELOX_SCHEDULER_INTERVAL", Kind: KindConfig, Default: "30s",
		Owner:          "internal/platform/config/jobs.go",
		SafeProduction: "30s",
		Description:    "Scheduler tick interval for background jobs.",
	},
	{
		Name: "VELOX_SCRIPTS_LOCALIZED_RENDER_GLOBAL_CONCURRENCY", Kind: KindConfig, Default: "6",
		Owner:          "internal/platform/config/scripts.go",
		SafeProduction: "6 (machine render ceiling)",
		Description:    "Machine-wide ceiling for localized render jobs.",
	},
	{
		Name: "VELOX_SEMANTIC_BLEND_FIXTURE", Kind: KindTestOnly, Default: "",
		Owner:          "internal/app/wiring/semantic_blend_live_test.go",
		SafeProduction: "unset",
		Description:    "Fixture payload for the opt-in live semantic blend test.",
	},
	{
		Name: "VELOX_SEMANTIC_BLEND_LIVE", Kind: KindTestOnly, Default: "",
		Owner:          "internal/app/wiring/semantic_blend_live_test.go",
		SafeProduction: "unset",
		Description:    "Enables the opt-in live semantic provider blend test.",
	},
	{
		Name: "VELOX_YOUTUBE_ASYNC_ENRICHMENT", Kind: KindEnv, Default: "",
		Owner:          "internal/capabilities/youtube/usecase/process_segment_step6to9.go",
		SafeProduction: "unset (inline enrichment) until the outbox worker is certified",
		Description:    "Commit YouTube clips before asynchronous metadata enrichment; boot refuses when the media outbox worker is unavailable.",
	},
}

var registryByName = func() map[string]Knob {
	byName := make(map[string]Knob, len(registry))
	for _, knob := range registry {
		byName[knob.Name] = knob
	}
	return byName
}()

// Registry returns a copy of the whole table (declaration order).
func Registry() []Knob {
	return append([]Knob(nil), registry...)
}

// Lookup returns the entry for an exact env name.
func Lookup(name string) (Knob, bool) {
	knob, ok := registryByName[strings.TrimSpace(name)]
	return knob, ok
}

// String returns the effective value of a knob: the process environment when
// the variable is SET, otherwise the registry default.
//
// An unregistered name is still read from the environment (callers outside
// this inventory must keep working), but no default is invented for it — the
// registry is the only place a default may live, so the guard test is what
// catches the omission.
func String(name string) string {
	name = strings.TrimSpace(name)
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	if knob, ok := Lookup(name); ok {
		return knob.Default
	}
	return ""
}

// Bool parses a knob with the truthy vocabulary the final-job gates already
// use ("1", "true", "on", "enabled", "yes"). An unset variable yields the
// registry default parsed the same way; a malformed value is false.
func Bool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(String(name))) {
	case "1", "true", "on", "enabled", "yes":
		return true
	default:
		return false
	}
}

// Setting is one overridden knob as observed on a running process.
type Setting struct {
	Name    string
	Value   string
	Default string
}

// NonDefault returns the env/config knobs whose effective value differs from
// the declared default, sorted by name. Markers and test-only knobs are
// excluded: they are not deployment configuration.
func NonDefault() []Setting {
	overrides := make([]Setting, 0, 4)
	for _, knob := range registry {
		if knob.Kind != KindEnv && knob.Kind != KindConfig {
			continue
		}
		value, set := os.LookupEnv(knob.Name)
		if !set || value == knob.Default {
			continue
		}
		overrides = append(overrides, Setting{Name: knob.Name, Value: value, Default: knob.Default})
	}
	sort.Slice(overrides, func(i, j int) bool { return overrides[i].Name < overrides[j].Name })
	return overrides
}

// LogNonDefault writes ONE line per overridden knob at boot. A host that runs
// with a non-default value must say so: a silent override is how a "measured"
// production run ends up describing a different configuration than the one
// documented.
func LogNonDefault(log *zap.Logger) {
	if log == nil {
		return
	}
	for _, override := range NonDefault() {
		log.Info("velox knob override",
			zap.String("knob", override.Name),
			zap.String("value", override.Value),
			zap.String("default", override.Default))
	}
}

// UnregisteredEnv names the VELOX_* variables present in environ that have no
// registry entry (and therefore no documented default or owner). It is used at
// boot as a warning and by the guard test as the failure condition; an empty
// result means the environment matches the inventory.
func UnregisteredEnv(environ []string) []string {
	var unknown []string
	for _, entry := range environ {
		name, _, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(name, "VELOX_") {
			continue
		}
		if _, ok := Lookup(name); !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// LogUnregisteredEnv warns once per VELOX_* variable the inventory does not
// know. It never fails boot: an unknown variable may belong to a subsystem
// outside the composition root, and the hard failure belongs in CI
// (TestEveryWiringKnobIsRegistered), not in a running host.
func LogUnregisteredEnv(log *zap.Logger, environ []string) {
	if log == nil {
		return
	}
	for _, name := range UnregisteredEnv(environ) {
		log.Warn("velox knob is not in the registry",
			zap.String("knob", name),
			zap.String("remedy", "register it in internal/app/knobs/registry.go"))
	}
}
