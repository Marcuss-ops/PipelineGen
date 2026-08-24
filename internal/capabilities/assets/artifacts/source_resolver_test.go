package assets

import (
	"errors"
	"testing"
)

func TestNewSourceCatalogRejectsMissingRepository(t *testing.T) {
	_, err := NewSourceCatalog(nil, nil, nil, nil, nil)
	if !errors.Is(err, ErrSourceCatalogDependencyUnavailable) {
		t.Fatalf("NewSourceCatalog error = %v, want ErrSourceCatalogDependencyUnavailable", err)
	}
}
