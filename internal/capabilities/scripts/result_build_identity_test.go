package scriptgeneration

import (
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
