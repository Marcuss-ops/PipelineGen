package research

import (
	"net/url"
	"strings"
	"testing"
)

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func TestValidatePublicURLBlocksSSRFTargets(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com/x", "http://127.0.0.1:8000/ready", "http://localhost/x"} {
		if err := validatePublicURL(mustURL(raw)); err == nil {
			t.Fatalf("expected blocked URL: %s", raw)
		}
	}
}

func TestExtractHTMLRemovesActiveAndNavigationContent(t *testing.T) {
	text, title := extractHTML([]byte(`<html><head><title>Boxer</title><script>alert(1)</script></head><body><nav>menu</nav><main>career facts</main><footer>footer</footer></body></html>`), 100)
	if title != "Boxer" || !strings.Contains(text, "career facts") || strings.Contains(text, "alert") || strings.Contains(text, "menu") {
		t.Fatalf("title=%q text=%q", title, text)
	}
}
