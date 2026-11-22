//go:build manual

package digest

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMeasureCollageFalsePositives re-runs the production detector on the URLs
// rejected as "panel grid collage" in run R30 and keeps the bytes so the
// verdict can be checked visually. Throwaway: run with -tags manual.
func TestMeasureCollageFalsePositives(t *testing.T) {
	cases := []struct{ label, url string }{
		{"typewriters-corridor", "https://c8.alamy.com/comp/T8HD2T/typewriters-in-a-corridor-of-the-link-campus-university-inside-casale-di-san-pio-v-saint-pio-v-house-in-rome-italy-T8HD2T.jpg"},
		{"rai-building", "https://c8.alamy.com/comp/3DRDRK8/building-of-the-italian-national-television-rai-in-rome-eur-itay-3DRDRK8.jpg"},
		{"rai-logo", "https://c8.alamy.com/comp/2X63A33/turin-italy-may-10-2024-logo-of-rai-radio-televisione-italiana-on-a-wall-sign-with-light-blue-and-yellow-spot-reflections-rai-is-the-italian-rad-2X63A33.jpg"},
		{"docsity-page", "https://static.docsity.com/documents_first_pages/2019/05/21/fd8ccaa4c68ad24f88ef27c2946ebfa9.png?v=1649278152"},
	}

	outDir := "/tmp/collage-measure"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	client := &http.Client{Timeout: 25 * time.Second}
	for _, tc := range cases {
		resp, err := client.Get(tc.url)
		if err != nil {
			t.Logf("%-20s FETCH-ERROR %v", tc.label, err)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			t.Logf("%-20s HTTP %d readErr=%v", tc.label, resp.StatusCode, readErr)
			continue
		}
		path := filepath.Join(outDir, tc.label)
		_ = os.WriteFile(path, body, 0o644)
		img, format, decErr := image.Decode(bytes.NewReader(body))
		if decErr != nil {
			t.Logf("%-20s decode-error %v", tc.label, decErr)
			continue
		}
		v, h, vc, hc := PanelGridCollageEvidence(img)
		b := img.Bounds()
		t.Logf("%-20s format=%-4s %dx%d seams_v=%d seams_h=%d cont_v=%.2f cont_h=%.2f collage=%v",
			tc.label, format, b.Dx(), b.Dy(), v, h, vc, hc, HasPanelGridCollageImage(img))
		fmt.Printf("saved %s\n", path)
	}
}
