package retrieved

import (
	"errors"
	"reflect"
	"testing"
)

func TestResolve_EmptyIDs_ReturnsEmpty(t *testing.T) {
	r := NewRetrievalProviderRegistry(nil, nil)
	got, err := r.Resolve([]string{})
	if err != nil {
		t.Errorf("Resolve([]) err = %v; want nil", err)
	}
	if got != nil && len(got) != 0 {
		t.Errorf("len(Resolve([])) = %d; want 0", len(got))
	}
}

func TestResolve_KnownID_ReturnsProvider(t *testing.T) {
	r := NewRetrievalProviderRegistry(nil, []RetrievalProvider{
		NewWikipediaProvider(nil, nil, nil, ""),
	})
	got, err := r.Resolve([]string{"wikipedia"})
	if err != nil {
		t.Errorf("Resolve([wikipedia]) err = %v; want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Resolve([wikipedia])) = %d; want 1", len(got))
	}
	if got[0].Name() != "wikipedia" {
		t.Errorf("got[0].Name() = %q; want wikipedia", got[0].Name())
	}
}

func TestResolve_MissingID_ReturnsErrProviderNotFound(t *testing.T) {
	r := NewRetrievalProviderRegistry(nil, []RetrievalProvider{
		NewWikipediaProvider(nil, nil, nil, ""),
	})
	_, err := r.Resolve([]string{"wikipedia", "duckduckgo"})
	if err == nil {
		t.Fatalf("Resolve([wikipedia,duckduckgo]) err = nil; want ErrProviderNotFound")
	}
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("Resolve([wikipedia,duckduckgo]) err = %v; want errors.Is(_, ErrProviderNotFound)", err)
	}
}

func TestTypeSystem_Provider_AliasMatchesRetrievalProvider(t *testing.T) {
	pType := reflect.TypeOf((*Provider)(nil)).Elem()
	rType := reflect.TypeOf((*RetrievalProvider)(nil)).Elem()
	if pType != rType {
		t.Errorf("alias broken: Provider (%v) != RetrievalProvider (%v)", pType, rType)
	}
}
