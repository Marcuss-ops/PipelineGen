package checkpoint

import (
	"strings"
	"testing"
	"time"
)

func hash64(c byte) string { return strings.Repeat(string(c), 64) }

func validCheckpoint() Checkpoint {
	return Checkpoint{
		JobID:            "job_ABC",
		Stage:            StageRenderScene,
		UnitID:           "scene_01",
		InputFingerprint: hash64('1'),
		Status:           StatusCompleted,
		ArtifactSHA256:   hash64('a'),
		ArtifactURI:      "cas://" + hash64('a'),
		ProcessorVersion: "rust-render/v3",
		CompletedAt:      time.Now().UTC(),
	}
}

func TestCheckpointValidateAcceptsCompleteRecord(t *testing.T) {
	if err := validCheckpoint().Validate(); err != nil {
		t.Fatalf("complete checkpoint must validate: %v", err)
	}
}

func TestCheckpointValidateAllowsArtifactlessStage(t *testing.T) {
	c := validCheckpoint()
	c.Stage = StageResearch
	c.UnitID = UnitGlobal
	c.ArtifactSHA256 = ""
	c.ArtifactURI = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("artifactless stage checkpoint must validate: %v", err)
	}
}

func TestCheckpointValidateRejectsIncompleteRecord(t *testing.T) {
	cases := map[string]func(*Checkpoint){
		"missing job id":         func(c *Checkpoint) { c.JobID = "  " },
		"missing stage":          func(c *Checkpoint) { c.Stage = "" },
		"missing unit id":        func(c *Checkpoint) { c.UnitID = "" },
		"missing fingerprint":    func(c *Checkpoint) { c.InputFingerprint = "" },
		"missing status":         func(c *Checkpoint) { c.Status = "" },
		"missing processor":      func(c *Checkpoint) { c.ProcessorVersion = "" },
		"bad artifact sha256":    func(c *Checkpoint) { c.ArtifactSHA256 = "not-a-hash" },
		"uppercase artifact sha": func(c *Checkpoint) { c.ArtifactSHA256 = strings.ToUpper(hash64('a')) },
		"missing completed at":   func(c *Checkpoint) { c.CompletedAt = time.Time{} },
	}
	for name, mutate := range cases {
		c := validCheckpoint()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}
