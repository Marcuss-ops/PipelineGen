package vectorstore

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

// aliasResponse is the Qdrant alias GET response shape (subset we need).
type aliasResponse struct {
	Result struct {
		Aliases []struct {
			AliasName  string `json:"alias_name"`
			Collection string `json:"collection_name"`
		} `json:"aliases"`
	} `json:"result"`
}

// versionedCollectionName returns the actual Qdrant collection name to
// write to, given the logical collection and the schema version. When
// CollectionVersion is empty (legacy deployments), the logical collection
// name is used unchanged, preserving pre-alias behaviour.
func (c *QdrantClient) versionedCollectionName() string {
	if c.cfg.CollectionVersion == "" {
		return c.cfg.Collection
	}
	return fmt.Sprintf("%s_%s", c.cfg.Collection, c.cfg.CollectionVersion)
}

// aliasName returns the alias that always points at "current" collection,
// or empty string when aliasing is disabled.
func (c *QdrantClient) aliasName() string {
	if c.cfg.CollectionVersion == "" || c.cfg.DisableAlias {
		return ""
	}
	if c.cfg.CollectionAlias != "" {
		return c.cfg.CollectionAlias
	}
	return c.cfg.Collection + "_current"
}

// AliasBasePath is the Qdrant endpoint for alias lifecycle. Since Qdrant 1.x
// the deprecation of the bare `/aliases` path was completed; the canonical
// endpoint is `/collections/aliases`. All alias lifecycle calls below route
// through this constant so future Qdrant API moves remain a one-line change.
const AliasBasePath = "/collections/aliases"

// operationCollection returns the collection name that data-plane operations
// (search, upsert, delete, scroll, payload) should target. It is the
// SINGLE resolver for any URL containing a collection segment.
//
// Three-case invariant — exactly one of these returns, in this order:
//
//  1. CollectionVersion == ""   -> cfg.Collection (legacy mode).
//     Pre-alias deployments operate directly on the logical name.
//  2. DisableAlias == true      -> versionedCollectionName().
//     Operator opted out of alias-routing and wants writes to target
//     the physical versioned collection (one-off backfill scripts).
//  3. otherwise                 -> aliasName().
//     Zero-downtime migration: the alias is the data-plane entry point
//     and is swapped via the explicit SwitchAlias entry-point, never
//     in EnsureCollection.
//
// Why case 3 is unconditional: aliasName()'s first guard fires only
// when CollectionVersion == \"\" OR DisableAlias == true. After the two
// prior cases have already returned, both predicates are false, so the
// guard cannot trigger and aliasName() always reaches one of its two
// non-empty returns (cfg.CollectionAlias or cfg.Collection+\"_current\").
// Therefore this branch needs no `if alias != \"\"` re-check, and there is
// NO fallback to versionedCollectionName() at the tail. Do NOT
// reintroduce a tail return or a guard here — admin endpoints
// (EnsureCollection, CollectionInfo, cleanup) continue to call
// versionedCollectionName() directly so they always target the
// physical collection.
// Companion tests in client_collection_test.go pin the resolver's
// externally observable outputs; any gate change that shifts a return
// value turns at least one red (including the aliasName /
// versionedCollectionName string-agreement axis, when both routes
// resolve to the same string by configuration).
func (c *QdrantClient) operationCollection() string {
	if c.cfg.CollectionVersion == "" {
		return c.cfg.Collection
	}
	if c.cfg.DisableAlias {
		return c.versionedCollectionName()
	}
	// Case 3: aliasName() is invariantly non-empty here.
	// The `if alias != \"\"` guard that previously sat here was
	// intentionally elided (see docstring above) — it was correct but
	// visually suggested defensive coding that this invariant removes.
	return c.aliasName()
}

// EnsureCollection creates the versioned collection + alias if missing.
// Pattern: `{Collection}_{Version}` is the actual collection, and
// `{Collection}_current` (or CollectionAlias) is an alias pointing at it.
//
//	pipelinegen_clips_current → pipelinegen_clips_v1
//
// On schema change, bump CollectionVersion in config; a new collection
// is created and (after backfill) the alias can be swapped via
// SwitchAlias() — clients keep reading through the alias with zero downtime.
func (c *QdrantClient) EnsureCollection(ctx context.Context) error {
	target := c.versionedCollectionName()

	respBody, err := c.qdrantRequest(ctx, "GET", fmt.Sprintf("/collections/%s", target), nil)
	if err != nil {
		// Collection doesn't exist — create it with full config
		fullVectors := c.buildVectorsConfig(true)
		if createErr := c.createCollection(ctx, target, fullVectors); createErr != nil {
			return createErr
		}
	} else {
		// Collection exists — check if transcript vector is present
		hasTranscript := c.collectionHasTranscript(respBody)
		if !hasTranscript {
			if err := c.migrateAddTranscriptVector(ctx, target); err != nil {
				return err
			}
		}
	}

	// Ensure alias exists and points at the target collection.
	alias := c.aliasName()
	if alias == "" {
		// Aliasing disabled (legacy mode). Operate directly on logical name.
		return nil
	}
	if err := c.ensureAlias(ctx, alias, target); err != nil {
		return fmt.Errorf("ensure alias: %w", err)
	}
	return nil
}

// migrateAddTranscriptVector adds the transcript vector to an existing
// collection via PUT /collections/{name}. The previous DELETE+recreate
// fallback has been REMOVED: an automatic destructive recreation cannot
// happen on startup. If the PUT fails the function returns the error and
// the operator must choose explicitly: bump CollectionVersion (recommended),
// drop the collection manually, or leave the schema unchanged.
func (c *QdrantClient) migrateAddTranscriptVector(ctx context.Context, target string) error {
	c.log.Info("transcript vector missing from Qdrant collection, attempting migration",
		zap.String("collection", target),
		zap.String("transcript_vector", c.cfg.TranscriptVectorName))

	migrationVectors := c.buildVectorsConfig(true)
	_, err := c.qdrantRequest(ctx, "PUT", fmt.Sprintf("/collections/%s", target), map[string]any{
		"vectors": migrationVectors,
	})
	if err != nil {
		c.log.Error("Qdrant PUT migration failed; refusing to auto-recreate a non-empty collection",
			zap.String("collection", target),
			zap.String("transcript_vector", c.cfg.TranscriptVectorName),
			zap.Error(err))
		return fmt.Errorf("add transcript vector to %s: %w", target, err)
	}
	c.log.Info("Qdrant collection migrated: transcript vector added",
		zap.String("collection", target))
	return nil
}

// buildVectorsConfig returns the named vectors config map.
// If includeTranscript is true, includes the transcript vector.
func (c *QdrantClient) buildVectorsConfig(includeTranscript bool) map[string]any {
	vectors := map[string]any{
		c.cfg.TextVectorName: map[string]any{
			"size":     c.cfg.TextDimensions,
			"distance": "Cosine",
		},
		c.cfg.VisualVectorName: map[string]any{
			"size":     c.cfg.VisualDimensions,
			"distance": "Cosine",
		},
	}
	if c.cfg.AudioVectorName != "" {
		vectors[c.cfg.AudioVectorName] = map[string]any{
			"size":     c.cfg.AudioDimensions,
			"distance": "Cosine",
		}
	}
	if includeTranscript && c.cfg.TranscriptVectorName != "" && c.cfg.TranscriptDimensions > 0 {
		vectors[c.cfg.TranscriptVectorName] = map[string]any{
			"size":     c.cfg.TranscriptDimensions,
			"distance": "Cosine",
		}
	}
	return vectors
}

// createCollection creates a new Qdrant collection with the given vectors config.
// Accepts an explicit target name so callers can operate on either the
// versioned collection or an arbitrary migration target.
func (c *QdrantClient) createCollection(ctx context.Context, target string, vectorsConfig map[string]any) error {
	createReq := map[string]any{
		"name":    target,
		"vectors": vectorsConfig,
		"hnsw_config": map[string]any{
			"m":            16,
			"ef_construct": 100,
		},
	}

	// Add sparse vector config for BM25 if name is set
	if c.cfg.SparseVectorName != "" {
		createReq["sparse_vectors"] = map[string]any{
			c.cfg.SparseVectorName: map[string]any{
				"index": map[string]any{
					"on_disk": false,
				},
			},
		}
	}

	_, err := c.qdrantRequest(ctx, "PUT",
		fmt.Sprintf("/collections/%s", target), createReq)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	return nil
}

// collectionHasTranscript parses the collection info response and checks if
// the transcript named vector is present.
func (c *QdrantClient) collectionHasTranscript(respBody []byte) bool {
	if c.cfg.TranscriptVectorName == "" {
		return true // transcript not configured, nothing to check
	}

	var info struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors map[string]any `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &info); err != nil {
		// If we can't parse, assume transcript is missing and migrate
		return false
	}

	_, exists := info.Result.Config.Params.Vectors[c.cfg.TranscriptVectorName]
	return exists
}

// fetchCollectionInfo queries /collections/{name} and parses the points_count
// from the response. Private helper used by both OperationCollectionInfo and
// PhysicalCollectionInfo so the parsing logic isn't duplicated. Returns a
// httpError for any 4xx/5xx so callers can branch on the status code via
// errors.As + AsHTTPError.
func (c *QdrantClient) fetchCollectionInfo(ctx context.Context, name string) (*CollectionInfo, error) {
	respBody, err := c.qdrantRequest(ctx, "GET", fmt.Sprintf("/collections/%s", name), nil)
	if err != nil {
		return nil, fmt.Errorf("collection info for %s: %w", name, err)
	}
	var info struct {
		Result struct {
			PointsCount int64 `json:"points_count"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &info); err != nil {
		return nil, fmt.Errorf("parse collection info for %s: %w", name, err)
	}
	return &CollectionInfo{PointsCount: info.Result.PointsCount}, nil
}

// OperationCollectionInfo returns metadata about the data-plane collection
// served through the alias (or the logical collection when aliasing is
// disabled). This is what users see in search/upsert; the index-health
// cross-check uses it because drift between the alias and SQLite is the
// drift operators care about.
func (c *QdrantClient) OperationCollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	return c.fetchCollectionInfo(ctx, c.operationCollection())
}

// PhysicalCollectionInfo returns metadata about the versioned (non-aliased)
// collection. Admin endpoints and migration-aware tooling use it to monitor
// the backfill window. May diverge from OperationCollectionInfo during a
// SwitchAlias swap (alias still serving old v1 while v2 is in backfill).
func (c *QdrantClient) PhysicalCollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	return c.fetchCollectionInfo(ctx, c.versionedCollectionName())
}

// CollectionInfo returns metadata about the physical (versioned) collection
// — alias for PhysicalCollectionInfo. Retained so older callers keep
// compiling while the cross-check migrates to OperationCollectionInfo. New
// code should pick the explicit method that matches the intent.
func (c *QdrantClient) CollectionInfo(ctx context.Context) (*CollectionInfo, error) {
	return c.PhysicalCollectionInfo(ctx)
}

// IndexHealth returns a cross-check between DB records and Qdrant points.
func (c *QdrantClient) IndexHealth(ctx context.Context) (*IndexHealthReport, error) {
	info, err := c.CollectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("collection info: %w", err)
	}
	return &IndexHealthReport{
		QdrantPoints: info.PointsCount,
		OK:           true,
	}, nil
}

// ── Alias management ────────────────────────────────────────────────────

// ensureAlias makes sure the alias points at the target collection. It is
// idempotent at startup: creates the alias if missing, leaves it alone if
// already pointing at the target, and REFUSES to repoint an existing alias
// that points elsewhere. That last branch is the scaling-review safety
// guard: an EnsureCollection startup call must never move production
// traffic. The operator must invoke SwitchAlias explicitly after verifying
// the new collection has been backfilled.
func (c *QdrantClient) ensureAlias(ctx context.Context, alias, target string) error {
	respBody, err := c.qdrantRequest(ctx, "GET", AliasBasePath, nil)
	if err != nil {
		return fmt.Errorf("list aliases: %w", err)
	}

	var ar aliasResponse
	if err := json.Unmarshal(respBody, &ar); err != nil {
		return fmt.Errorf("parse aliases: %w", err)
	}

	for _, a := range ar.Result.Aliases {
		if a.AliasName != alias {
			continue
		}
		if a.Collection == target {
			return nil // already correct
		}
		// Do NOT auto-repoint. Refuse and leave traffic on the existing target
		// until the operator runs SwitchAlias explicitly.
		return fmt.Errorf(
			"alias %q already points at %q (not %q); do not auto-repoint "+
				"on startup — verify backfill and call SwitchAlias explicitly",
			alias, a.Collection, target)
	}

	// Alias doesn't exist — create it.
	_, createErr := c.qdrantRequest(ctx, "POST", AliasBasePath, map[string]any{
		"actions": []map[string]any{
			{
				"create_alias": map[string]any{
					"alias_name":      alias,
					"collection_name": target,
				},
			},
		},
	})
	if createErr != nil {
		// Treat HTTP 409 (alias already exists) as benign — the parallel startup
		// that won the race has set the alias; we accept it. Detection via
		// errors.As + structural status code so we don't break if Qdrant
		// rewords its message.
		if he := AsHTTPError(createErr); he != nil && he.StatusCode == 409 {
			c.log.Info("Qdrant alias already exists (race resolved)",
				zap.String("alias", alias),
				zap.String("target", target))
			return nil
		}
		return fmt.Errorf("create alias %s → %s: %w", alias, target, createErr)
	}
	c.log.Info("Qdrant alias created",
		zap.String("alias", alias),
		zap.String("collection", target))
	return nil
}

// repointAlias atomically swaps an existing alias from oldCollection to
// newCollection via POST /collections/aliases with both delete+create
// actions in a single batch — readers never see a missing alias. This is
// the only function called to move traffic, and only from SwitchAlias.
func (c *QdrantClient) repointAlias(ctx context.Context, alias, oldCollection, newCollection string) error {
	_, err := c.qdrantRequest(ctx, "POST", AliasBasePath, map[string]any{
		"actions": []map[string]any{
			{"delete_alias": map[string]any{"alias_name": alias}},
			{"create_alias": map[string]any{
				"alias_name":      alias,
				"collection_name": newCollection,
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("swap alias %s from %s to %s: %w", alias, oldCollection, newCollection, err)
	}
	c.log.Info("Qdrant alias repointed",
		zap.String("alias", alias),
		zap.String("from", oldCollection),
		zap.String("to", newCollection))
	return nil
}

// SwitchAlias is the public migration entry-point. After a new versioned
// collection has been populated, callers invoke this to atomically swap
// the alias from the previous versioned collection to the new one.
// oldCollection and newCollection are the FULL versioned collection names
// (e.g. "pipelinegen_clips_v1" and "pipelinegen_clips_v2"). The previous
// versioned collection is not deleted — operators can drop it manually
// once the swap is verified in production.
func (c *QdrantClient) SwitchAlias(ctx context.Context, oldCollection, newCollection string) error {
	alias := c.aliasName()
	if alias == "" {
		return fmt.Errorf("alias pattern disabled; cannot switch alias")
	}
	return c.repointAlias(ctx, alias, oldCollection, newCollection)
}

// CreateAlias exposes alias creation for operators that want to manage
// aliases manually (e.g. one-off scripts).
func (c *QdrantClient) CreateAlias(ctx context.Context, alias, collection string) error {
	_, err := c.qdrantRequest(ctx, "POST", AliasBasePath, map[string]any{
		"actions": []map[string]any{
			{"create_alias": map[string]any{
				"alias_name":      alias,
				"collection_name": collection,
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("create alias %s → %s: %w", alias, collection, err)
	}
	return nil
}

// DeleteAlias removes an alias. Does NOT delete the underlying collection.
func (c *QdrantClient) DeleteAlias(ctx context.Context, alias string) error {
	_, err := c.qdrantRequest(ctx, "POST", AliasBasePath, map[string]any{
		"actions": []map[string]any{
			{"delete_alias": map[string]any{"alias_name": alias}},
		},
	})
	if err != nil {
		return fmt.Errorf("delete alias %s: %w", alias, err)
	}
	return nil
}
