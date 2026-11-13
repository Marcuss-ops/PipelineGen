package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// finalArtifact is the terminal receipt the Producer needs to fetch a final
// render. The artifact route is protected by the Master bearer; the SHA-256 is
// the receipt's identity, not a value derived from the downloaded bytes.
type finalArtifact struct {
	RemoteJobID string
	Status      string
	URL         string
	SHA256      string
}

type finalDownload struct {
	SizeBytes   int64
	SHA256      string
	ContentType string
}

// cmdDownloadFinal fetches the job document, extracts
// result.result.remote_final_job, downloads that artifact and verifies the
// declared SHA-256 before publishing the output file. It intentionally uses
// the admin job endpoint: the M2M status projection does not expose the
// artifact locator.
func cmdDownloadFinal(args []string) int {
	cfg := loadConfig()
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox download-final: %v\n", err)
		return exitUsage
	}
	cfg.json = fs.has("json")
	if fs.has("m2m") {
		fmt.Fprintln(os.Stderr, "velox download-final: --m2m cannot read the final-job receipt; use the admin token")
		return exitUsage
	}
	// The receipt path is admin-only even when the operator's shell has
	// VELOX_M2M=true set globally for submit/poll.
	cfg.m2m = false
	if token := strings.TrimSpace(os.Getenv("VELOX_ADMIN_TOKEN")); token != "" {
		cfg.token = token
	} else if token := strings.TrimSpace(os.Getenv("VELOX_WORKER_TOKEN")); token != "" {
		cfg.token = token
	}
	if len(fs.pos) == 0 {
		fmt.Fprintln(os.Stderr, "velox download-final: <job_id> is required")
		return exitUsage
	}
	jobID := fs.pos[0]
	out := fs.get("out", "")
	if out == "" {
		out = fs.get("o", "")
	}
	if out == "" && len(fs.pos) > 1 {
		out = fs.pos[1]
	}
	if out == "" {
		out = defaultFinalDownloadName(jobID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	raw, err := newClient(cfg).GetJobFull(ctx, jobID)
	if err != nil {
		if errors.Is(err, veloxclient.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "velox download-final: job %s not found\n", jobID)
			return exitMissing
		}
		fmt.Fprintf(os.Stderr, "velox download-final: read job %s: %v\n", jobID, err)
		return exitFailure
	}
	artifact, err := finalArtifactFromJob(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox download-final: %v\n", err)
		return exitFailure
	}
	token, _, err := resolveArtifactBearer(fs.get("token-file", ""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox download-final: %v\n", err)
		return exitFailure
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "velox download-final: no artifact bearer found; set VELOX_MASTER_ADMIN_TOKEN (or VELOX_ARTIFACT_TOKEN / --token-file)")
		return exitFailure
	}

	result, err := downloadFinalArtifact(ctx, artifact, token, out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox download-final: %v\n", err)
		return exitFailure
	}
	if cfg.json {
		return emitJSON(map[string]any{
			"job_id":        jobID,
			"remote_job_id": artifact.RemoteJobID,
			"file":          out,
			"size_bytes":    result.SizeBytes,
			"sha256":        result.SHA256,
			"content_type":  result.ContentType,
		})
	}
	remote := ""
	if artifact.RemoteJobID != "" {
		remote = " (remote " + artifact.RemoteJobID + ")"
	}
	fmt.Printf("downloaded final artifact %s%s → %s (%d bytes, sha256 %s)\n",
		jobID, remote, out, result.SizeBytes, result.SHA256)
	return exitOK
}

// finalArtifactFromJob looks for the canonical remote_final_job receipt first.
// The fallback exists for older receipts that stored the same artifact_url +
// sha256 pair one level up; it deliberately requires both fields so an image
// asset URL can never be mistaken for the final video.
func finalArtifactFromJob(raw []byte) (finalArtifact, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return finalArtifact{}, fmt.Errorf("decode job response: %w", err)
	}
	if node, ok := findMapByKey(doc, "remote_final_job"); ok {
		return finalArtifactFromMap(node)
	}
	if node, ok := findArtifactReceipt(doc); ok {
		return finalArtifactFromMap(node)
	}
	return finalArtifact{}, fmt.Errorf("job has no remote_final_job artifact; it is not a final job or the receipt is missing")
}

func finalArtifactFromMap(m map[string]any) (finalArtifact, error) {
	status := strings.ToUpper(strings.TrimSpace(stringValue(m["status"])))
	if status != "" && status != "SUCCEEDED" && status != "SUCCESS" && status != "COMPLETED" {
		return finalArtifact{}, fmt.Errorf("remote final job is %s; the artifact is not ready", status)
	}
	artifactURL := strings.TrimSpace(stringValue(m["artifact_url"]))
	artifactSHA := strings.TrimSpace(stringValue(m["sha256"]))
	if artifactURL == "" {
		return finalArtifact{}, fmt.Errorf("remote final job has no artifact_url")
	}
	if artifactSHA == "" {
		return finalArtifact{}, fmt.Errorf("remote final job has no sha256; refusing an unverified download")
	}
	if len(artifactSHA) != 64 {
		return finalArtifact{}, fmt.Errorf("remote final job has an invalid sha256 %q", artifactSHA)
	}
	if _, err := hex.DecodeString(artifactSHA); err != nil {
		return finalArtifact{}, fmt.Errorf("remote final job has an invalid sha256 %q", artifactSHA)
	}
	parsed, err := url.Parse(artifactURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return finalArtifact{}, fmt.Errorf("remote final job has an unsupported artifact_url %q", artifactURL)
	}
	return finalArtifact{
		RemoteJobID: strings.TrimSpace(stringValue(m["job_id"])),
		Status:      status,
		URL:         artifactURL,
		SHA256:      strings.ToLower(artifactSHA),
	}, nil
}

func findMapByKey(v any, key string) (map[string]any, bool) {
	switch node := v.(type) {
	case map[string]any:
		if child, ok := node[key]; ok {
			if m, ok := child.(map[string]any); ok {
				return m, true
			}
		}
		for _, child := range node {
			if m, ok := findMapByKey(child, key); ok {
				return m, true
			}
		}
	case []any:
		for _, child := range node {
			if m, ok := findMapByKey(child, key); ok {
				return m, true
			}
		}
	}
	return nil, false
}

func findArtifactReceipt(v any) (map[string]any, bool) {
	switch node := v.(type) {
	case map[string]any:
		if _, hasURL := node["artifact_url"]; hasURL {
			if _, hasSHA := node["sha256"]; hasSHA {
				return node, true
			}
		}
		for _, child := range node {
			if m, ok := findArtifactReceipt(child); ok {
				return m, true
			}
		}
	case []any:
		for _, child := range node {
			if m, ok := findArtifactReceipt(child); ok {
				return m, true
			}
		}
	}
	return nil, false
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// resolveArtifactBearer prefers an explicit token file, then the bearer the
// worker already uses for the Master, then the two deployment credential files.
// It never logs or returns the token in an error.
func resolveArtifactBearer(explicitFile string) (token, source string, err error) {
	if path := strings.TrimSpace(explicitFile); path != "" {
		token, err := readArtifactTokenFile(path)
		if err != nil {
			return "", "", fmt.Errorf("--token-file: %w", err)
		}
		return token, "file:" + path, nil
	}
	for _, key := range []string{"VELOX_ARTIFACT_TOKEN", "VELOX_MASTER_ADMIN_TOKEN", "VELOX_FINAL_JOB_M2M_SECRET"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, "env:" + key, nil
		}
	}
	var candidates []string
	for _, key := range []string{"VELOX_MASTER_TOKEN_FILE", "VELOX_FINAL_JOB_CREDENTIALS_FILE"} {
		if path := strings.TrimSpace(os.Getenv(key)); path != "" {
			candidates = append(candidates, path)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, "creator-77-master.env"))
	}
	for _, path := range candidates {
		value, readErr := readArtifactTokenFile(path)
		if readErr != nil {
			continue
		}
		return value, "file:" + path, nil
	}
	return "", "", nil
}

// readArtifactTokenFile accepts the deployment's KEY=VALUE env file and the
// simpler one-line token file. A single line that matched no known key is
// treated as a raw bearer so the operator can also use `openssl rand`-style
// token files without inventing variable names.
func readArtifactTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "VELOX_MASTER_ADMIN_TOKEN", "VELOX_ARTIFACT_TOKEN", "VELOX_M2M_SECRET", "VELOX_FINAL_JOB_M2M_SECRET":
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if value != "" {
				return value, nil
			}
		}
	}
	if !strings.Contains(text, "\n") {
		return text, nil
	}
	return "", fmt.Errorf("token file %s has no artifact bearer", path)
}

func downloadFinalArtifact(ctx context.Context, artifact finalArtifact, token, out string) (finalDownload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return finalDownload{}, fmt.Errorf("build artifact request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "video/mp4,application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return finalDownload{}, fmt.Errorf("download artifact: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return finalDownload{}, fmt.Errorf("artifact bearer rejected (HTTP %d): %s; set VELOX_MASTER_ADMIN_TOKEN to a token authorized by the Master", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return finalDownload{}, fmt.Errorf("download artifact: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	dir := filepath.Dir(out)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return finalDownload{}, fmt.Errorf("create output directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".velox-final-*.part")
	if err != nil {
		return finalDownload{}, fmt.Errorf("create staging file: %w", err)
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	hasher := digest.NewSHA256()
	written, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body)
	if err != nil {
		return finalDownload{}, fmt.Errorf("read artifact body: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return finalDownload{}, fmt.Errorf("sync artifact: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return finalDownload{}, fmt.Errorf("chmod artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return finalDownload{}, fmt.Errorf("close artifact: %w", err)
	}
	closed = true
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return finalDownload{}, fmt.Errorf("artifact size mismatch: got %d bytes, response declared %d", written, resp.ContentLength)
	}
	gotSHA := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(gotSHA, artifact.SHA256) {
		return finalDownload{}, fmt.Errorf("artifact sha256 mismatch: got %s, want %s", gotSHA, artifact.SHA256)
	}
	if err := os.Rename(tmpName, out); err != nil {
		return finalDownload{}, fmt.Errorf("publish %s: %w", out, err)
	}
	tmpName = ""
	return finalDownload{
		SizeBytes:   written,
		SHA256:      gotSHA,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

func defaultFinalDownloadName(jobID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, jobID)
	safe = strings.Trim(safe, "_")
	if safe == "" {
		safe = "job"
	}
	return "velox-final-" + safe + ".mp4"
}
