package reconciliation

import (
	"fmt"
	"sort"
	"strings"
)

// classify is the pure classification function.
//
// Given the (sqliteSnapshots, qdrantPoints) snapshot computed by the
// service (already fetched from the canonical ports), produces the
// classification list. No IO, no time.Now side effects — test-friendly.
//
// The function returns ONE classification per asset_id (high-priority
// wins when multiple categories could apply). Multiple classifications
// are emitted ONLY across distinct asset_ids.
//
// Order in the returned slice:
//  1. All Missing entries (alphabetical by asset_id, for stable diffs).
//  2. All Orphan entries.
//  3. All Duplicate entries (Task 6, July 2026).
//  4. Paired classifications with priority applied
//     (highest-priority kind wins).
func classify(sqliteSet map[string]AssetSnapshot, qdrantSet map[string]pointWithID, schema SchemaVersions, pointIDFor AssetPointIDFunc, duplicates map[string][]pointWithID) []Classification {
	var missing []Classification
	for id := range sqliteSet {
		if _, ok := qdrantSet[id]; !ok {
			// PR 10+11: surface content_hash so repair dispatch can
			// compute the deterministic outbox dedupe key without an
			// extra sqlite lookup. Empty when ListAssetsForReconcile
			// returned "" for this row (e.g. row without metadata_json
			// or pre-hash ingest path).
			missing = append(missing, Classification{
				Kind:        KindMissing,
				AssetID:     id,
				ContentHash: sqliteSet[id].ContentHash,
				Details:     "asset exists in media_assets but no matching Qdrant point found via payload.asset_id",
			})
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].AssetID < missing[j].AssetID })

	var orphan []Classification
	for id, p := range qdrantSet {
		if _, ok := sqliteSet[id]; !ok {
			orphan = append(orphan, Classification{
				Kind:          KindOrphan,
				AssetID:       id,
				QdrantPointID: p.ID,
				Details:       "Qdrant point has payload.asset_id that does not match any media_assets id",
			})
		}
	}
	sort.Slice(orphan, func(i, j int) bool { return orphan[i].AssetID < orphan[j].AssetID })

	var pairs []Classification
	for id, snap := range sqliteSet {
		p, ok := qdrantSet[id]
		if !ok {
			continue
		}
		c := classifyPair(id, snap, p, schema, pointIDFor)
		if c == nil {
			continue
		}
		c.QdrantPointID = p.ID
		pairs = append(pairs, *c)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].AssetID == pairs[j].AssetID {
			return pairs[i].Kind < pairs[j].Kind
		}
		return pairs[i].AssetID < pairs[j].AssetID
	})

	var duplicatesCls []Classification
	for assetID, extraPoints := range duplicates {
		for _, p := range extraPoints {
			duplicatesCls = append(duplicatesCls, Classification{
				Kind:          KindDuplicate,
				AssetID:       assetID,
				QdrantPointID: p.ID,
				Details:       fmt.Sprintf("duplicate Qdrant point for asset_id %q: point ID %q (first occurrence kept)", assetID, p.ID),
			})
		}
	}
	sort.Slice(duplicatesCls, func(i, j int) bool {
		if duplicatesCls[i].AssetID == duplicatesCls[j].AssetID {
			return duplicatesCls[i].QdrantPointID < duplicatesCls[j].QdrantPointID
		}
		return duplicatesCls[i].AssetID < duplicatesCls[j].AssetID
	})

	out := make([]Classification, 0, len(missing)+len(orphan)+len(duplicatesCls)+len(pairs))
	out = append(out, missing...)
	out = append(out, orphan...)
	out = append(out, duplicatesCls...)
	out = append(out, pairs...)
	return out
}

// pointWithID pairs a point's ID with its payload for classification.
type pointWithID struct {
	ID      string
	Payload map[string]any
}

// classifyPair applies the priority-ordered checks to a single
// (sqlite snapshot, qdrant point) pair. Returns nil when the pair
// is fully consistent (no drift).
//
// Priority order:
//  1. NonCanonicalPointID — Qdrant point ID contradicts the
//     AssetIDToQdrantPointID(asset_id) contract.
//  2. PayloadIncomplete — payload missing required keys.
//  3. VersionStale — any channel embedding_version_<ch> mismatch.
//  4. HashMismatch — SQLite's canonical content_hash differs from
//     the Qdrant projection's content_hash. An empty SQLite hash is
//     not treated as a mismatch because SQLite has no authoritative
//     value to compare.
//  5. LifecycleMismatch — case-insensitive compare.
//  6. WorkspaceMismatch — exact-string compare (case-sensitive).
//  7. LifecycleKeyLegacy — payload uses retired "status" key.
//  8. LocatorLegacy — payload retains drive_link / local_path.
func classifyPair(assetID string, snap AssetSnapshot, p pointWithID, schema SchemaVersions, pointIDFor AssetPointIDFunc) *Classification {
	expected := pointIDFor(assetID)
	if expected != "" && p.ID != "" && expected != p.ID {
		return &Classification{
			Kind:        KindNonCanonicalPointID,
			AssetID:     assetID,
			ContentHash: snap.ContentHash,
			Details:     fmt.Sprintf("expected %q, got %q", expected, p.ID),
		}
	}
	for _, k := range schema.RequiredKeys {
		if _, ok := p.Payload[k]; !ok {
			return &Classification{
				Kind:        KindPayloadIncomplete,
				AssetID:     assetID,
				ContentHash: snap.ContentHash,
				Details:     fmt.Sprintf("missing required payload key %q", k),
			}
		}
	}
	for channel, want := range schema.PerChannelVersion {
		if want == "" {
			continue
		}
		key := "embedding_version_" + channel
		actual, present := stringFromPayload(p.Payload, key)
		if !present {
			legacy, hasLegacy := stringFromPayload(p.Payload, "embedding_version")
			if !hasLegacy || legacy != want {
				return &Classification{
					Kind:        KindVersionStale,
					AssetID:     assetID,
					ContentHash: snap.ContentHash,
					Channel:     channel,
					Details:     fmt.Sprintf("channel %q: payload missing %q and legacy global not equal to %q", channel, key, want),
				}
			}
			continue
		}
		if actual != want {
			return &Classification{
				Kind:        KindVersionStale,
				AssetID:     assetID,
				ContentHash: snap.ContentHash,
				Channel:     channel,
				Details:     fmt.Sprintf("channel %q: payload %q=%q, schema wants %q", channel, key, actual, want),
			}
		}
	}
	// SQLite is the sole authority for content identity. Compare only
	// when SQLite has a canonical hash. A missing or different Qdrant
	// hash means the projection is stale; Qdrant is never allowed to
	// fill an absent SQLite value or become authoritative.
	if canonicalHash := strings.TrimSpace(snap.ContentHash); canonicalHash != "" {
		if projectedHash := strings.TrimSpace(stringFromPayloadOrEmpty(p.Payload, "content_hash")); projectedHash != canonicalHash {
			return &Classification{
				Kind:        KindHashMismatch,
				AssetID:     assetID,
				ContentHash: snap.ContentHash,
				Details:     fmt.Sprintf("sqlite content_hash=%q, qdrant payload content_hash=%q", canonicalHash, projectedHash),
			}
		}
	}

	wantStateWantVerify := strings.ToLower(strings.TrimSpace(snap.LifecycleState))
	if wantStateWantVerify != "" {
		gotState := strings.ToLower(strings.TrimSpace(stringFromPayloadOrEmpty(p.Payload, "lifecycle_state")))
		if gotState != "" && wantStateWantVerify != gotState {
			return &Classification{
				Kind:        KindLifecycleMismatch,
				AssetID:     assetID,
				ContentHash: snap.ContentHash,
				Details:     fmt.Sprintf("sqlite=%q, payload=%q", wantStateWantVerify, gotState),
			}
		}
	}
	if snap.WorkspaceID != "" {
		gotWS := strings.TrimSpace(stringFromPayloadOrEmpty(p.Payload, "workspace_id"))
		if gotWS != "" && snap.WorkspaceID != gotWS {
			return &Classification{
				Kind:        KindWorkspaceMismatch,
				AssetID:     assetID,
				ContentHash: snap.ContentHash,
				Channel:     "",
				Details:     fmt.Sprintf("sqlite=%q, payload=%q", snap.WorkspaceID, gotWS),
			}
		}
	}
	if _, ok := p.Payload["status"]; ok {
		return &Classification{
			Kind:        KindLifecycleKeyLegacy,
			AssetID:     assetID,
			ContentHash: snap.ContentHash,
			Details:     "payload uses legacy \"status\" key; canonical key is \"lifecycle_state\"",
		}
	}
	// LocatorLegacy: capture EVERY present legacy locator key into
	// LocatorKeys so the service-layer metric accounting can bump the
	// canonical payload_legacy_cleaned_total{legacy_key=...} series
	// per-key based on what the payload actually carries (rather than
	// a blanket bump per locator point regardless of which keys it
	// had). Pre-fix this loop returned at the first hit, which lost
	// the second-key presence signal.
	keys := make([]string, 0, 2)
	for _, legacy := range []string{"drive_link", "local_path"} {
		v, ok := p.Payload[legacy]
		if !ok || v == nil {
			continue
		}
		if s, _ := v.(string); s != "" {
			keys = append(keys, legacy)
		}
	}
	if len(keys) > 0 {
		return &Classification{
			Kind:        KindLocatorLegacy,
			AssetID:     assetID,
			ContentHash: snap.ContentHash,
			Details:     fmt.Sprintf("payload carries legacy locator keys %v", keys),
			LocatorKeys: keys,
		}
	}
	return nil
}

func stringFromPayload(p map[string]any, key string) (string, bool) {
	if p == nil {
		return "", false
	}
	v, ok := p[key]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, s != ""
}

func stringFromPayloadOrEmpty(p map[string]any, key string) string {
	s, _ := stringFromPayload(p, key)
	return s
}
