package schema

import "testing"

func TestEmbeddingContract_SameDimensionDifferentModelFails(t *testing.T) {
	want := DefaultV3Schema().GetDense("text")
	got := want.Contract()
	got.Model = "multilingual-e5-base"
	if want.MatchesContract(got) {
		t.Fatal("same-dimensional different-model contract must fail closed")
	}
}

func TestEmbeddingContract_ExactContractMatches(t *testing.T) {
	want := DefaultV3Schema().GetDense("text")
	if !want.MatchesContract(want.Contract()) {
		t.Fatal("an embedding spec must match its own contract")
	}
}
