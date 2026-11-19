package media

import (
	"context"
	"errors"
	"strings"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// The content-link boundary is the only place media_assets.content_sha256 is
// written, so a fabricated or MD5-length digest must fail closed before the
// UPDATE is built. A nil executor is safe here: validation runs first.
func TestLinkMediaAssetContentRejectsNonCanonicalDigest(t *testing.T) {
	err := LinkMediaAssetContent(context.Background(), nil, "asset-1", strings.Repeat("a", 32))
	if !errors.Is(err, capregistry.ErrInvalidContentSHA256) {
		t.Fatalf("error = %v, want ErrInvalidContentSHA256", err)
	}
}
