package knobs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// veloxNamePattern matches a knob-shaped token in source or in the
// environment. Keeping the same pattern for both the scan and the runtime
// check means the guard test and the boot warning can never disagree.
var veloxNamePattern = regexp.MustCompile(`VELOX_[A-Z0-9_]+`)

// TestEveryWiringKnobIsRegistered is the guard the ticket asked for: a
// VELOX_* name that appears in the composition root without a registry row is
// a knob with no documented default, no owner and no generated runbook entry,
// so it fails the build instead of being discovered in production.
func TestEveryWiringKnobIsRegistered(t *testing.T) {
	wiringDir := filepath.Join("..", "wiring")
	var files int
	err := filepath.WalkDir(wiringDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files++
		for _, token := range veloxNamePattern.FindAllString(string(body), -1) {
			if _, ok := Lookup(token); !ok {
				t.Errorf("%s: %s is not registered in internal/app/knobs/registry.go", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", wiringDir, err)
	}
	if files == 0 {
		t.Fatalf("no Go source files scanned under %s", wiringDir)
	}
}

// TestRegistryRowsAreWellFormed keeps the table itself reviewable: exact
// names, declared kinds, no duplicates, and an owner + production value +
// description on every row (a row without them is not documentation).
func TestRegistryRowsAreWellFormed(t *testing.T) {
	seen := make(map[string]bool, len(registry))
	for _, knob := range registry {
		if !strings.HasPrefix(knob.Name, "VELOX_") || !veloxNamePattern.MatchString(knob.Name) {
			t.Errorf("bad knob name %q", knob.Name)
		}
		if seen[knob.Name] {
			t.Errorf("duplicate knob %q", knob.Name)
		}
		seen[knob.Name] = true
		switch knob.Kind {
		case KindEnv, KindConfig, KindTestOnly, KindMarker:
		default:
			t.Errorf("%s: unknown kind %q", knob.Name, knob.Kind)
		}
		if strings.TrimSpace(knob.Owner) == "" {
			t.Errorf("%s: owner is required", knob.Name)
		}
		if strings.TrimSpace(knob.SafeProduction) == "" {
			t.Errorf("%s: safe production value is required", knob.Name)
		}
		if strings.TrimSpace(knob.Description) == "" {
			t.Errorf("%s: description is required", knob.Name)
		}
		// An empty default is legal ("unset is the default"), but it must be
		// the empty string, never padded whitespace that would reach the
		// generated config verbatim.
		if knob.Default != strings.TrimSpace(knob.Default) {
			t.Errorf("%s: default %q has surrounding whitespace", knob.Name, knob.Default)
		}
	}
}

// TestNonDefaultLogsEveryOverride pins the boot log: a host that runs with a
// non-default knob must say so, with the value AND the default it overrides.
func TestNonDefaultLogsEveryOverride(t *testing.T) {
	t.Setenv("VELOX_FINAL_JOB_PREPARE_LANES", "7")
	core, observed := observer.New(zap.InfoLevel)
	LogNonDefault(zap.New(core))

	entries := observed.FilterMessage("velox knob override").All()
	if len(entries) == 0 {
		t.Fatal("no 'velox knob override' log line for a non-default knob")
	}
	found := false
	for _, entry := range entries {
		fields := entry.ContextMap()
		if fields["knob"] == "VELOX_FINAL_JOB_PREPARE_LANES" {
			found = true
			if fields["value"] != "7" {
				t.Errorf("value field = %v, want 7", fields["value"])
			}
			if fields["default"] != "4" {
				t.Errorf("default field = %v, want 4", fields["default"])
			}
		}
	}
	if !found {
		t.Errorf("override line does not name VELOX_FINAL_JOB_PREPARE_LANES: %v", entries)
	}

	// A knob back at its default is silent.
	t.Setenv("VELOX_FINAL_JOB_PREPARE_LANES", "4")
	core, observed = observer.New(zap.InfoLevel)
	LogNonDefault(zap.New(core))
	for _, setting := range NonDefault() {
		if setting.Name == "VELOX_FINAL_JOB_PREPARE_LANES" {
			t.Errorf("default value was reported as an override: %+v", setting)
		}
	}
}

// TestUnregisteredEnvIsWarnedAndReported pins the runtime half of the guard:
// a knob-shaped variable the inventory does not know is named, and boot
// continues (the hard failure belongs to the guard test, not to a host).
func TestUnregisteredEnvIsWarnedAndReported(t *testing.T) {
	t.Setenv("VELOX_NOT_A_REGISTERED_KNOB", "1")
	unknown := UnregisteredEnv(os.Environ())
	found := false
	for _, name := range unknown {
		if name == "VELOX_NOT_A_REGISTERED_KNOB" {
			found = true
		}
		if _, ok := Lookup(name); ok {
			t.Errorf("%s was reported as unregistered but is in the table", name)
		}
	}
	if !found {
		t.Fatalf("VELOX_NOT_A_REGISTERED_KNOB missing from %v", unknown)
	}

	core, observed := observer.New(zap.WarnLevel)
	LogUnregisteredEnv(zap.New(core), os.Environ())
	if observed.FilterMessage("velox knob is not in the registry").Len() == 0 {
		t.Error("no warning for an unregistered VELOX_* variable")
	}
}

// TestGeneratedKnobArtifactsAreCurrent fails when the committed example config
// or runbook no longer matches the registry — the drift that made the three
// hand-maintained lists disagree in the first place.
func TestGeneratedKnobArtifactsAreCurrent(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	if err := CheckArtifacts(root); err != nil {
		t.Fatal(err)
	}
}
