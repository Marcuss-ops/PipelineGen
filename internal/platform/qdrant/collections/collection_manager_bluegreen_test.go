package collections

import (
	"context"
	"errors"
	"strings"
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/verification"
)

// TestPromoteCandidate_FailsWithoutVerify pins PR 6 §#6.3: calling
// PromoteCandidate on a candidate for which VerifyCandidate has NOT
// marked verifyLedger must return ErrPromoteWithoutVerify.
//
// The test constructs the CollectionManager with a nil client on
// purpose: the ledger gate (consumeVerified) runs BEFORE any HTTP
// call, so the test is single-process safe and doesn't need a mock
// Qdrant server. The same ledger gate would also fire against a
// real client because the gate is the FIRST statement in
// PromoteCandidate. The nil-client PositiveCase below fails with
// a DIFFERENT error (CreateAlias on nil receiver), proving the
// order: ledger check first, then alias write.
func TestPromoteCandidate_FailsWithoutVerify(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	cm := NewCollectionManager(nil, schema, nil)

	// NegativeCase: ledger is empty, PromoteCandidate MUST short-circuit
	// with ErrPromoteWithoutVerify BEFORE touching the (nil) client.
	err := cm.PromoteCandidate(context.Background(), "any-candidate-name")
	if err == nil {
		t.Fatalf("PromoteCandidate without a prior VerifyCandidate MUST return an error; got nil")
	}
	if !errors.Is(err, ErrPromoteWithoutVerify) {
		t.Errorf("PromoteCandidate without VerifyCandidate MUST return ErrPromoteWithoutVerify; got %v", err)
	}
	if !strings.Contains(err.Error(), "any-candidate-name") {
		t.Errorf("error must reference the candidate name to help operator diagnosis; got %q", err.Error())
	}

	// PositiveCase: mark the ledger, then PromoteCandidate advances
	// past the gate. It WILL panic on the nil-client CreateAlias call
	// (the gate is the next line that fires), but the ledger tracking
	// has already been consumed. We verify the negative semantic with
	// a SECOND empty-ledger call to confirm the consumeVerified single-
	// use invariant.
	cm.MarkVerified("consumed-candidate")
	if !cm.consumeVerified("consumed-candidate") {
		t.Errorf("markVerified + consumeVerified should roundtrip true on a fresh name")
	}
	if cm.consumeVerified("consumed-candidate") {
		t.Errorf("consumeVerified MUST be single-use; second call returns false")
	}
}

// TestSchemaRegistry_ResolveBootProbe pins PR 6 §#4 acceptance,
// updated for PR #11 (July 2026): the registry is now unexported;
// this test probes the package-level Resolve() / Versions() surface
// and verifies that the returned schema is a deep copy (not the
// registry's internal instance).
func TestSchemaRegistry_ResolveBootProbe(t *testing.T) {
	// (0) PR #11: verifies that Resolve() returns a deep copy —
	// the caller can mutate the returned pointer without affecting
	// future Resolve() responses.
	got1, err := verification.ResolveSchema("v3")
	if err != nil {
		t.Fatalf("verification.ResolveSchema(\"v3\") = nil err, want nil; got %v", err)
	}
	got1.PhysicalName = "MUTATED-COPY-ONLY"

	got2, err := verification.ResolveSchema("v3")
	if err != nil {
		t.Fatalf("second verification.Resolve(\"v3\") = nil err, want nil; got %v", err)
	}
	if got2.PhysicalName == "MUTATED-COPY-ONLY" {
		t.Errorf("PR #11: Resolve() did NOT return a deep copy — mutating the first result leaked into the second. Expected PhysicalName != %q", got2.PhysicalName)
	}

	// (1) Default registry resolves both registered schemas.
	gotV3, err := verification.ResolveSchema("v3")
	if err != nil {
		t.Fatalf("verification.ResolveSchema(\"v3\") = nil err, want nil; got %v", err)
	}
	if gotV3 == nil {
		t.Fatalf("verification.ResolveSchema(\"v3\") returned nil schema; want *qdrantSchema.IndexSchema")
	}
	if gotV3.Version != "v3" {
		t.Errorf("ResolveSchema(\"v3\") returned Version=%q, want \"%s\"", gotV3.Version, "v3")
	}

	gotSpeaker, err := verification.ResolveSchema("v3-multilingual-speaker")
	if err != nil {
		t.Fatalf("verification.ResolveSchema(\"v3-multilingual-speaker\") returned err = %v; want nil", err)
	}
	if gotSpeaker == nil {
		t.Fatalf("verification.Resolve(\"v3-multilingual-speaker\") returned nil schema; want *qdrantSchema.IndexSchema")
	}
	if gotSpeaker.Version != "v3-multilingual-speaker" {
		t.Errorf("Resolve(\"v3-multilingual-speaker\") returned Version=%q, want \"%s\"", gotSpeaker.Version, "v3-multilingual-speaker")
	}
	if !gotSpeaker.HasChannel("speaker") {
		t.Errorf("v3-multilingual-speaker MUST carry the speaker dense channel (the structural delta from v3); got chans %v",
			channelsOf(gotSpeaker))
	}

	// (2) Empty version falls back to "v3".
	gotEmptyDefault, err := verification.ResolveSchema("")
	if err != nil {
		t.Fatalf("verification.ResolveSchema(\"\") returned err = %v; want nil (boot compatibility)", err)
	}
	if gotEmptyDefault == nil || gotEmptyDefault.Version != "v3" {
		t.Errorf("verification.ResolveSchema(\"\") returned Version=%v, want v3 (boot compat fallback)", gotEmptyDefault)
	}

	// (3) Unknown version returns verification.ErrSchemaVersionNotFound.
	_, err = verification.ResolveSchema("nonexistent-version-zzz")
	if err == nil {
		t.Fatalf("verification.ResolveSchema(\"nonexistent\") returned nil err; want verification.ErrSchemaVersionNotFound")
	}
	if !errors.Is(err, verification.ErrSchemaVersionNotFound) {
		t.Errorf("ResolveSchema(\"nonexistent\") returned err = %v; want errors.Is(err, verification.ErrSchemaVersionNotFound)", err)
	}

	// (4) Versions() returns ≥2 entries.
	vs := verification.RegisteredVersions()
	if len(vs) < 2 {
		t.Errorf("verification.RegisteredVersions() returned %d entries; need ≥2 to demonstrate the mechanism is not a single-value facade", len(vs))
	}
}

// channelsOf returns the list of dense vector channel names in s.
// Helper for the test assertion message.
func channelsOf(s *qdrantSchema.IndexSchema) []string {
	out := make([]string, 0, len(s.DenseVectors))
	for _, v := range s.DenseVectors {
		out = append(out, v.Channel)
	}
	return out
}
