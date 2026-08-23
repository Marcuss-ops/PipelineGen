// client_aliases.go — /collections/*/aliases and /collections/aliases REST surface.
//
// PR2 mechanical split (June 2026): relocated from client.go without
// signature or behaviour changes. GetAliasTarget's wire contract fix
// (decode `result.aliases[]` instead of the legacy `result[]`) is the
// canonical PR1 fix/qdrant-wire-contracts change; pre-PR1 it silently
// returned empty whenever the alias actually existed.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetAliasTarget returns the collection name an alias points to, or empty string.
//
// PR1 — fix/qdrant-wire-contracts: the Qdrant /collections/{alias}/aliases
// endpoint returns the canonical `{"result": {"aliases": [{alias_name,
// collection_name}, ...]}}` envelope (see https://api.qdrant.tech/api-reference/aliases/get-collection-aliases).
// The pre-PR1 decoder treated `result` as a top-level array (the
// /collections output shape, not /aliases), silently returning empty
// whenever the alias actually existed. The fix has two pieces:
//
//  1. Decode `result.aliases[]` instead of `result[]` here.
//  2. Accept the legacy flat shape during the migration window so
//     callers using cached/raw payloads are not broken — controlled
//     by aliasesEnv.probeShape envelope detector.
func (c *Client) GetAliasTarget(ctx context.Context, alias string) (string, error) {
	// Use the global /aliases endpoint because /collections/{alias}/aliases
	// is designed for physical collection names — it returns empty when the
	// parameter is itself an alias rather than a concrete collection.
	// PR-ALIAS-RESOLVE-FIX (2026-07-04): verified on Qdrant v1.x.
	url := fmt.Sprintf("%s/aliases", c.baseURL)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", &ErrCollectionNotFound{Name: alias}
	}
	if resp.StatusCode != http.StatusOK {
		return "", c.parseErrorWith("GetAliasTarget", resp)
	}

	type aliasEntry struct {
		AliasName      string `json:"alias_name"`
		CollectionName string `json:"collection_name"`
	}

	// Re-read the body so we can probe the envelope before decoding.
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxAPIBodyBytes))
	var env struct {
		Result struct {
			Aliases []aliasEntry `json:"aliases"`
		} `json:"result"`
	}
	// PR 14 (June 2026): removed the len(env.Result.Aliases) > 0 guard.
	// When the canonical envelope decode succeeds (err == nil), we know
	// the response is the {"result": {"aliases": [...]}} shape. If aliases
	// is empty, the alias doesn't exist — return "" cleanly instead of
	// falling through to the legacy flat-shape decoder, which would crash
	// on "cannot unmarshal object into []aliasEntry" when result is an
	// object with an empty aliases slice.
	if err := json.Unmarshal(bodyBytes, &env); err == nil {
		for _, a := range env.Result.Aliases {
			if a.AliasName == alias {
				return a.CollectionName, nil
			}
		}
		return "", nil
	}

	// Fallback: pre-PR1 / legacy flat shape — `{"result": [...]}`.
	// Remove once all fixtures and cached payloads have been validated
	// against the canonical Qdrant envelope.
	var legacy struct {
		Result []aliasEntry `json:"result"`
	}
	if err := json.Unmarshal(bodyBytes, &legacy); err != nil {
		return "", &APIError{
			Operation: "GetAliasTarget",
			Status:    http.StatusOK,
			Message:   fmt.Sprintf("invalid alias response: %v", err),
			Retryable: false,
		}
	}
	for _, a := range legacy.Result {
		if a.AliasName == alias {
			return a.CollectionName, nil
		}
	}
	return "", nil
}

// UpdateAliases performs a batched alias update (create/delete/switch).
func (c *Client) UpdateAliases(ctx context.Context, actions []map[string]any) error {
	body := map[string]any{
		"actions": actions,
	}
	url := fmt.Sprintf("%s/collections/aliases", c.baseURL)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("update aliases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// CreateAlias creates an alias pointing to a target collection.
func (c *Client) CreateAlias(ctx context.Context, alias, target string) error {
	return c.UpdateAliases(ctx, []map[string]any{
		{
			"create_alias": map[string]string{
				"alias_name":      alias,
				"collection_name": target,
			},
		},
	})
}

// DeleteAlias removes an alias without creating a replacement. It is used
// only for compensation when activation created a previously absent alias.
func (c *Client) DeleteAlias(ctx context.Context, alias string) error {
	return c.UpdateAliases(ctx, []map[string]any{
		{
			"delete_alias": map[string]string{
				"alias_name": alias,
			},
		},
	})
}

// SwitchAlias atomically changes an alias from oldTarget to newTarget.
func (c *Client) SwitchAlias(ctx context.Context, alias, oldTarget, newTarget string) error {
	actions := []map[string]any{}
	if oldTarget != "" {
		actions = append(actions, map[string]any{
			"delete_alias": map[string]string{
				"alias_name": alias,
			},
		})
	}
	actions = append(actions, map[string]any{
		"create_alias": map[string]string{
			"alias_name":      alias,
			"collection_name": newTarget,
		},
	})
	return c.UpdateAliases(ctx, actions)
}
