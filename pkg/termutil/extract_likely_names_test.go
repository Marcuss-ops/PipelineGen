package termutil

import (
	"reflect"
	"testing"
)

func TestExtractLikelyNames(t *testing.T) {
	got := ExtractLikelyNames("John and Mary went to Paris. John saw Mary.")
	want := []string{"John", "Mary", "Paris"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractLikelyNames() = %v, want %v", got, want)
	}
}
