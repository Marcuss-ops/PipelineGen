package entitycatalog

import (
	"strings"
	"testing"
)

func TestValidateMaterializationRejectsNonSHA256Content(t *testing.T) {
	err := ValidateMaterialization(Materialization{CandidateID: 1, ContentSHA256: "sha-mj"})
	if err == nil || !strings.Contains(err.Error(), "64 hex") {
		t.Fatalf("non-SHA materialization error = %v, want a 64-hex rejection", err)
	}
}
