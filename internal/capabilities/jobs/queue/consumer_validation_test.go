package queue

import (
	"strings"
	"testing"
)

type consumerCatalogStub struct{ types []string }

func (s consumerCatalogStub) AllTypes() []string { return append([]string(nil), s.types...) }

type consumerBindingsStub map[string]bool

func (s consumerBindingsStub) HasHandler(jobType string) bool { return s[jobType] }

func TestValidateConsumers(t *testing.T) {
	catalog := consumerCatalogStub{types: []string{"z.job", "a.job", "m.job"}}
	bindings := consumerBindingsStub{"a.job": true, "z.job": true}

	err := ValidateConsumers(catalog, bindings)
	if err == nil || !strings.Contains(err.Error(), "m.job") {
		t.Fatalf("error = %v, want missing m.job", err)
	}
}

func TestValidateConsumersAcceptsCompleteBindings(t *testing.T) {
	catalog := consumerCatalogStub{types: []string{"a.job", "b.job"}}
	bindings := consumerBindingsStub{"a.job": true, "b.job": true}
	if err := ValidateConsumers(catalog, bindings); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConsumersNilInputsAreTolerated(t *testing.T) {
	if err := ValidateConsumers(nil, consumerBindingsStub{}); err != nil {
		t.Fatalf("nil catalog: %v", err)
	}
	if err := ValidateConsumers(consumerCatalogStub{}, nil); err != nil {
		t.Fatalf("nil bindings: %v", err)
	}
}
