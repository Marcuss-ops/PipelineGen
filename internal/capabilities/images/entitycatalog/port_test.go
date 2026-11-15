package entitycatalog

import (
	"strings"
	"testing"
)

func TestCanonicalizePersonNameCollapsesCaseAndWhitespace(t *testing.T) {
	variants := []string{
		"Michael Jordan",
		"MICHAEL JORDAN",
		" Michael   Jordan ",
	}
	want := "person:michael-jordan"
	for _, variant := range variants {
		identity, err := CanonicalizePersonName(variant)
		if err != nil {
			t.Fatalf("CanonicalizePersonName(%q): %v", variant, err)
		}
		if identity.CanonicalEntityID != want {
			t.Fatalf("CanonicalizePersonName(%q) = %q, want %q", variant, identity.CanonicalEntityID, want)
		}
	}
}

func TestCanonicalizePersonNameKeepsMiddleNameDistinct(t *testing.T) {
	jordan, err := CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}
	bJordan, err := CanonicalizePersonName("Michael B. Jordan")
	if err != nil {
		t.Fatal(err)
	}
	if jordan.CanonicalEntityID == bJordan.CanonicalEntityID {
		t.Fatalf("distinct identities collided: %q", jordan.CanonicalEntityID)
	}
	if bJordan.CanonicalEntityID != "person:michael-b--jordan" {
		t.Fatalf("Michael B. Jordan ID = %q", bJordan.CanonicalEntityID)
	}
}

func TestCanonicalizePersonIdentityDerivesMissingID(t *testing.T) {
	identity, err := CanonicalizePersonIdentity("  Michael   Jordan  ", "")
	if err != nil {
		t.Fatal(err)
	}
	if identity.CanonicalEntityID != "person:michael-jordan" || identity.CanonicalName != "Michael Jordan" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestCanonicalizePersonIdentityRejectsMismatchedID(t *testing.T) {
	if _, err := CanonicalizePersonIdentity("Michael Jordan", "person:michael-b-jordan"); err == nil {
		t.Fatal("expected mismatched supplied ID to be rejected")
	}
}

func TestValidateEntityRequiresPersonAndMatchingCanonicalName(t *testing.T) {
	if err := ValidateEntity(Entity{
		CanonicalEntityID: "person:michael-jordan",
		EntityType:        "PERSON",
		CanonicalName:     "MICHAEL JORDAN",
	}); err != nil {
		t.Fatalf("equivalent PERSON variant rejected: %v", err)
	}
	if err := ValidateEntity(Entity{
		CanonicalEntityID: "person:michael-jordan",
		EntityType:        "PERSON",
		CanonicalName:     "Michael B. Jordan",
	}); err == nil {
		t.Fatal("expected name/ID collision to be rejected")
	}
}

func TestValidateMaterializationRejectsInvalidContentSHA(t *testing.T) {
	materialization := Materialization{CandidateID: 1, Status: MaterializationStatusPending}
	if err := ValidateMaterialization(materialization); err != nil {
		t.Fatalf("pending materialization without a hash rejected: %v", err)
	}
	materialization.ContentSHA256 = "sha-mj"
	if err := ValidateMaterialization(materialization); err == nil {
		t.Fatal("non-SHA materialization hash was accepted")
	}
	materialization.ContentSHA256 = strings.Repeat("a", 64)
	if err := ValidateMaterialization(materialization); err != nil {
		t.Fatalf("valid 64-hex materialization hash rejected: %v", err)
	}
}

func TestIsUsableCandidateQualityRequiresCurrentVerifierVersion(t *testing.T) {
	base := Candidate{
		CanonicalEntityID: "person:michael-jordan",
		Provider:          "wikimedia_commons",
		Rank:              1,
		SourceURL:         "https://images.example/michael-jordan/portrait.jpg",
		Status:            CandidateStatusFresh,
		SemanticStatus:    CandidateSemanticAccepted,
		SemanticScore:     0.9,
		TechnicalScore:    0.9,
		QualityReason:     "Ollama visual identity verification confirmed the requested person and one dominant subject",
		Width:             1200,
		Height:            800,
	}
	if IsUsableCandidateQuality(base) {
		t.Fatal("an unversioned accepted row must not be usable")
	}
	base.VerifierVersion = CandidateVerifierVersion
	if !IsUsableCandidateQuality(base) {
		t.Fatal("a current-verifier accepted row must be usable")
	}
	base.VerifierVersion = "legacy-pre-2026-11-14"
	if IsUsableCandidateQuality(base) {
		t.Fatal("a stale-verifier accepted row must not be usable")
	}
}
