// Package main is a minimal reference worker that demonstrates calling
// pipelinegen from a separate process over the cross-worker veloxclient:
//
//  1. builds a deterministic X-Request-ID from the inputs (so a crashed
//     retry lands on the same server-side job via the
//     (type, correlation_id) UNIQUE constraint),
//  2. submits the job to /api/script/generate-with-images,
//  3. polls until the job reaches a terminal state,
//  4. pretty-prints the result on stdout (or stderr on failure).
//
// Run it with:
//
//	go run ./examples/worker_integration \
//	  -url "http://127.0.0.1:18080" \
//	  -token "$(cat ~/.config/pipelinegen/worker_token)" \
//	  -topic "the great barrier reef" \
//	  -video-name reef-documentary -language en
//
// Or set VELOX_WORKER_TOKEN and PIPELINEGEN_URL env vars. Use -dry-run to
// inspect the payload + derived reqID without contacting the server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

func main() {
	var (
		baseURL    = flag.String("url", envDefault("PIPELINEGEN_URL", "http://127.0.0.1:18080"), "pipelinegen base URL")
		token      = flag.String("token", os.Getenv("VELOX_WORKER_TOKEN"), "bearer token (or env VELOX_WORKER_TOKEN)")
		topic      = flag.String("topic", "the great barrier reef", "script topic")
		videoName  = flag.String("video-name", "reef-documentary", "video name used to namespace the script")
		language   = flag.String("language", "en", "script output language")
		sentences  = flag.Int("sentences-per-image", 8, "sentences per scene image (default 8 matches /api/script/generate-with-images)")
		endpoint   = flag.String("endpoint", "/api/script/generate-with-images", "pipelinegen endpoint path")
		pollEvery  = flag.Duration("poll-every", 5*time.Second, "status poll interval")
		maxWait    = flag.Duration("max-wait", 30*time.Minute, "max wall-time before giving up on the job")
		dryRun     = flag.Bool("dry-run", false, "print the payload + reqID without calling the server")
		verbose    = flag.Bool("verbose", false, "print every poll result (default: only on status changes)")
	)
	flag.Parse()

	if strings.TrimSpace(*token) == "" {
		log.Fatal("-token (or VELOX_WORKER_TOKEN env var) is required")
	}

	// Payload — minimal shape that /api/script/generate-with-images accepts.
	// The endpoint forces extract_entities=false, generate_scene_images=true,
	// generate_metadata=false regardless of what we send; the only fields the
	// handler actually reads are video_name, language, sentences_per_image.
	// Note: the handler expects `topic` (singular string) — NOT `topics`
	// (array, which the legacy clip-extraction path accepted). Use the
	// singular form; the handler rejects `topics` with HTTP 400.
	payload := map[string]any{
		"project":             "integration-demo",
		"video_name":          *videoName,
		"topic":               *topic,
		"language":            *language,
		"sentences_per_image": *sentences,
	}

	// Deterministic reqID: same inputs => same job_id on the server, so a
	// worker restart that re-runs this exact command lands on the existing
	// job rather than spawning a duplicate. MD5 is fine here — the reqID is
	// not a security boundary (token check is); server's request-id
	// middleware accepts up to 64 alphanumeric chars and 32 hex fits
	// comfortably with room for human prefixes.
	reqID := buildStableReqID(*topic, *videoName, *language)

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("worker starting endpoint=%s url=%s", *endpoint, *baseURL)

	if *dryRun {
		out := map[string]any{
			"url":         strings.TrimRight(*baseURL, "/") + "/" + strings.TrimLeft(*endpoint, "/"),
			"x_request_id": reqID,
			"payload":      payload,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		log.Printf("(dry-run: nothing was submitted; rerun without -dry-run to dispatch)")
		return
	}

	// SIGINT/SIGTERM cancels the long poll loop without orphaning the job
	// (the job keeps running server-side — that's the whole point of the
	// submit/poll separation).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Default 30s submit/poll timeout is fine — /api/script/generate-with-images
	// typically returns within a few hundred ms even on the idempotency-hit
	// branch (existing job rows read fast).
	cli := veloxclient.New(*baseURL, *token)

	log.Printf("submitting job (reqID=%s)", reqID)
	resp, err := cli.SubmitAsync(ctx, *endpoint, payload, reqID)
	if err != nil {
		// SubmitAsync already wraps ErrUnauthorized/ErrBadRequest/ErrServer
		// with credential-redacted bodies — surface directly to operator.
		log.Fatalf("submit failed: %v", err)
	}
	log.Printf("job accepted: job_id=%s status=%s", resp.JobID, resp.Status)

	// Poll until terminal state. Tolerate transient ErrServer (network
	// hiccups mid-poll) by logging + looping; only a StatusFailed result is
	// fatal.
	deadline := time.Now().Add(*maxWait)
	lastStatus := ""
	for {
		select {
		case <-ctx.Done():
			log.Fatalf("interrupted before terminal state (job %s still running on server): %v", resp.JobID, ctx.Err())
		case <-time.After(*pollEvery):
		}
		if time.Now().After(deadline) {
			log.Fatalf("timed out after %s waiting for job %s", *maxWait, resp.JobID)
		}

		st, err := cli.GetJobStatus(ctx, resp.JobID)
		if err != nil {
			if errors.Is(err, veloxclient.ErrNotFound) {
				// Job created seconds ago and already 404 — would be a bug.
				log.Fatalf("job %s vanished (404): %v", resp.JobID, err)
			}
			log.Printf("poll transient error (will retry): %v", err)
			continue
		}
		if *verbose || st.Status != lastStatus {
			log.Printf("poll: id=%s status=%s progress=%d%%", st.ID, st.Status, st.Progress)
			lastStatus = st.Status
		}
		if veloxclient.IsTerminal(st.Status) {
			renderResult(resp.JobID, st)
			if st.Status == veloxclient.StatusFailed {
				os.Exit(2)
			}
			return
		}
	}
}

// buildStableReqID returns a 32-char hex reqID derived from the inputs.
// The caller MUST keep inputs stable across retries; the server enforces
// (type, correlation_id) UNIQUE and returns the existing job on collision
// — the network only sees a single job_id no matter how many times the
// worker process is restarted with the same arguments.
func buildStableReqID(parts ...string) string {
	canonical := strings.Join(parts, "|")
	return hashutil.MD5String(canonical) // 32 hex chars; server accepts up to 64
}

// renderResult pretty-prints the final job status. Success → stdout, fail
// → stderr + non-zero exit. The Result field is the pipelinegen-defined
// payload map (script, scenes, voiceover paths, etc.).
func renderResult(jobID string, st *veloxclient.JobStatusResponse) {
	log.Printf("job %s reached terminal status=%s progress=%d%%",
		jobID, st.Status, st.Progress)
	if st.Status == veloxclient.StatusFailed {
		// Failure — likely has an `error_message` / `last_error` key in
		// Result. Surface on stderr so scripts can grep.
		enc := json.NewEncoder(os.Stderr)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"job_id": jobID,
			"failed": true,
			"detail": st.Result,
		})
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(st)
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
