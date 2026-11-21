package finalizer

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

func TestParseDrivePolicy_DefaultsToHistoricalAndRejectsTypos(t *testing.T) {
	for _, raw := range []string{"", "all", " ALL "} {
		policy, err := finalization.ParseDrivePolicy(raw)
		if err != nil || policy.Mode != finalization.DrivePolicyAll {
			t.Fatalf("ParseDrivePolicy(%q) = %+v, %v; want all", raw, policy, err)
		}
	}
	if policy, err := finalization.ParseDrivePolicy("deliverables"); err != nil || policy.Mode != finalization.DrivePolicyDeliverables {
		t.Fatalf("deliverables mode = %+v, %v", policy, err)
	}
	if _, err := finalization.ParseDrivePolicy("delivrables"); err == nil {
		t.Fatal("a typo must be an error, not a silent fallback")
	}
}

func TestDrivePolicy_RequiresDrive(t *testing.T) {
	intermediate := finalization.VerifiedArtifact{ArtifactMetadata: map[string]any{finalization.MetadataArtifactRole: "intermediate"}}
	deliverable := finalization.VerifiedArtifact{ArtifactMetadata: map[string]any{}}
	all := finalization.DefaultDrivePolicy()
	deliverables := finalization.DrivePolicy{Mode: finalization.DrivePolicyDeliverables}

	cases := []struct {
		name   string
		policy finalization.DrivePolicy
		art    finalization.VerifiedArtifact
		want   bool
	}{
		{"historical publishes intermediates", all, intermediate, true},
		{"historical publishes deliverables", all, deliverable, true},
		{"deliverables mode skips intermediates", deliverables, intermediate, false},
		{"deliverables mode publishes deliverables", deliverables, deliverable, true},
		{"unclassified metadata is a deliverable", deliverables, finalization.VerifiedArtifact{}, true},
		{"unknown role is a deliverable", deliverables, finalization.VerifiedArtifact{ArtifactMetadata: map[string]any{finalization.MetadataArtifactRole: "temp"}}, true},
	}
	for _, tc := range cases {
		if got := tc.policy.RequiresDrive(tc.art); got != tc.want {
			t.Errorf("%s: RequiresDrive = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestArtifactPreparation_IntermediateSkipsDrive is ELIM-03's behavioural
// proof: in deliverables mode an intermediate never reaches the publisher and
// comes back addressed by its content hash in the object store.
func TestArtifactPreparation_IntermediateSkipsDrive(t *testing.T) {
	artifact := writeVerifiedArtifact(t, "intermediate-bytes", map[string]any{
		finalization.MetadataArtifactRole: "intermediate",
	})
	publisher := &stubPublisherPort{location: finalization.AssetLocation{Provider: "drive", FileID: "drive-123"}}
	preparation := NewArtifactPreparationWithDrivePolicy(publisher, nil,
		finalization.DrivePolicy{Mode: finalization.DrivePolicyDeliverables})

	published, err := preparation.Prepare(context.Background(), artifact)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if publisher.got.ArtifactID != "" {
		t.Fatalf("the publisher was called for an intermediate (%q)", publisher.got.ArtifactID)
	}
	if published.Location.Provider != "objectstore" || published.Location.FileID != artifact.SHA256 {
		t.Fatalf("intermediate location = %+v, want objectstore/%s", published.Location, artifact.SHA256)
	}
	if published.ArtifactMetadata["drive_file_id"] != artifact.SHA256 {
		t.Fatalf("manifest reference = %v, want the content address", published.ArtifactMetadata["drive_file_id"])
	}
}

// TestArtifactPreparation_DeliverableStillPublishes keeps the delivery
// contract: the final video and the other deliverables keep their Drive
// identity in deliverables mode.
func TestArtifactPreparation_DeliverableStillPublishes(t *testing.T) {
	artifact := writeVerifiedArtifact(t, "final-bytes", nil)
	publisher := &stubPublisherPort{location: finalization.AssetLocation{Provider: "drive", FileID: "drive-999", WebViewLink: "https://drive.example/999"}}
	preparation := NewArtifactPreparationWithDrivePolicy(publisher, nil,
		finalization.DrivePolicy{Mode: finalization.DrivePolicyDeliverables})

	published, err := preparation.Prepare(context.Background(), artifact)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if publisher.got.ArtifactID != artifact.ArtifactID {
		t.Fatal("a deliverable must be published to Drive")
	}
	if published.Location.Provider != "drive" || published.ArtifactMetadata["drive_file_id"] != "drive-999" {
		t.Fatalf("deliverable location = %+v, want the Drive identity", published.Location)
	}
}

// TestArtifactPreparation_HistoricalPolicyPublishesIntermediate is the
// rollback guard: with the default policy nothing changes.
func TestArtifactPreparation_HistoricalPolicyPublishesIntermediate(t *testing.T) {
	artifact := writeVerifiedArtifact(t, "intermediate-bytes", map[string]any{
		finalization.MetadataArtifactRole: "intermediate",
	})
	publisher := &stubPublisherPort{location: finalization.AssetLocation{Provider: "drive", FileID: "drive-123"}}
	preparation := NewArtifactPreparationWithDrivePolicy(publisher, nil, finalization.DefaultDrivePolicy())

	if _, err := preparation.Prepare(context.Background(), artifact); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if publisher.got.ArtifactID != artifact.ArtifactID {
		t.Fatal("the historical policy must keep publishing every artifact")
	}
}
