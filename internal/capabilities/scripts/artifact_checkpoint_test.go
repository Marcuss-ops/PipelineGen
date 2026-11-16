package scriptgeneration

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validArtifactCheckpoint() ArtifactCheckpoint {
	sha := strings.Repeat("a", 64)
	return ArtifactCheckpoint{
		RunID:              "run-1",
		Kind:               ArtifactCheckpointLocalizedRender,
		Key:                "scene-0/clip-1/en",
		SchemaVersion:      ArtifactCheckpointSchemaVersion,
		InputFingerprint:   strings.Repeat("1", 64),
		ProcessorVersion:   "chronon/v1",
		ArtifactID:         "asset-1",
		ArtifactSHA256:     sha,
		ArtifactURI:        "cas://" + sha,
		ArtifactSizeBytes:  1024,
		ArtifactDurationUS: 5_000_000,
		Payload:            json.RawMessage(`{"asset_id":"asset-1","language":"en"}`),
		Attempt:            2,
		CompletedAt:        time.Date(2026, 11, 16, 12, 0, 0, 123456789, time.UTC),
	}
}

func TestArtifactCheckpointValidateAcceptsCompleteArtifact(t *testing.T) {
	cp := validArtifactCheckpoint()
	cp.PayloadSHA256 = cp.PayloadDigest()
	if err := cp.Validate(); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}
}

func TestArtifactCheckpointValidateRejectsIncompleteRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ArtifactCheckpoint)
	}{
		{"empty run id", func(c *ArtifactCheckpoint) { c.RunID = "" }},
		{"padded run id", func(c *ArtifactCheckpoint) { c.RunID = " run-1" }},
		{"unknown kind", func(c *ArtifactCheckpoint) { c.Kind = "made_up" }},
		{"empty key", func(c *ArtifactCheckpoint) { c.Key = "" }},
		{"padded key", func(c *ArtifactCheckpoint) { c.Key = "scene-0/clip-1/en " }},
		{"old schema", func(c *ArtifactCheckpoint) { c.SchemaVersion = 0 }},
		{"future schema", func(c *ArtifactCheckpoint) { c.SchemaVersion = ArtifactCheckpointSchemaVersion + 1 }},
		{"empty fingerprint", func(c *ArtifactCheckpoint) { c.InputFingerprint = "" }},
		{"empty processor version", func(c *ArtifactCheckpoint) { c.ProcessorVersion = "" }},
		{"padded artifact id", func(c *ArtifactCheckpoint) { c.ArtifactID = " asset-1" }},
		{"bad artifact sha", func(c *ArtifactCheckpoint) { c.ArtifactSHA256 = "not-a-sha" }},
		{"padded artifact uri", func(c *ArtifactCheckpoint) { c.ArtifactURI = "cas://x " }},
		{"negative size", func(c *ArtifactCheckpoint) { c.ArtifactSizeBytes = -1 }},
		{"negative duration", func(c *ArtifactCheckpoint) { c.ArtifactDurationUS = -1 }},
		{"invalid payload", func(c *ArtifactCheckpoint) { c.Payload = json.RawMessage(`{`) }},
		{"payload digest mismatch", func(c *ArtifactCheckpoint) { c.PayloadSHA256 = strings.Repeat("b", 64) }},
		{"negative attempt", func(c *ArtifactCheckpoint) { c.Attempt = -1 }},
		{"missing completion time", func(c *ArtifactCheckpoint) { c.CompletedAt = time.Time{} }},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cp := validArtifactCheckpoint()
			tc.mutate(&cp)
			err := cp.Validate()
			if !errors.Is(err, ErrInvalidArtifactCheckpoint) {
				t.Fatalf("Validate() error = %v, want ErrInvalidArtifactCheckpoint", err)
			}
		})
	}
}

func TestArtifactCheckpointValidateRejectsOversizedPayload(t *testing.T) {
	cp := validArtifactCheckpoint()
	cp.Payload = json.RawMessage(`"` + strings.Repeat("a", MaxArtifactCheckpointPayloadBytes) + `"`)
	if err := cp.Validate(); !errors.Is(err, ErrInvalidArtifactCheckpoint) {
		t.Fatalf("Validate() error = %v, want oversized payload rejection", err)
	}
}

func TestArtifactCheckpointEmptyPayloadIsCanonicalJSONObject(t *testing.T) {
	cp := validArtifactCheckpoint()
	cp.Payload = nil
	if got := string(cp.CanonicalPayload()); got != "{}" {
		t.Fatalf("CanonicalPayload() = %q, want {}", got)
	}
	if got := cp.PayloadDigest(); got != "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a" {
		t.Fatalf("PayloadDigest() = %q, want SHA-256 of the canonical empty object", got)
	}
}

func TestArtifactCheckpointKeyRejectsAmbiguousSegments(t *testing.T) {
	key, err := ArtifactCheckpointKey("scene-0", "clip-1", "en")
	if err != nil || key != "scene-0/clip-1/en" {
		t.Fatalf("ArtifactCheckpointKey = %q, %v; want scene-0/clip-1/en", key, err)
	}
	for _, parts := range [][]string{
		nil,
		{""},
		{"scene-0", ""},
		{"scene-0", "clip/1"},
		{"scene-0", " en"},
	} {
		if _, err := ArtifactCheckpointKey(parts...); !errors.Is(err, ErrInvalidArtifactCheckpoint) {
			t.Fatalf("ArtifactCheckpointKey(%q) error = %v, want ErrInvalidArtifactCheckpoint", parts, err)
		}
	}
}

func TestArtifactCheckpointReusableForRequiresEveryDimension(t *testing.T) {
	cp := validArtifactCheckpoint()
	if ok, reason := cp.ReusableFor(cp.InputFingerprint, cp.ProcessorVersion); !ok {
		t.Fatalf("matching checkpoint rejected: %s", reason)
	}
	if ok, reason := cp.ReusableFor("different", cp.ProcessorVersion); ok || reason != "input fingerprint changed" {
		t.Fatalf("changed fingerprint = %v/%q", ok, reason)
	}
	if ok, reason := cp.ReusableFor(cp.InputFingerprint, "chronon/v2"); ok || reason != "processor version changed" {
		t.Fatalf("changed processor = %v/%q", ok, reason)
	}
	cp.SchemaVersion = ArtifactCheckpointSchemaVersion + 1
	if ok, reason := cp.ReusableFor(cp.InputFingerprint, cp.ProcessorVersion); ok || reason != "checkpoint schema version changed" {
		t.Fatalf("changed schema = %v/%q", ok, reason)
	}
}
