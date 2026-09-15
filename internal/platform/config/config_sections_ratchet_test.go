package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// rootConfigYAMLs are the config surfaces an operator actually edits, ordered
// so a failure reads top-down. They are globbed rather than listed so a new
// root config file is covered by the ratchet the day it lands.
func rootConfigYAMLs(t *testing.T, root string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "config*.yaml"))
	if err != nil {
		t.Fatalf("glob config*.yaml: %v", err)
	}
	if len(matches) == 0 {
		t.Skip("no root config yaml found")
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, filepath.Base(m))
	}
	sort.Strings(names)
	return names
}

// knownConfigSections reflects the yaml tags of Config. The loader
// (GetFromPath) uses a non-strict yaml.Unmarshal, so a top-level key that is
// not one of these is silently discarded — which turns a typo in a settings
// block into "the setting quietly does nothing".
func knownConfigSections(t *testing.T) map[string]struct{} {
	t.Helper()
	typ := reflect.TypeOf(Config{})
	known := make(map[string]struct{}, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		known[tag] = struct{}{}
	}
	if len(known) == 0 {
		t.Fatal("Config declares no yaml sections; the ratchet would pass vacuously")
	}
	return known
}

func yamlTopLevelSections(t *testing.T, root, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Skipf("%s unreadable: %v", name, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s is not valid yaml: %v", name, err)
	}
	sections := make([]string, 0, len(doc))
	for key := range doc {
		sections = append(sections, key)
	}
	sort.Strings(sections)
	return sections
}

// unknownConfigSections is the predicate behind the ratchet, extracted so
// TestUnknownConfigSectionDetectionTrips can prove the detection fires instead
// of the suite only ever asserting that the tree happens to be clean.
func unknownConfigSections(sections []string, known map[string]struct{}) []string {
	var unknown []string
	for _, section := range sections {
		if _, ok := known[section]; !ok {
			unknown = append(unknown, section)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// TestConfigYAMLSectionsAreKnownToTheLoader closes the silent-ignore hole in
// the configuration surface: config.GetFromPath performs a NON-strict yaml
// decode, so a mistyped section name (qdrantt:, securityy:) parses cleanly,
// boots cleanly, and simply never applies. Nothing else in the suite catches
// that, because every other config test asserts on content of keys it already
// knows exists.
//
// The ratchet is one-directional on purpose: it fails when a YAML declares a
// section Config does not have, and stays silent when Config has a section no
// YAML sets (those are the optional, default-only blocks).
func TestConfigYAMLSectionsAreKnownToTheLoader(t *testing.T) {
	root := repoRoot(t)
	known := knownConfigSections(t)

	var checked int
	for _, name := range rootConfigYAMLs(t, root) {
		sections := yamlTopLevelSections(t, root, name)
		if len(sections) == 0 {
			continue
		}
		checked++
		for _, section := range unknownConfigSections(sections, known) {
			t.Errorf("%s declares top-level section %q, which is not a yaml field of config.Config: "+
				"the loader silently ignores it, so the settings under it never take effect",
				name, section)
		}
	}
	if checked == 0 {
		t.Skip("no config yaml declared any section")
	}
}

// TestExampleConfigsDifferOnlyByDocumentedProductionExtras is the ratchet
// between the two example files. They are meant to be the same document with
// one production-only extra, so a section that appears only in one of them is
// either a forgotten copy or a deliberate difference that has to be written
// down here.
func TestExampleConfigsDifferOnlyByDocumentedProductionExtras(t *testing.T) {
	root := repoRoot(t)

	base := yamlTopLevelSections(t, root, "config.example.yaml")
	production := yamlTopLevelSections(t, root, "config.production.example.yaml")
	if len(base) == 0 || len(production) == 0 {
		t.Skip("example config yaml missing")
	}

	// productionOnlySections is the reviewed, intentional difference:
	// media_postgresql is the production media SSOT switch and has no place
	// in the local development example.
	productionOnlySections := map[string]struct{}{"media_postgresql": {}}

	inBase := make(map[string]struct{}, len(base))
	for _, s := range base {
		inBase[s] = struct{}{}
	}
	inProduction := make(map[string]struct{}, len(production))
	for _, s := range production {
		inProduction[s] = struct{}{}
	}

	for section := range productionOnlySections {
		if _, ok := inProduction[section]; !ok {
			t.Errorf("config.production.example.yaml no longer declares %q; remove it from the documented extras", section)
		}
	}
	for _, section := range production {
		if _, ok := inBase[section]; ok {
			continue
		}
		if _, documented := productionOnlySections[section]; documented {
			continue
		}
		t.Errorf("config.production.example.yaml declares %q but config.example.yaml does not: "+
			"either copy it into the base example or document it as a production-only extra", section)
	}
	for _, section := range base {
		if _, ok := inProduction[section]; !ok {
			t.Errorf("config.example.yaml declares %q but config.production.example.yaml does not: "+
				"the production example must stay a superset of the base one", section)
		}
	}
}

// TestUnknownConfigSectionDetectionTrips proves the ratchet in
// TestConfigYAMLSectionsAreKnownToTheLoader actually detects the failure it
// exists for: a mistyped section name that the non-strict loader would accept
// and then ignore. Without this, the ratchet could decay into an assertion
// that only ever observes the current tree.
func TestUnknownConfigSectionDetectionTrips(t *testing.T) {
	known := map[string]struct{}{"server": {}, "features": {}}

	got := unknownConfigSections([]string{"features", "qdrantt", "server", "securityy"}, known)
	if len(got) != 2 || got[0] != "qdrantt" || got[1] != "securityy" {
		t.Fatalf("unknown sections = %v, want [qdrantt securityy]", got)
	}
	if clean := unknownConfigSections([]string{"server", "features"}, known); len(clean) != 0 {
		t.Fatalf("known sections reported as unknown: %v", clean)
	}
}
