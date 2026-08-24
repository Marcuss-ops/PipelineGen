// cmd/admin/qdrant_preflight_stack.go — Stack-health preflight tests (Tests 1-2).
//
// Extracted from qdrant_preflight.go (PR-PREFLIGHT-STACK-SPLIT).
// These 2 tests verify the Qdrant stack is reachable and the canonical
// schema v3 + alias routing is in place.

package qdrant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// testQdrantStackHealthy (PR-QDRANT-PREFLIGHT-TEST-1): GET qdrant /healthz.
// Pass = 200; Fail = anything else OR connection error.
func testQdrantStackHealthy(ctx context.Context, deps *preflightDeps) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deps.QdrantURL+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPreflightStackDown, err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: GET /healthz: %w", ErrPreflightStackDown, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: GET /healthz => %d: %s", ErrPreflightStackDown, resp.StatusCode, string(body))
	}
	return nil
}

// testSchemaV3Shipped (PR-QDRANT-PREFLIGHT-TEST-2): GET /collections + /aliases.
//
// Canonical Qdrant 1.18+ /collections response shape:
//
//	{"result": {"collections": [{"name": "..."}], "aliases": {...}}, "status":"ok", "time":...}
//
// Aliases are sometimes embedded under result.aliases and sometimes served
// only via the separate /aliases endpoint (Qdrant kernel behavior varies by
// build); the runner does both calls and verifies the canonical alias
// `media_assets_current` routes to `deps.Collection` (i.e. media_assets_v3_e5_768_siglip_768,
// per AGENTS.md Qdrant Entity Associations table). godlike/07 NO-FAKE-AVAILABILITY:
// any drift (alias points to wrong collection) fails loud via typed sentinel
// ErrPreflightStackDown.
func testSchemaV3Shipped(ctx context.Context, deps *preflightDeps) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deps.QdrantURL+"/collections", nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPreflightStackDown, err)
	}
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: GET /collections: %w", ErrPreflightStackDown, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: schema-V3: GET /collections => %d", ErrPreflightStackDown, resp.StatusCode)
	}

	var apiResp struct {
		Result struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
			Aliases json.RawMessage `json:"aliases"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("schema-V3: decode /collections response: %w", err)
	}

	// (a) Canonical collection present?
	found := false
	for _, c := range apiResp.Result.Collections {
		if c.Name == deps.Collection {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("%w: schema-V3: collection %q not in /collections response", ErrPreflightStackDown, deps.Collection)
	}

	// (b) Canonical alias target. The /collections response may embed
	// aliases as null, an array, or a map (Qdrant kernel behavior varies
	// by build). Always use the separate /aliases endpoint which returns
	// the canonical Qdrant 1.18+ shape.
	target := ""
	aliasReq, err := http.NewRequestWithContext(ctx, http.MethodGet, deps.QdrantURL+"/aliases", nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPreflightStackDown, err)
	}
	aliasResp, err := deps.HTTPClient.Do(aliasReq)
	if err != nil {
		return fmt.Errorf("%w: GET /aliases: %w", ErrPreflightStackDown, err)
	}
	defer aliasResp.Body.Close()
	if aliasResp.StatusCode != http.StatusOK {
		return fmt.Errorf("schema-V3: GET /aliases => %d", aliasResp.StatusCode)
	}
	var aliasesResp struct {
		Result struct {
			Aliases []struct {
				AliasName      string `json:"alias_name"`
				CollectionName string `json:"collection_name"`
			} `json:"aliases"`
		} `json:"result"`
	}
	if err := json.NewDecoder(aliasResp.Body).Decode(&aliasesResp); err != nil {
		return fmt.Errorf("schema-V3: decode /aliases response: %w", err)
	}
	for _, a := range aliasesResp.Result.Aliases {
		if a.AliasName == "media_assets_current" {
			target = a.CollectionName
			break
		}
	}

	// (c) Canonical alias present?
	if target == "" {
		return fmt.Errorf("%w: schema-V3: canonical alias media_assets_current missing from /collections + /aliases responses", ErrPreflightStackDown)
	}

	// (d) Target==deps.Collection assertion. AGENTS.md Qdrant Entity Associations:
	// media_assets_current -> media_assets_v3_e5_768_siglip_768.
	if target != deps.Collection {
		return fmt.Errorf("%w: schema-V3: alias target drift: expected %q, got %q (canonical routing per AGENTS.md Qdrant Entity Associations table)", ErrPreflightStackDown, deps.Collection, target)
	}

	return nil
}
