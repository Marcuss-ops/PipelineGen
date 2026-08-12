// Package testutil provides hermetic test helpers for Qdrant integration
// tests. Each helper creates a fresh collection with a unique name, runs
// the test against it, and deletes the collection on cleanup — so tests
// don't depend on pre-existing state in the Qdrant instance.
//
// godlike/07 NO-FAKE-AVAILABILITY: every helper probes Qdrant health
// before creating a collection. If Qdrant is not reachable the test is
// SKIPPED (t.Skip), not silently failed or faked.
//
// Usage:
//
//	client := transport.NewClient(&schema.Config{BaseURL: "http://localhost:6333"}, zap.NewNop())
//	schema := schema.DefaultV3Schema()
//	testutil.WithHermeticCollection(t, client, schema,
//	    func(ctx context.Context, name string, cm *collections.CollectionManager) {
//	        // test code that reads/writes the collection named "name"
//	    })
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
)

// WithHermeticCollection creates a unique Qdrant collection, calls fn
// with the collection name and a CollectionManager, then deletes the
// collection on cleanup. If Qdrant is not reachable the test is
// skipped via t.Skip (godlike/07 NO-FAKE-AVAILABILITY).
//
// The collection name is derived from t.Name() with a random 8-char
// hex suffix to prevent collisions between parallel test runs (e.g.
// go test -count=N). Slashes in t.Name() are replaced with dashes.
//
// The schema is validated before use. Its RuntimeAlias and
// PhysicalName are overridden with the generated name (the schema
// values are preserved for vector/sparse/payload-index config).
func WithHermeticCollection(
	t testing.TB,
	client *transport.Client,
	idxSchema *schema.IndexSchema,
	fn func(ctx context.Context, collectionName string, cm *collections.CollectionManager),
) {
	t.Helper()

	if err := idxSchema.Validate(); err != nil {
		t.Fatalf("hermetic: invalid schema: %v", err)
	}

	// Probe Qdrant health. t.Skip if unreachable — the test is
	// opt-in (godlike/07 NO-FAKE-AVAILABILITY).
	if err := probeQdrant(client); err != nil {
		t.Skipf("hermetic: Qdrant not reachable at %s: %v", client.BaseURL(), err)
	}

	name := collectionName(t)
	updatedSchema := cloneSchema(idxSchema, name)

	cm := collections.NewCollectionManager(client, updatedSchema, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create the physical collection + payload indexes.
	if err := cm.CreateCollection(ctx, name); err != nil {
		t.Fatalf("hermetic: CreateCollection(%q): %v", name, err)
	}

	// Teardown (synchronous): delete the collection AFTER the
	// callback returns — even on panic so stale collections don't
	// accumulate in the Qdrant instance. Using `defer` instead of
	// t.Cleanup ties the delete to the helper's own return path,
	// so callers can synchronously observe the post-callback
	// "collection deleted" flag (and so a CreateCollection failure
	// aborts before scheduling a delete). `defer` still runs on
	// panic (just like t.Cleanup), so the contract is preserved.
	defer func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanCancel()
		if err := client.DeleteCollection(cleanCtx, name); err != nil {
			t.Logf("hermetic: DeleteCollection(%q) on cleanup: %v (may need manual deletion)", name, err)
		}
	}()

	fn(ctx, name, cm)
}

// ── helpers ──────────────────────────────────────────────────────────

// collectionName derives a unique, collision-safe collection name from
// t.Name(). Slashes are replaced with dashes; non-printable characters
// are stripped. An 8-char random hex suffix is appended for uniqueness
// across parallel or repeated test runs.
func collectionName(t testing.TB) string {
	t.Helper()

	var b strings.Builder
	b.WriteString("test-")
	for _, r := range t.Name() {
		if r == '/' {
			b.WriteByte('-')
		} else if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	b.WriteByte('-')

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		// crypto/rand.Read failures are essentially impossible in
		// practice (only empty buffer or /dev/urandom failure).
		// Fallback: use nanosecond timestamp as raw bytes.
		ns := time.Now().UnixNano()
		suffix[0] = byte(ns >> 56)
		suffix[1] = byte(ns >> 48)
		suffix[2] = byte(ns >> 40)
		suffix[3] = byte(ns >> 32)
	}
	b.WriteString(hex.EncodeToString(suffix))

	return b.String()
}

// probeQdrant does a lightweight health check against the Qdrant REST
// API. Qdrant's /telemetry endpoint is a cheap GET that returns 200 if
// the node is alive. Any non-2xx status (including 401/403 API-key
// required and 404 not-found) is treated as a probe failure.
func probeQdrant(client *transport.Client) error {
	// Use the low-level HTTP client so we don't couple to the
	// transport.Client method set (which may evolve). A simple GET
	// to <baseURL>/telemetry tells us whether Qdrant is alive.
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		strings.TrimRight(client.BaseURL(), "/")+"/telemetry",
		nil,
	)
	if err != nil {
		return err
	}
	// We use a fresh http.Client with a short timeout to avoid
	// hanging if Qdrant is completely down. The transport.Client's
	// http.Client field is unexported, so we construct our own.
	hc := &http.Client{Timeout: 2 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Qdrant returned %d", resp.StatusCode)
	}
	return nil
}

// cloneSchema returns a shallow copy of src with PhysicalName and
// RuntimeAlias both set to name. The original schema's vector/sparse/
// payload-index config is preserved.
func cloneSchema(src *schema.IndexSchema, name string) *schema.IndexSchema {
	dst := *src // shallow copy
	dst.PhysicalName = name
	dst.RuntimeAlias = name
	return &dst
}
