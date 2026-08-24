// Package main — script_mode.go
//
// Script-mode runner (-mode=script): validates the canonical source set,
// builds the GenerationEnvelopeV2 payload, derives the deterministic
// reqID, and hands off to the shared submitAndPoll loop. Job-creation
// logic (payload + source map + clip parsing) lives here so main() stays
// a slim dispatcher.
package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	veloxclient "github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// runScriptMode implements -mode=script: POST /api/script/generate with
// GenerationEnvelopeV2 (source.type="text" or "clips"). Fail-closed
// validation mirrors the server-side validators so an invalid payload
// never burns a network round-trip (godlike/07).
func runScriptMode(cfg *workerConfig) {
	if cfg.Source != "text" && cfg.Source != "clips" {
		log.Fatalf("-source must be 'text' or 'clips' (godlike/06 one-source-type-per-item); got %q", cfg.Source)
	}
	if cfg.Source == "text" && strings.TrimSpace(cfg.Topic) == "" {
		log.Fatalf("-source=text requires non-empty -topic")
	}

	// Resolve clip_ids once: consumed by both the payload source map
	// (items[].source.clip_ids) and the deterministic reqID hash.
	// Empty + duplicate validation mirrors the server-side validator
	// in internal/kernel/script/generation_envelope.go
	// (validateGenerationSourceClips) — fail-closed here means we never
	// burn a network round-trip on a payload the server would reject,
	// and the operator sees the exact error before submission (godlike/07).
	var parsedClipIDs []string
	if cfg.Source == "clips" {
		parsedClipIDs = parseClipIDs(cfg.ClipIDs)
		if len(parsedClipIDs) == 0 {
			log.Fatalf("-source=clips requires non-empty -clip-ids (comma-separated clip ids); godlike/07 fail-closed")
		}
		seen := make(map[string]struct{}, len(parsedClipIDs))
		for _, id := range parsedClipIDs {
			if strings.TrimSpace(id) == "" {
				log.Fatalf("clip_ids cannot contain empty or whitespace-only values (godlike/07 fail-closed)")
			}
			if _, dup := seen[id]; dup {
				log.Fatalf("duplicate clip_id %q (validateGenerationSourceClips forbids dups in internal/kernel/script/generation_envelope.go)", id)
			}
			seen[id] = struct{}{}
		}
	}

	payload, reqID := buildScriptPayload(cfg, parsedClipIDs)

	log.Printf("worker starting endpoint=%s url=%s", scriptGenerateEndpoint, cfg.BaseURL)

	if cfg.DryRun {
		printDryRun(scriptGenerateEndpoint, cfg.BaseURL, reqID, payload)
		return
	}

	// SIGINT/SIGTERM cancels the long poll loop without orphaning the job
	// (the job keeps running server-side — that's the whole point of the
	// submit/poll separation).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cli := veloxclient.New(cfg.BaseURL, cfg.Token)
	submitAndPoll(ctx, cli, scriptGenerateEndpoint, payload, reqID, cfg.PollEvery, cfg.MaxWait, cfg.Verbose)
}

// buildScriptPayload assembles the GenerationEnvelopeV2 payload and the
// deterministic reqID for script mode.
//
// Payload — GenerationEnvelopeV2 canonical shape (version: 2).
// Source.type is one of: "text" (with the user's topic) or "clips"
// (with the user's -clip-ids, validated in runScriptMode). Output flags
// (generate_document, generate_scene_images, extract_entities)
// declare what the engine emits; the legacy `sentences_per_image`
// integer density is NOT a first-class V2 field — per-image density
// is owned by the scene-synthesis postprocessor and tuned
// post-submit. `script_params.target_words` controls narrative length.
// See architecture/capability_inventory for the postprocessor chain.
//
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
func buildScriptPayload(cfg *workerConfig, parsedClipIDs []string) (map[string]any, string) {
	itemID := fmt.Sprintf("integrate-%s", cfg.VideoName)
	payload := map[string]any{
		"version": 2,
		"preset":  "custom",
		"items": []map[string]any{
			{
				"id":       itemID,
				"title":    cfg.VideoName,
				"language": cfg.Language,
				"source":   buildSourceMap(cfg.Source, cfg.Topic, parsedClipIDs),
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

	reqIDParts := []string{itemID}
	switch cfg.Source {
	case "text":
		reqIDParts = append(reqIDParts, cfg.Topic)
	case "clips":
		sortedForHash := append([]string(nil), parsedClipIDs...)
		sort.Strings(sortedForHash)
		reqIDParts = append(reqIDParts, strings.Join(sortedForHash, ","))
	}
	reqIDParts = append(reqIDParts, cfg.Language)
	return payload, buildStableReqID(reqIDParts...)
}

// buildSourceMap assembles the `items[].source` map from CLI args.
// The num_clips field is derived from len(clipIDs) (godlike/06 SSOT:
// never duplicated from a separate CLI flag; the server-side validator
// requires clip_ids non-empty + no dups, and num_clips is informational
// downstream).
//
// For source.type="clips": grounding_policy and fallback_policy are
// set to the canonical clip-native defaults (clips_primary + strict)
// per internal/kernel/script/source_spec.go. The plan builder may
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
// internal/kernel/script/generation_envelope.go).
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
