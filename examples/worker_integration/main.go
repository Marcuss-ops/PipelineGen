// Package main is a minimal reference worker that demonstrates calling
// pipelinegen from a separate process over the cross-worker veloxclient:
//
//  1. builds a deterministic X-Request-ID from the inputs (so a crashed
//     retry lands on the same server-side job via the
//     (type, correlation_id) UNIQUE constraint),
//  2. submits the job to /api/script/generate with GenerationEnvelopeV2
//     (source.type="text" + items[] with script_params + output flags),
//  3. polls until the job reaches a terminal state,
//  4. pretty-prints the result on stdout (or stderr on failure).
//
// Run it with:
//
//	go run ./examples/worker_integration \//
//	  -url "http://127.0.0.1:8080" \\\
//	  -token "$(cat ~/.config/pipelinegen/worker_token)" \\\
//	  -topic "the great barrier reef" \\\
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
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	veloxclient "github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// scriptGenerateEndpoint is the canonical submission route for all
// script-generation requests (text, clips, catalog, search sources).
// The legacy per-source endpoints (`/api/script/generate-with-images`,
// `/api/script/generate-from-clips`, `/api/script/generate-from-catalog`,
// `/api/script/curate`) are retired; clients flow through this single
// canonical endpoint with the GenerationEnvelopeV2 shape and select
// the source via `items[].source.type`. See architecture/current.yaml
// for the deprecation ticket.
const scriptGenerateEndpoint = "/api/script/generate"

func main() {
	var (
		baseURL   = flag.String("url", envDefault("PIPELINEGEN_URL", envDefault("VELOX_MASTER_URL", "http://127.0.0.1:8000")), "pipelinegen base URL (or env PIPELINEGEN_URL / VELOX_MASTER_URL)")
		token     = flag.String("token", os.Getenv("VELOX_WORKER_TOKEN"), "bearer token (or env VELOX_WORKER_TOKEN)")
		topic     = flag.String("topic", "the great barrier reef", "script topic (becomes items[].source.topic)")
		videoName = flag.String("video-name", "reef-documentary", "video name — used as items[].id (idempotency key) + items[].title")
		language  = flag.String("language", "en", "script output language (items[].language)")
		source    = flag.String("source", "text", "items[].source.type — 'text' (topic-driven) or 'clips' (media-clip-driven); godlike/06 canonical set")
		clipIDs   = flag.String("clip-ids", "", "comma-separated clip ids; REQUIRED iff -source=clips (fail-closed per godlike/07); example: yt_RRJvrDKunyA_32_37_v1,yt_RRJvrDKunyA_993_998_v1")
		pollEvery = flag.Duration("poll-every", 5*time.Second, "status poll interval")
		maxWait   = flag.Duration("max-wait", 30*time.Minute, "max wall-time before giving up on the job")
		dryRun    = flag.Bool("dry-run", false, "print the payload + reqID without calling the server")
		verbose   = flag.Bool("verbose", false, "print every poll result (default: only on status changes)")
	)
	flag.Parse()

	if strings.TrimSpace(*token) == "" {
		log.Fatal("-token (or VELOX_WORKER_TOKEN env var) is required")
	}
	if *source != "text" && *source != "clips" {
		log.Fatalf("-source must be 'text' or 'clips' (godlike/06 one-source-type-per-item); got %q", *source)
	}
	if *source == "text" && strings.TrimSpace(*topic) == "" {
		log.Fatalf("-source=text requires non-empty -topic")
	}

	// Resolve clip_ids once: consumed by both the payload source map
	// (items[].source.clip_ids) and the deterministic reqID hash.
	// Empty + duplicate validation mirrors the server-side validator
	// in internal/domain/script/generation_envelope.go
	// (validateGenerationSourceClips) — fail-closed here means we never
	// burn a network round-trip on a payload the server would reject,
	// and the operator sees the exact error before submission (godlike/07).
	var parsedClipIDs []string
	if *source == "clips" {
		parsedClipIDs = parseClipIDs(*clipIDs)
		if len(parsedClipIDs) == 0 {
			log.Fatalf("-source=clips requires non-empty -clip-ids (comma-separated clip ids); godlike/07 fail-closed")
		}
		seen := make(map[string]struct{}, len(parsedClipIDs))
		for _, id := range parsedClipIDs {
			if strings.TrimSpace(id) == "" {
				log.Fatalf("clip_ids cannot contain empty or whitespace-only values (godlike/07 fail-closed)")
			}
			if _, dup := seen[id]; dup {
				log.Fatalf("duplicate clip_id %q (validateGenerationSourceClips forbids dups in internal/domain/script/generation_envelope.go)", id)
			}
			seen[id] = struct{}{}
		}
	}

	// Payload — GenerationEnvelopeV2 canonical shape (version: 2).
	// Source.type is one of: "text" (with the user's topic) or "clips"
	// (with the user's -clip-ids, validated above). Output flags
	// (generate_document, generate_scene_images, extract_entities)
	// declare what the engine emits; the legacy `sentences_per_image`
	// integer density is NOT a first-class V2 field — per-image density
	// is owned by the scene-synthesis postprocessor and tuned
	// post-submit. `script_params.target_words` controls narrative length.
	// See architecture/capability_inventory for the postprocessor chain.
	itemID := fmt.Sprintf("integrate-%s", *videoName)
	payload := map[string]any{
		"version": 2,
		"preset":  "custom",
		"items": []map[string]any{
			{
				"id":       itemID,
				"title":    *videoName,
				"language": *language,
				"source":   buildSourceMap(*source, *topic, parsedClipIDs),
				"script_params": map[string]any{
					"target_words": 1500,
				},
				"output": map[string]any{
					"generate_document":     true,
					"generate_scene_images": true,
					"extract_entities":      true,
				},
			},
		},
	}

	// Deterministic reqID: same inputs => same job_id on the server, so a
	// worker restart that re-runs this exact command lands on the existing
	// job rather than spawning a duplicate. MD5 is fine here — the reqID is
	// not a security boundary (token check is); server's request-id
	// middleware accepts up to 64 alphanumeric chars and 32 hex fits
	// comfortably with room for human prefixes.
	//
	// V2 contract: items[].id is the canonical idempotency anchor for the
	// server-side (type, correlation_id) UNIQUE index; reqID is the
	// secondary job-creation anchor for cross-run dedup at the worker
	// layer. We derive reqID from `itemID + source_descriptor + language`
	// so that varying -topic (text) or reordering -clip-ids (clips)
	// generates a fresh job rather than colliding with the existing one.
	//
	// For source.type="clips" the source_descriptor is the canonical
	// sorted CSV of clip_ids for the reqID hash — sort order-invariant
	// so two operators who submit the same logical clip set in different
	// surface order (e.g. accidental comma-swap, alphabetized vs
	// chronological) collide on the same idempotency key, avoiding
	// wasted duplicate-server-jobs. The payload's items[].source.clip_ids
	// array preserves the user-supplied order untouched (narrative
	// intent is operator-set; the server-side planner consumes them in
	// the supplied order). This split-body-vs-canonicalized-hash is the
	// content-based-idempotency pattern from godlike/07 fail-closed +
	// godlike/06 SSOT (single canonical form per semantic quantity).
	reqIDParts := []string{itemID}
	switch *source {
	case "text":
		reqIDParts = append(reqIDParts, *topic)
	case "clips":
		sortedForHash := append([]string(nil), parsedClipIDs...)
		sort.Strings(sortedForHash)
		reqIDParts = append(reqIDParts, strings.Join(sortedForHash, ","))
	}
	reqIDParts = append(reqIDParts, *language)
	reqID := buildStableReqID(reqIDParts...)

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("worker starting endpoint=%s url=%s", scriptGenerateEndpoint, *baseURL)

	if *dryRun {
		out := map[string]any{
			"url":          strings.TrimRight(*baseURL, "/") + scriptGenerateEndpoint,
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

	// Default 30s submit/poll timeout is fine — /api/script/generate
	// typically returns within a few hundred ms even on the idempotency-hit
	// branch (existing job rows read fast).
	cli := veloxclient.New(*baseURL, *token)

	log.Printf("submitting job (reqID=%s)", reqID)
	resp, err := cli.SubmitAsync(ctx, scriptGenerateEndpoint, payload, reqID)
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

// buildSourceMap assembles the `items[].source` map from CLI args.
// The num_clips field is derived from len(clipIDs) (godlike/06 SSOT:
// never duplicated from a separate CLI flag; the server-side validator
// requires clip_ids non-empty + no dups, and num_clips is informational
// downstream).
//
// For source.type="clips": grounding_policy and fallback_policy are
// set to the canonical clip-native defaults (clips_primary + strict)
// per internal/domain/script/source_spec.go. The plan builder may
// refine these downstream based on evidence but the worker write-time
// defaults are godlike/06 SSOT single-canonical-owner-per-fact — no
// alternate leaf-pkg SSOT candidate lives in this file.
func buildSourceMap(source, topic string, clipIDs []string) map[string]any {
	switch source {
	case "text":
		return map[string]any{
			"type":  "text",
			"topic": topic,
		}
	case "clips":
		m := map[string]any{
			"type":     "clips",
			"clip_ids": clipIDs,
		}
		if n := len(clipIDs); n > 0 {
			m["num_clips"] = n
		}
		m["grounding_policy"] = "clips_primary"
		m["fallback_policy"] = "strict"
		return m
	default:
		// Unreachable — caller validates against the canonical set
		// (text|clips) above and Fatal-closes on mismatch. panic is
		// the canonical Go pattern for genuinely-unreachable branches
		// in the PipelineGen codebase (godlike/07 fail-closed: surface
		// the violation loudly rather than returning a malformed map).
		panic(fmt.Sprintf("buildSourceMap: unknown source %q (godlike/06 canonical set: text|clips)", source))
	}
}

// parseClipIDs splits a CSV of clip ids, trims whitespace, and drops
// empty segments. Duplicates are NOT removed here — the caller validates
// against the server-side identity rule (no duplicates allowed;
// see validateGenerationSourceClips in
// internal/domain/script/generation_envelope.go).
func parseClipIDs(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
