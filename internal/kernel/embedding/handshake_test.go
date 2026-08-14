package embedding

import (
	"errors"
	"testing"
)

func TestVerify_AllLegsMatch(t *testing.T) {
	qdrant := Contract{Dimension: CanonicalText.Dimension, Distance: CanonicalText.Distance}
	query := Contract{ModelID: CanonicalText.ModelID}
	if err := Verify(CanonicalText, CanonicalText, qdrant, query); err != nil {
		t.Fatalf("all-matching legs must pass, got: %v", err)
	}
}

func TestVerify_SidecarMismatch(t *testing.T) {
	sidecar := CanonicalText
	sidecar.ModelID = "nomic-embed-text"
	err := Verify(CanonicalText, sidecar, Contract{}, Contract{})
	assertMismatch(t, err, ComponentSidecar)
}

func TestVerify_QdrantDimensionMismatch(t *testing.T) {
	qdrant := Contract{Dimension: 512, Distance: CanonicalText.Distance}
	err := Verify(CanonicalText, CanonicalText, qdrant, Contract{})
	assertMismatch(t, err, ComponentQdrant)
}

func TestVerify_QueryModelMismatch(t *testing.T) {
	query := Contract{ModelID: "nomic-embed-text"}
	err := Verify(CanonicalText, CanonicalText, Contract{}, query)
	assertMismatch(t, err, ComponentQuery)
}

func TestVerify_MismatchErrorSemantics(t *testing.T) {
	sidecar := CanonicalText
	sidecar.Dimension = 384
	err := Verify(CanonicalText, sidecar, Contract{}, Contract{})

	if !errors.Is(err, ErrContractMismatch) {
		t.Fatalf("errors.Is(err, ErrContractMismatch) = false, want true")
	}
	var me *MismatchError
	if !errors.As(err, &me) {
		t.Fatalf("errors.As(*MismatchError) = false, want true; got %T", err)
	}
	if me.Code() != "QDRANT_EMBEDDING_CONTRACT_MISMATCH" {
		t.Fatalf("Code() = %q, want QDRANT_EMBEDDING_CONTRACT_MISMATCH", me.Code())
	}
	if me.Component != ComponentSidecar {
		t.Fatalf("Component = %q, want %q", me.Component, ComponentSidecar)
	}
}

func assertMismatch(t *testing.T, err error, wantComponent string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected mismatch error for component %q, got nil", wantComponent)
	}
	var me *MismatchError
	if !errors.As(err, &me) {
		t.Fatalf("expected *MismatchError, got %T: %v", err, err)
	}
	if me.Component != wantComponent {
		t.Fatalf("Component = %q, want %q (err: %v)", me.Component, wantComponent, err)
	}
	if me.Code() != "QDRANT_EMBEDDING_CONTRACT_MISMATCH" {
		t.Fatalf("Code() = %q, want QDRANT_EMBEDDING_CONTRACT_MISMATCH", me.Code())
	}
}
