package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// endpointAliases maps the short job names used by operators to the canonical
// async endpoints. Keeping the mapping here (not in a shell heredoc) means a
// typo is a usage error instead of a request to the wrong path.
var endpointAliases = map[string]string{
	"clips-process":   veloxclient.RouteClipsProcess,
	"clips-render":    veloxclient.RouteClipsRender,
	"render-batch":    veloxclient.RouteClipsRenderBatch,
	"script-generate": veloxclient.RouteScriptGenerate,
	"jobs":            veloxclient.RouteJobsEnqueue,
}

// ansiPattern matches ANSI escape sequences. A body that carries them breaks
// downstream `jq` parsing, so the CLI strips them before submitting.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func newClient(cfg config) *veloxclient.Client {
	return veloxclient.New(cfg.baseURL, cfg.token)
}

// cleanPayload removes CR and ANSI escapes and verifies the result is valid
// JSON, so a body that would make jq fail is rejected with a clear message
// rather than submitted and diagnosed later.
func cleanPayload(raw []byte) ([]byte, error) {
	cleaned := ansiPattern.ReplaceAll(raw, nil)
	cleaned = bytesReplaceCR(cleaned)
	if !json.Valid(cleaned) {
		return nil, fmt.Errorf("payload is not valid JSON after cleaning (check quoting)")
	}
	return cleaned, nil
}

func bytesReplaceCR(b []byte) []byte {
	out := b[:0:0]
	for _, c := range b {
		if c == '\r' {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ── submit ────────────────────────────────────────────────────────────

func cmdSubmit(args []string) int {
	cfg := loadConfig()
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox submit: %v\n", err)
		return exitUsage
	}
	cfg.json = fs.has("json")

	endpoint := fs.get("path", "")
	if endpoint == "" {
		if len(fs.pos) == 0 {
			fmt.Fprintln(os.Stderr, "velox submit: <job-alias> or --path is required")
			return exitUsage
		}
		alias := fs.pos[0]
		resolved, ok := endpointAliases[alias]
		if !ok {
			fmt.Fprintf(os.Stderr, "velox submit: unknown job alias %q (known: %s)\n", alias, strings.Join(aliasNames(), ", "))
			return exitUsage
		}
		endpoint = resolved
	}
	project := fs.get("key", "")
	if project == "" {
		fmt.Fprintln(os.Stderr, "velox submit: --key PROJECT is required (it is the idempotency namespace)")
		return exitUsage
	}
	payloadPath := fs.get("payload", "")
	if payloadPath == "" {
		fmt.Fprintln(os.Stderr, "velox submit: --payload FILE is required")
		return exitUsage
	}
	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox submit: read payload: %v\n", err)
		return exitUsage
	}
	payload, err := cleanPayload(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox submit: %v\n", err)
		return exitUsage
	}

	key := fs.get("idem-key", "")
	if key == "" {
		key = idempotencyKey(project, payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := newClient(cfg).SubmitBytes(ctx, endpoint, payload, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox submit: %v\n", err)
		return exitFailure
	}

	absPath, _ := filepath.Abs(payloadPath)
	rec := jobRecord{
		JobID:          resp.JobID,
		Endpoint:       endpoint,
		Project:        project,
		IdempotencyKey: key,
		PayloadPath:    absPath,
		PayloadSHA256:  payloadSHA256(payload),
		CreatedAt:      time.Now().UTC(),
		LastStatus:     resp.Status,
	}
	if resp.JobID != "" {
		st, serr := OpenStore(cfg.storePath)
		if serr != nil {
			fmt.Fprintf(os.Stderr, "velox submit: warning: job store: %v\n", serr)
		} else if perr := st.Put(rec); perr != nil {
			fmt.Fprintf(os.Stderr, "velox submit: warning: persist job: %v\n", perr)
		}
	}

	if cfg.json {
		return emitJSON(map[string]any{
			"job_id":          resp.JobID,
			"status":          resp.Status,
			"endpoint":        endpoint,
			"idempotency_key": key,
			"store":           cfg.storePath,
		})
	}
	fmt.Printf("job_id=%s status=%s endpoint=%s idempotency_key=%s\n", resp.JobID, resp.Status, endpoint, key)
	if resp.JobID == "" {
		fmt.Fprintln(os.Stderr, "velox submit: NOTE server returned no job_id (legacy ACK); poll by listing jobs")
	}
	return exitOK
}

// ── poll ──────────────────────────────────────────────────────────────

func cmdPoll(args []string) int {
	cfg := loadConfig()
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox poll: %v\n", err)
		return exitUsage
	}
	cfg.json = fs.has("json")
	if len(fs.pos) == 0 {
		fmt.Fprintln(os.Stderr, "velox poll: <job_id> is required")
		return exitUsage
	}
	jobID := fs.pos[0]

	timeout, err := time.ParseDuration(fs.get("timeout", "30m"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox poll: invalid --timeout: %v\n", err)
		return exitUsage
	}
	interval, err := time.ParseDuration(fs.get("interval", "3s"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox poll: invalid --interval: %v\n", err)
		return exitUsage
	}

	deadline := time.Now().Add(timeout)
	client := newClient(cfg)
	prev := ""
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		status, gerr := client.GetJobStatus(ctx, jobID)
		cancel()
		if gerr != nil {
			if errors.Is(gerr, veloxclient.ErrNotFound) {
				fmt.Fprintf(os.Stderr, "velox poll: job %s not found\n", jobID)
				return exitMissing
			}
			fmt.Fprintf(os.Stderr, "velox poll: %v\n", gerr)
			return exitFailure
		}
		if status.Status != prev {
			fmt.Printf("%s status=%s progress=%d%%\n", jobID, status.Status, status.Progress)
			prev = status.Status
		}
		if terminal, success := veloxclient.IsTerminalStatus(status.Status); terminal {
			persistStatus(cfg.storePath, jobID, status.Status)
			if cfg.json {
				return emitJSON(map[string]any{
					"job_id": jobID, "status": status.Status, "success": success, "error": status.Error,
				})
			}
			if !success {
				fmt.Fprintf(os.Stderr, "velox poll: job %s finished as %s", jobID, status.Status)
				if status.Error != "" {
					fmt.Fprintf(os.Stderr, ": %s", status.Error)
				}
				fmt.Fprintln(os.Stderr)
				return exitFailure
			}
			fmt.Printf("%s SUCCEEDED\n", jobID)
			return exitOK
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "velox poll: timed out after %s (last status=%s)\n", timeout, status.Status)
			return exitFailure
		}
		time.Sleep(interval)
	}
}

func persistStatus(storePath, jobID, status string) {
	st, err := OpenStore(storePath)
	if err != nil {
		return
	}
	_ = st.UpdateStatus(jobID, status)
}

// ── replay ────────────────────────────────────────────────────────────

func cmdReplay(args []string) int {
	cfg := loadConfig()
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox replay: %v\n", err)
		return exitUsage
	}
	cfg.json = fs.has("json")
	if len(fs.pos) == 0 {
		fmt.Fprintln(os.Stderr, "velox replay: <job_id> is required")
		return exitUsage
	}
	jobID := fs.pos[0]

	st, err := OpenStore(cfg.storePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox replay: %v\n", err)
		return exitFailure
	}
	rec, ok := st.Get(jobID)
	if !ok {
		fmt.Fprintf(os.Stderr, "velox replay: no stored record for %s (only jobs submitted through velox can be replayed)\n", jobID)
		return exitMissing
	}
	raw, err := os.ReadFile(rec.PayloadPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox replay: read stored payload %s: %v\n", rec.PayloadPath, err)
		return exitFailure
	}
	payload, err := cleanPayload(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox replay: %v\n", err)
		return exitUsage
	}
	if got := payloadSHA256(payload); got != rec.PayloadSHA256 {
		fmt.Fprintf(os.Stderr, "velox replay: WARNING payload %s changed since submission; the same Idempotency-Key now covers different bytes\n", rec.PayloadPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The SAME key as the original submission: the server replays the original
	// job instead of creating a second one.
	resp, err := newClient(cfg).SubmitBytes(ctx, rec.Endpoint, payload, rec.IdempotencyKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox replay: %v\n", err)
		return exitFailure
	}
	if cfg.json {
		return emitJSON(map[string]any{
			"job_id": resp.JobID, "status": resp.Status,
			"endpoint": rec.Endpoint, "idempotency_key": rec.IdempotencyKey, "replayed": true,
		})
	}
	fmt.Printf("job_id=%s status=%s endpoint=%s idempotency_key=%s (reused)\n",
		resp.JobID, resp.Status, rec.Endpoint, rec.IdempotencyKey)
	return exitOK
}

// ── download ──────────────────────────────────────────────────────────

func cmdDownload(args []string) int {
	cfg := loadConfig()
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox download: %v\n", err)
		return exitUsage
	}
	if len(fs.pos) == 0 {
		fmt.Fprintln(os.Stderr, "velox download: <asset_id> is required")
		return exitUsage
	}
	assetID := fs.pos[0]
	source := fs.get("source", defaultSourceFor(assetID))
	out := fs.get("out", "")
	if out == "" {
		out = fs.get("o", "")
	}
	if out == "" {
		out = defaultDownloadName(assetID)
	}

	endpoint := veloxclient.RouteClipsDownload(source, assetID)
	client := newClient(cfg)

	// The download endpoint is POST-only; PostToWriter encodes that so a GET
	// (which the server answers with 404) is not even possible from here.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var contentType string
	writeErr := atomicwrite.Write(out, 0o644, func(w io.Writer) error {
		ct, perr := client.PostToWriter(ctx, endpoint, map[string]any{}, w)
		contentType = ct
		return perr
	})
	if writeErr != nil {
		switch {
		case errors.Is(writeErr, veloxclient.ErrNotReady):
			fmt.Fprintf(os.Stderr, "velox download: asset %s is not ready yet (job still running); retry shortly\n", assetID)
			return exitFailure
		case errors.Is(writeErr, veloxclient.ErrNotFound):
			fmt.Fprintf(os.Stderr, "velox download: asset %s not found (%s)\n", assetID, endpoint)
			return exitMissing
		default:
			fmt.Fprintf(os.Stderr, "velox download: %v\n", writeErr)
			return exitFailure
		}
	}
	info, _ := os.Stat(out)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	fmt.Printf("downloaded %s → %s (%d bytes, %s)\n", assetID, out, size, contentType)
	return exitOK
}

// defaultSourceFor infers the clip source from the asset id prefix. YouTube
// clips are canonically `yt_*`; anything else is presumed local.
func defaultSourceFor(assetID string) string {
	if strings.HasPrefix(assetID, "yt_") {
		return "youtube"
	}
	if strings.HasPrefix(assetID, "vo_") || strings.HasPrefix(assetID, "voiceover") {
		return "voiceover"
	}
	return "local"
}

func defaultDownloadName(assetID string) string {
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, assetID)
	return safe + ".mp4"
}

// ── search ────────────────────────────────────────────────────────────

func cmdSearch(args []string) int {
	cfg := loadConfig()
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox search: %v\n", err)
		return exitUsage
	}
	cfg.json = fs.has("json")
	if len(fs.pos) == 0 {
		fmt.Fprintln(os.Stderr, "velox search: <query> is required")
		return exitUsage
	}
	query := strings.Join(fs.pos, " ")

	filters := map[string]any{}
	if src := fs.get("source", ""); src != "" {
		filters["source"] = src
	}
	// Taxonomy flags select the asset FAMILY / usage intent, which provenance
	// cannot express: a stock clip acquired from YouTube is source=youtube with
	// asset_kind=stock_video. Passing --kind keeps that distinction on the wire
	// instead of leaving the operator to filter asset ids by hand.
	if kind := fs.get("kind", ""); kind != "" {
		filters["asset_kind"] = kind
	}
	if role := fs.get("role", ""); role != "" {
		filters["semantic_role"] = role
	}
	body := map[string]any{"query": query, "limit": attrInt(fs.get("limit", ""), 20)}
	if src := fs.get("source", ""); src != "" {
		// The source list is sent alongside the structured filter so the
		// server enforces the constraint on both axes (see the aggregator).
		body["sources"] = []string{src}
	}
	if len(filters) > 0 {
		body["filters"] = filters
	}
	if u := fs.get("universe", ""); u != "" {
		body["universe"] = u
	}
	if m := fs.get("mode", ""); m != "" {
		body["mode"] = m
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	raw, err := newClient(cfg).PostJSON(ctx, veloxclient.RouteMediaSearch, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox search: %v\n", err)
		return exitFailure
	}
	if cfg.json {
		os.Stdout.Write(raw)
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			fmt.Println()
		}
		return exitOK
	}		var envelope struct {
		Items []struct {
			AssetID      string  `json:"asset_id"`
			Source       string  `json:"source"`
			AssetKind    string  `json:"asset_kind"`
			SemanticRole string  `json:"semantic_role"`
			Title        string  `json:"title"`
			MediaType    string  `json:"media_type"`
			Score        float64 `json:"score"`
		} `json:"items"`
		Partial        bool              `json:"partial"`
		ProviderErrors map[string]string `json:"provider_errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		fmt.Fprintf(os.Stderr, "velox search: decode response: %v\n", err)
		return exitFailure
	}
	for _, it := range envelope.Items {
		// asset_kind is printed next to source so a stock clip acquired from
		// YouTube is visibly distinct from a YouTube-native clip; provenance
		// alone cannot tell them apart.
		fmt.Printf("%-6.3f  %-10s  %-14s  %-28s  %s\n", it.Score, it.Source, it.AssetKind, it.AssetID, it.Title)
	}
	fmt.Printf("%d item(s)%s\n", len(envelope.Items), partialNote(envelope.Partial, envelope.ProviderErrors))
	return exitOK
}

func partialNote(partial bool, errs map[string]string) string {
	if !partial {
		return ""
	}
	parts := make([]string, 0, len(errs))
	for k, v := range errs {
		parts = append(parts, k+": "+v)
	}
	return " [partial: " + strings.Join(parts, "; ") + "]"
}

// ── jobs ──────────────────────────────────────────────────────────────

func cmdJobs(args []string) int {
	cfg := loadConfig()
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox jobs: %v\n", err)
		return exitUsage
	}
	cfg.json = fs.has("json")
	st, err := OpenStore(cfg.storePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox jobs: %v\n", err)
		return exitFailure
	}
	jobs := st.List()
	if cfg.json {
		return emitJSON(jobs)
	}
	if len(jobs) == 0 {
		fmt.Printf("no jobs recorded in %s\n", cfg.storePath)
		return exitOK
	}
	for _, j := range jobs {
		fmt.Printf("%s  %-14s  %-12s  %s  %s\n",
			j.CreatedAt.Format(time.RFC3339), j.LastStatus, j.Project, j.JobID, j.Endpoint)
	}
	return exitOK
}

func aliasNames() []string {
	out := make([]string, 0, len(endpointAliases))
	for k := range endpointAliases {
		out = append(out, k)
	}
	return out
}

func emitJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "velox: encode output: %v\n", err)
		return exitFailure
	}
	return exitOK
}
