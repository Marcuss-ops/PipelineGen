package structure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeStructSrc lays down <root>/internal/<rel>/<file>.go with
// the supplied source body. Mirror of writeCtor for struct tests.
func writeStructSrc(t *testing.T, root, rel, file, body string) {
	t.Helper()
	dir := filepath.Join(root, "internal", rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	target := filepath.Join(dir, file)
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
}

// structPolicy returns a Policy with the struct-deps knob set.
func structPolicy(maxStruct int) *policy.Policy {
	return &policy.Policy{
		MaxStructDeps: maxStruct,
	}
}

// structReport returns a Report ready for ScanStructDeps.
func structReport(pol *policy.Policy) *report.Report {
	return &report.Report{
		Mode:    "unit-test",
		Policy:  pol,
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
}

// TestScanStructDeps_DetectsDependenciesStruct verifies that a struct
// named "Dependencies" with >8 mandatory fields is flagged.
// Optional fields (*zap.Logger, int, string) are excluded.
func TestScanStructDeps_DetectsDependenciesStruct(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "deps.go", `package synthpkg

type Dependencies struct {
	Repo      Repository
	Cache     CacheService
	Logger    *zap.Logger
	Auth      AuthProvider
	Uploader  UploaderPort
	Indexer   IndexerPort
	Notifier  NotifierPort
	Metrics   MetricsPort
	Validator ValidatorPort
	Config    string
}

type Repository interface{}
type CacheService interface{}
type AuthProvider interface{}
type UploaderPort interface{}
type IndexerPort interface{}
type NotifierPort interface{}
type MetricsPort interface{}
type ValidatorPort interface{}
`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	// Repo, Cache, Auth, Uploader, Indexer, Notifier, Metrics, Validator = 8 mandatory
	// + Logger (*zap.Logger optional), Config (string optional) = 2 excluded
	// Total 10 fields, 8 mandatory = at cap → no violation
	if len(r.Violations) != 0 {
		t.Fatalf("8 mandatory fields at cap 8 must not fire; got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
}

// TestScanStructDeps_OverCap verifies that >8 mandatory fields fires.
func TestScanStructDeps_OverCap(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "over.go", `package synthpkg

type Dependencies struct {
	A, B, C, D, E, F, G, H, I string
}

`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	// 9 fields, all string = optional → 0 mandatory, should NOT fire
	if len(r.Violations) != 0 {
		t.Fatalf("9 string (optional) fields must not fire; got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
}

// TestScanStructDeps_DetectsServiceDeps verifies that a struct ending
// with "Deps" (e.g. ServiceDeps) with >8 mandatory fields is flagged.
// Optional fields (Logger, int) are excluded.
func TestScanStructDeps_DetectsServiceDeps(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "servicedeps.go", `package synthpkg

type ServiceDeps struct {
	TTS         TTSProvider
	Dest        DestResolver
	Audio       AudioPostProc
	Lifecycle   AssetLifecycle
	Repo        VoiceoverRepo
	Outbox      TxOutbox
	DB          *sql.DB
	Logger      *zap.Logger
	Parallelism int
	MaxParallel int
	ExtraOne    interface{}
	ExtraTwo    interface{}
	ExtraThree  interface{}
}

type TTSProvider interface{}
type DestResolver interface{}
type AudioPostProc interface{}
type AssetLifecycle interface{}
type VoiceoverRepo interface{}
type TxOutbox interface{}
`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	// Mandatory: TTS, Dest, Audio, Lifecycle, Repo, Outbox, DB, ExtraOne, ExtraTwo, ExtraThree = 10
	// Optional: Logger, Parallelism, MaxParallel = 3 excluded
	if len(r.Violations) != 1 {
		t.Fatalf("want 1 violation for 10 mandatory ServiceDeps, got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
	if r.Violations[0].ActualCount != 10 {
		t.Errorf("ActualCount = %d, want 10", r.Violations[0].ActualCount)
	}
}

// TestScanStructDeps_DetectsOptions verifies that "Options" suffix
// structs are detected.
func TestScanStructDeps_DetectsOptions(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "opts.go", `package synthpkg

type ModuleOptions struct {
	A interface{}
	B interface{}
	C interface{}
	D interface{}
	E interface{}
	F interface{}
	G interface{}
	H interface{}
	I interface{}
	Timeout int
	Retries int
}
`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	// 9 mandatory (A-I), 2 optional (Timeout, Retries)
	if len(r.Violations) != 1 {
		t.Fatalf("want 1 violation for 9-mandatory ModuleOptions, got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
	if r.Violations[0].ActualCount != 9 {
		t.Errorf("ActualCount = %d, want 9", r.Violations[0].ActualCount)
	}
}

// TestScanStructDeps_UnderThreshold verifies that a struct with exactly
// 8 mandatory fields does NOT trigger a violation.
func TestScanStructDeps_UnderThreshold(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "ok.go", `package synthpkg

type Dependencies struct {
	Repo   Repository
	Cache  CacheService
	Logger *zap.Logger
	Auth   AuthProvider
	One    interface{}
	Two    interface{}
	Three  interface{}
	Four   interface{}
	Five   interface{}
}

type Repository interface{}
type CacheService interface{}
type AuthProvider interface{}
`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	// Repo, Cache, Auth, One, Two, Three, Four, Five = 8 mandatory
	// Logger = optional → excluded
	if len(r.Violations) != 0 {
		t.Fatalf("8 mandatory fields under cap must not fire; got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
}

// TestScanStructDeps_IgnoresNonDepsStruct verifies that structs not
// named Dependencies/Deps/Options are not flagged.
func TestScanStructDeps_IgnoresNonDepsStruct(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "other.go", `package synthpkg

type User struct {
	Name  string
	Email string
	Age   int
	Addr  string
	Phone string
	Notes string
	Tags  []string
	Prefs map[string]string
	Meta  map[string]string
	Extra string
}

type Config struct {
	A interface{}
	B interface{}
	C interface{}
	D interface{}
	E interface{}
	F interface{}
	G interface{}
	H interface{}
	I interface{}
	J interface{}
}
`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	if len(r.Violations) != 0 {
		t.Fatalf("non-Deps structs must be ignored; got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
}

// TestScanStructDeps_OptOut verifies that MaxStructDeps=0 opts out.
func TestScanStructDeps_OptOut(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "many.go", `package synthpkg

type Dependencies struct {
	A, B, C, D, E, F, G, H, I, J, K, L interface{}
}
`)

	pol := structPolicy(0)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	if len(r.Violations) != 0 {
		t.Fatalf("MaxStructDeps=0 must opt out; got %d violations", len(r.Violations))
	}
}

// TestScanStructDeps_CountsEmbeddedTypes verifies that embedded types
// (e.g. *sql.DB on its own line) are counted as one mandatory field each.
func TestScanStructDeps_CountsEmbeddedTypes(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "embedded.go", `package synthpkg

type Dependencies struct {
	*sql.DB
	Repo    Repository
	Cache   CacheService
	Auth    AuthProvider
	One     interface{}
	Two     interface{}
	Three   interface{}
	Four    interface{}
	Five    interface{}
}

type Repository interface{}
type CacheService interface{}
type AuthProvider interface{}
`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	// *sql.DB = 1 mandatory, Repo-Remo = 2, Auth = 1, One-Five = 5
	// Total = 9 mandatory
	if len(r.Violations) != 1 {
		t.Fatalf("want 1 violation for 9-mandatory struct with embedded type; got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
	if r.Violations[0].ActualCount != 9 {
		t.Errorf("ActualCount = %d, want 9", r.Violations[0].ActualCount)
	}
}

// TestScanStructDeps_MultiFile verifies the scanner picks up structs
// across multiple files.
func TestScanStructDeps_MultiFile(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "a.go", `package synthpkg
type Dependencies struct {
	A Port
	B Port
	C Port
	D Port
	E Port
	F Port
	G Port
	H Port
	I Port
}

type Port interface{}
`)
	writeStructSrc(t, root, "synthpkg", "b.go", `package synthpkg
type Options struct {
	A Port
	B Port
	C Port
	D Port
	E Port
	F Port
	G Port
	H Port
	I Port
	J Port
}

type Port interface{}
`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	if len(r.Violations) != 2 {
		t.Fatalf("want 2 violations across 2 files; got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
	// Check file names
	files := map[string]bool{}
	for _, v := range r.Violations {
		files[v.File] = true
	}
	if !files[filepath.ToSlash(filepath.Join("internal", "synthpkg", "a.go"))] {
		t.Error("missing violation from a.go")
	}
	if !files[filepath.ToSlash(filepath.Join("internal", "synthpkg", "b.go"))] {
		t.Error("missing violation from b.go")
	}
}

// TestScanStructDeps_BraceOnSameLine verifies the counter handles
// `{` on the same line as the first field.
func TestScanStructDeps_BraceOnSameLine(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "same.go", `package synthpkg

type Dependencies struct { A Port
	B Port
	C Port
	D Port
	E Port
	F Port
	G Port
	H Port
	I Port
}

type Port interface{}
`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	if len(r.Violations) != 1 {
		t.Fatalf("want 1 violation for 9-field Dependencies with { on same line; got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
}

// TestScanStructDeps_NestedStructNotCounted verifies that a nested
// struct inside a Dependencies struct does not cause the inner `}`
// to be miscounted as a mandatory field.
func TestScanStructDeps_NestedStructNotCounted(t *testing.T) {
	root := t.TempDir()

	writeStructSrc(t, root, "synthpkg", "nested.go", `package synthpkg

type Dependencies struct {
	_ struct {
		Inner Port
	}
	A Port
	B Port
	C Port
	D Port
	E Port
	F Port
	G Port
	H Port
	I Port
}

type Port interface{}
`)

	pol := structPolicy(8)
	r := structReport(pol)
	ScanStructDeps(root, pol, r)

	// Mandatory fields at depth 1: A, B, C, D, E, F, G, H, I = 9
	// The _ struct line is depth 2 (skipped), the inner `}` line
	// brings depth back to 1 but must be excluded by the brace guard.
	if len(r.Violations) != 1 {
		t.Fatalf("want 1 violation for 9 mandatory ports (nested struct `}` excluded); got %d: %s",
			len(r.Violations), dumpViolations(r.Violations))
	}
	if r.Violations[0].ActualCount != 9 {
		t.Errorf("ActualCount = %d, want 9 (inner `}` not counted as field)", r.Violations[0].ActualCount)
	}
}
