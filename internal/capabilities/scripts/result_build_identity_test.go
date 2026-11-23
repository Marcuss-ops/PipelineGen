package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAttachBuildIdentityStampsResult(t *testing.T) {
	t.Setenv("BRAINRUST_RELEASE", "v0.2.0")
	result := &GenerateResult{}
	attachBuildIdentity(result)
	if result.RustRelease != "v0.2.0" {
		t.Fatalf("rust release = %q, want v0.2.0", result.RustRelease)
	}
}

// The report must name the Rust planner that actually produced the plan, not
// only its release tag. The manifest written by build-semantic-engine carries
// the commit and the installed binary digest; both reach the durable result.
func TestAttachBuildIdentityStampsTheEngineFromTheManifest(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "BRAINRUST_RELEASE.json")
	body := []byte(`{"version":"v9.9.9","git_sha":"53ea9829aeb5cf944978d5335decb7478a5dc0cf","binaries":{"semantic-engine":"5674454FE817B02026C5F134E071C8366EE1A625A38704B4A9AC7FA51B9BC428"}}`)
	if err := os.WriteFile(manifest, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAINRUST_RELEASE", "")
	t.Setenv("BRAINRUST_RELEASE_MANIFEST", manifest)

	result := &GenerateResult{}
	attachBuildIdentity(result)
	if result.RustRelease != "v9.9.9" {
		t.Fatalf("rust release = %q, want v9.9.9", result.RustRelease)
	}
	if result.RustGitSHA != "53ea9829aeb5cf944978d5335decb7478a5dc0cf" {
		t.Fatalf("rust git sha = %q, want the manifest commit", result.RustGitSHA)
	}
	if result.RustBinarySHA256 != "5674454fe817b02026c5f134e071c8366ee1a625a38704b4a9ac7fa51b9bc428" {
		t.Fatalf("semantic-engine sha256 = %q, want the manifest digest", result.RustBinarySHA256)
	}
	// The durable result is what the job report serializes: the provenance must
	// survive the JSON boundary, not only the in-memory struct.
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["rust_git_sha"] != result.RustGitSHA {
		t.Fatalf("serialized rust_git_sha = %v, want %q", payload["rust_git_sha"], result.RustGitSHA)
	}
	if payload["rust_binary_sha256"] != result.RustBinarySHA256 {
		t.Fatalf("serialized rust_binary_sha256 = %v, want %q", payload["rust_binary_sha256"], result.RustBinarySHA256)
	}
}
