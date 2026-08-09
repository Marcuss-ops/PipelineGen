package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadCanonicalPolicyRoundTrip(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	p, err := Load(filepath.Join(root, "architecture", "policy.yaml"))
	if err != nil {
		t.Fatalf("Load canonical policy: %v", err)
	}
	if p.MaxClipIngestPipelineFields != 9 {
		t.Fatalf("MaxClipIngestPipelineFields=%d, want 9", p.MaxClipIngestPipelineFields)
	}
	if p.MaxStructDeps != 8 {
		t.Fatalf("MaxStructDeps=%d, want 8", p.MaxStructDeps)
	}
	if p.MaxWarnings != 77 {
		t.Fatalf("MaxWarnings=%d, want 77", p.MaxWarnings)
	}
	if !contains(p.KernelSubzones, "observability") {
		t.Fatalf("KernelSubzones=%v, want observability to be registered", p.KernelSubzones)
	}
	if len(p.HardGates) == 0 {
		t.Fatal("hard_gates were not parsed")
	}
	if !contains(p.HardGates, "percheck_video_encoder_policy") {
		t.Fatalf("HardGates=%v, want percheck_video_encoder_policy", p.HardGates)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestLoadCanonicalApplicationAreas(t *testing.T) {
	t.Run("multiline list parses", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "policy.yaml")
		content := "canonical_application_areas:\n  - internal/application/images\n  - internal/application/scripts\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := []string{"internal/application/images", "internal/application/scripts"}
		if !reflect.DeepEqual(p.CanonicalApplicationAreas, want) {
			t.Fatalf("CanonicalApplicationAreas=%v, want %v", p.CanonicalApplicationAreas, want)
		}
	})

	for name, content := range map[string]string{
		"duplicate":           "canonical_application_areas:\n  - internal/application/images\n  - internal/application/images\n",
		"traversal":           "canonical_application_areas:\n  - internal/application/../images\n",
		"outside application": "canonical_application_areas:\n  - internal/infrastructure\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy.yaml")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "canonical application area") {
				t.Fatalf("expected canonical-area validation error, got %v", err)
			}
		})
	}
}

func TestLoadRejectsUnknownTopLevelKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte("max_files_per_package: 40\nunknown_arch_rule: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown architecture policy key") {
		t.Fatalf("expected unknown-key error, got %v", err)
	}
}

func TestLoadRejectsDuplicateTopLevelKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	content := "max_files_per_package: 40\nmax_files_per_package: 50\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicate architecture policy key") {
		t.Fatalf("expected duplicate-key error, got %v", err)
	}
}

func TestLoadRejectsDuplicateDocumentSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	content := "max_files_per_package: 40\nlint_gates:\nlint_gates:\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicate architecture policy key") {
		t.Fatalf("expected duplicate-key error, got %v", err)
	}
}

func TestPolicyBindingsCoverEveryModelFieldAndConsumer(t *testing.T) {
	covered := map[string]bool{}
	for key, binding := range policyBindings {
		if binding.Field == "" || binding.Consumer == "" || binding.Apply == nil {
			t.Fatalf("binding %q is incomplete: %+v", key, binding)
		}
		covered[binding.Field] = true
	}
	typ := reflect.TypeOf(Policy{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !covered[field.Name] {
			t.Errorf("Policy.%s has no parser/consumer binding", field.Name)
		}
	}
}
