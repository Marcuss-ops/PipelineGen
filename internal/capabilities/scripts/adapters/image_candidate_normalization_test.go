package adapters

import "testing"

func TestIsInternetImageSourceProvider(t *testing.T) {
	for _, provider := range []string{"internet_images", "duckduckgo", "wikimedia_commons", "searxng"} {
		if !isInternetImageSourceProvider(provider) {
			t.Errorf("%q should enter the internet image lifecycle", provider)
		}
	}
	for _, provider := range []string{"youtube", "generated_images", "local_stock", "unknown"} {
		if isInternetImageSourceProvider(provider) {
			t.Errorf("%q must not enter the internet image lifecycle", provider)
		}
	}
}
