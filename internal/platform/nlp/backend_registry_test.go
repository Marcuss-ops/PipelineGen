package nlp

import (
	"testing"
)

func TestNewBackendRegistryRequiresConfiguredSpacyEndpoint(t *testing.T) {
	if _, err := NewBackendRegistry("spacy", "", "", "", nil); err == nil {
		t.Fatal("missing spaCy endpoint must fail closed")
	}
	registry, err := NewBackendRegistry("rust", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve("xlm-roberta"); err == nil {
		t.Fatal("unregistered XLM-R backend must not fall back")
	}
}
