package jobs

// m2m_artifact.go is the M2M half of final-render delivery:
//
//	GET /api/v1/jobs/:id/artifact
//
// A job submitted by a remote M2M client may end in a two-stage final render
// whose receipt lives in result.remote_final_job as {artifact_url, sha256}.
// Before this route the remote had to hold VELOX_ADMIN_TOKEN just to read the
// receipt and then the artifact URL's own Master bearer — an admin credential
// handed to a submitter. The route keeps the credential on the Master:
//
//   - the client authenticates with its scoped M2M key (jobs.read),
//   - the job must belong to that client (j.ClientID == client.ClientID),
//   - the Master extracts the receipt from the job result,
//   - the Master redeems its own artifact bearer against artifact_url and
//     streams the bytes (Range forwarded) back to the client.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	mwm2m "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/jsonutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// artifactHTTPClient streams large renders, so it has no overall timeout; it
// still fails fast when the remote master never answers, instead of parking a
// handler (and its concurrency slot) indefinitely on a broken URL.
var artifactHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// artifactReceipt is the subset of the remote_final_job receipt this route
// needs. Status is validated so a still-running render cannot be downloaded.
type artifactReceipt struct {
	JobID       string `json:"job_id"`
	Status      string `json:"status"`
	ArtifactURL string `json:"artifact_url"`
	SHA256      string `json:"sha256"`
}

// M2MArtifact streams the final render of a job owned by the authenticated M2M
// client. It never exposes artifact_url itself, only the bytes.
func (h *JobsHandler) M2MArtifact(c *gin.Context) {
	client, ok := m2mClient(c)
	if !ok {
		apiutil.Error(c, http.StatusUnauthorized, "M2M client identity is required")
		return
	}
	id := c.Param("id")
	j, err := h.service.Get(c.Request.Context(), id)
	if err != nil || j == nil || j.ClientID != client.ClientID {
		apiutil.NotFound(c, "job not found")
		return
	}

	receipt, err := artifactReceiptFromResult(j.Result)
	if err != nil {
		apiutil.Error(c, http.StatusUnprocessableEntity, err.Error())
		return
	}

	parsed, err := url.Parse(receipt.ArtifactURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		apiutil.Error(c, http.StatusBadGateway, "artifact locator is not a supported URL")
		return
	}
	bearer := strings.TrimSpace(os.Getenv("VELOX_ARTIFACT_TOKEN"))
	if bearer == "" {
		bearer = strings.TrimSpace(os.Getenv("VELOX_MASTER_ADMIN_TOKEN"))
	}
	if bearer == "" {
		h.log.Error("m2m artifact: no artifact bearer configured",
			zap.String("job_id", id))
		apiutil.Error(c, http.StatusServiceUnavailable, "artifact bearer is not configured on the master")
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, receipt.ArtifactURL, nil)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "video/mp4,application/octet-stream")
	if rng := strings.TrimSpace(c.GetHeader("Range")); rng != "" {
		req.Header.Set("Range", rng)
	}

	resp, err := artifactHTTPClient.Do(req)
	if err != nil {
		h.log.Error("m2m artifact: fetch failed", zap.String("job_id", id), zap.Error(err))
		apiutil.Error(c, http.StatusBadGateway, "artifact source is unreachable")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		if v := resp.Header.Get("Content-Range"); v != "" {
			c.Header("Content-Range", v)
		}
		c.Status(http.StatusRequestedRangeNotSatisfiable)
		return
	case http.StatusUnauthorized, http.StatusForbidden:
		h.log.Error("m2m artifact: master bearer rejected", zap.String("job_id", id), zap.Int("status", resp.StatusCode))
		apiutil.Error(c, http.StatusBadGateway, "artifact source rejected the master credential")
		return
	default:
		h.log.Warn("m2m artifact: artifact source returned error",
			zap.String("job_id", id), zap.Int("status", resp.StatusCode))
		apiutil.Error(c, http.StatusBadGateway, "artifact source returned an error")
		return
	}

	c.Header("Cache-Control", "private, no-store")
	c.Header("Accept-Ranges", "bytes")
	if ct := strings.TrimSpace(resp.Header.Get("Content-Type")); ct != "" {
		c.Header("Content-Type", ct)
	} else {
		c.Header("Content-Type", "application/octet-stream")
	}
	for _, name := range []string{"Content-Length", "Content-Range", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(name); v != "" {
			c.Header(name, v)
		}
	}
	if receipt.SHA256 != "" {
		c.Header("X-Content-SHA256", strings.ToLower(receipt.SHA256))
	}
	c.Header("X-Remote-Job-ID", receipt.JobID)
	c.Status(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		h.log.Debug("m2m artifact: stream interrupted", zap.String("job_id", id), zap.Error(err))
	}
}

// m2mClient reads the resolved client the auth middleware stored. It mirrors
// the lookup M2MGet/M2MCancel perform so all three routes share one rule.
func m2mClient(c *gin.Context) (*mwm2m.M2MClient, bool) {
	raw, exists := c.Get("m2m_client")
	if !exists || raw == nil {
		return nil, false
	}
	client, ok := raw.(*mwm2m.M2MClient)
	if !ok || client == nil || client.ClientID == "" {
		return nil, false
	}
	return client, true
}

// artifactReceiptFromResult finds the remote_final_job receipt anywhere in the
// persisted result JSON. It requires both artifact_url and sha256 so an image
// asset URL can never be mistaken for the final video.
func artifactReceiptFromResult(raw json.RawMessage) (artifactReceipt, error) {
	var doc any
	if len(raw) == 0 {
		return artifactReceipt{}, &artifactError{"job has no result"}
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return artifactReceipt{}, &artifactError{"job result is not valid JSON"}
	}
	// Prefer the canonical remote_final_job receipt; only then accept any
	// nested {artifact_url, sha256} pair (older receipts stored it one level
	// up). This mirrors the CLI's extraction order.
	node, ok := jsonutil.FindMapByKey(doc, "remote_final_job")
	if !ok {
		node, ok = findReceiptMap(doc)
	}
	if !ok {
		return artifactReceipt{}, &artifactError{"job has no final-render artifact receipt"}
	}
	receipt := artifactReceipt{
		JobID:       strings.TrimSpace(stringFromAny(node["job_id"])),
		Status:      strings.ToUpper(strings.TrimSpace(stringFromAny(node["status"]))),
		ArtifactURL: strings.TrimSpace(stringFromAny(node["artifact_url"])),
		SHA256:      strings.ToLower(strings.TrimSpace(stringFromAny(node["sha256"]))),
	}
	if receipt.Status != "" && receipt.Status != "SUCCEEDED" && receipt.Status != "SUCCESS" && receipt.Status != "COMPLETED" {
		return artifactReceipt{}, &artifactError{"final render is not complete: " + receipt.Status}
	}
	if receipt.ArtifactURL == "" {
		return artifactReceipt{}, &artifactError{"final-render receipt has no artifact_url"}
	}
	if receipt.SHA256 == "" {
		return artifactReceipt{}, &artifactError{"final-render receipt has no sha256"}
	}
	if len(receipt.SHA256) != 64 {
		return artifactReceipt{}, &artifactError{"final-render receipt has an invalid sha256"}
	}
	if _, err := hex.DecodeString(receipt.SHA256); err != nil {
		return artifactReceipt{}, &artifactError{"final-render receipt has an invalid sha256"}
	}
	return receipt, nil
}

type artifactError struct{ message string }

func (e *artifactError) Error() string { return e.message }

func findReceiptMap(v any) (map[string]any, bool) {
	switch node := v.(type) {
	case map[string]any:
		if _, hasURL := node["artifact_url"]; hasURL {
			if _, hasSHA := node["sha256"]; hasSHA {
				return node, true
			}
		}
		for _, child := range node {
			if m, ok := findReceiptMap(child); ok {
				return m, true
			}
		}
	case []any:
		for _, child := range node {
			if m, ok := findReceiptMap(child); ok {
				return m, true
			}
		}
	}
	return nil, false
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
