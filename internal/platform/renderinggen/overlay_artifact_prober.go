package renderinggen

// overlay_artifact_prober.go answers the one question a content-addressed
// render cache cannot answer from its own index: are the bytes behind this
// certified locator still in the object store?
//
// The cache stores a locator, not a copy. Object stores have retention
// policies and are occasionally swept, so trusting the index would hand a dead
// URL to the publication step on every subsequent run. A HEAD costs one
// round-trip and turns a poison entry into a miss.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// OverlayArtifactProber checks a certified artifact URL with HEAD. It shares
// the bounded object-store client and bearer credential used by the prefetch
// bridge, so a private store behaves exactly as it does for uploads.
type OverlayArtifactProber struct {
	client *http.Client
}

// NewOverlayArtifactProber builds the prober on the shared object-store HTTP
// client (5-minute timeout, pooled keep-alive connections).
func NewOverlayArtifactProber() *OverlayArtifactProber {
	return &OverlayArtifactProber{client: objectStoreHTTPClient}
}

// Present reports whether the artifact is still retrievable.
//
//	200           -> present
//	404/410       -> absent (a missing object is a cache miss, not an incident)
//	401/403       -> error: the store is not reachable with this credential, so
//	                 the cache cannot be judged and must not be invalidated
//	5xx / transport -> error, same reason
//
// Returning an error makes the caller render normally without evicting a
// possibly valid entry.
func (p *OverlayArtifactProber) Present(ctx context.Context, artifact scriptgen.RenderArtifact) (bool, error) {
	rawURL := strings.TrimSpace(artifact.URL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return false, fmt.Errorf("overlay artifact prober: %q is not an http(s) URL", rawURL)
	}
	client := p.client
	if client == nil {
		client = objectStoreHTTPClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return false, fmt.Errorf("overlay artifact prober: build request: %w", err)
	}
	setObjectStoreAuth(req)
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("overlay artifact prober: head %s: %w", artifact.SHA256, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusGone:
		return false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, fmt.Errorf("overlay artifact prober: store rejected the credential (HTTP %d)", resp.StatusCode)
	default:
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, fmt.Errorf("overlay artifact prober: head %s: HTTP %d", artifact.SHA256, resp.StatusCode)
	}
}

var _ scriptgen.OverlayArtifactProber = (*OverlayArtifactProber)(nil)
