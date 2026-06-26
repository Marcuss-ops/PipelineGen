package reconciler

import (
	"fmt"
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
//  3. Paired classifications with priority applied
//     (highest-priority kind wins).
func classify(sqliteSet map[string]AssetSnapshot, qdrantSet map[string]pointWithID, schema SchemaVersions, pointIDFor AssetPointIDFunc) []Classification {
	var missing []Classification
	for id := range sqliteSet {
		if _, ok := qdrantSet[id]; !ok {
			missing = append(missing, Classification{
				Kind:    KindMissing,
				AssetID: id,
				Details: "asset exists in media_assets but no matching Qdrant point found via payload.asset_id",
			})
		}
	}
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
	out := make([]Classification, 0, len(missing)+len(orphan)+len(pairs))
	out = append(out, missing...)
	out = append(out, orphan...)
	out = append(out, pairs...)
	return out
}

// pointWithID pairs a point's ID with its payload for classification.
type pointWithID struct {
	ID      string
	Payload map[string]interface{}
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
//  4. LifecycleMismatch — case-insensitive compare.
//  5. WorkspaceMismatch — exact-string compare (case-sensitive).
//  6. LifecycleKeyLegacy — payload uses retired "status" key.
//  7. LocatorLegacy — payload retains drive_link / local_path.
func classifyPair(assetID string, snap AssetSnapshot, p pointWithID, schema SchemaVersions, pointIDFor AssetPointIDFunc) *Classification {
	expected := pointIDFor(assetID)
	if expected != "" && p.ID != "" && expected != p.ID {
		return &Classification{
			Kind:    KindNonCanonicalPointID,
			AssetID: assetID,
			Details: fmt.Sprintf("expected %q, got %q", expected, p.ID),
		}
	}
	for _, k := range schema.RequiredKeys {
		if _, ok := p.Payload[k]; !ok {
			return &Classification{
				Kind:    KindPayloadIncomplete,
				AssetID: assetID,
				Details: fmt.Sprintf("missing required payload key %q", k),
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
					Kind:    KindVersionStale,
					AssetID: assetID,
					Channel: channel,
					Details: fmt.Sprintf("channel %q: payload missing %q and legacy global not equal to %q", channel, key, want),
				}
			}
			continue
		}
		if actual != want {
			return &Classification{
				Kind:    KindVersionStale,
				AssetID: assetID,
				Channel: channel,
				Details: fmt.Sprintf("channel %q: payload %q=%q, schema wants %q", channel, key, actual, want),
			}
		}
	}
	wantStateWantVerify := strings.ToLower(strings.TrimSpace(snap.LifecycleState))
	if wantStateWantVerify != "" {
		gotState := strings.ToLower(strings.TrimSpace(stringFromPayloadOrEmpty(p.Payload, "lifecycle_state")))
		if gotState != "" && wantStateWantVerify != gotState {
			return &Classification{
				Kind:    KindLifecycleMismatch,
				AssetID: assetID,
				Details: fmt.Sprintf("sqlite=%q, payload=%q", wantStateWantVerify, gotState),
			}
		}
	}
	if snap.WorkspaceID != "" {
		gotWS := strings.TrimSpace(stringFromPayloadOrEmpty(p.Payload, "workspace_id"))
		if gotWS != "" && snap.WorkspaceID != gotWS {
			return &Classification{
				Kind:    KindWorkspaceMismatch,
				AssetID: assetID,
				Channel: "",
				Details: fmt.Sprintf("sqlite=%q, payload=%q", snap.WorkspaceID, gotWS),
			}
		}
	}
	if _, ok := p.Payload["status"]; ok {
		return &Classification{
			Kind:    KindLifecycleKeyLegacy,
			AssetID: assetID,
			Details: "payload uses legacy \"status\" key; canonical key is \"lifecycle_state\"",
		}
	}
	for _, legacy := range []string{"drive_link", "local_path"} {
		v, ok := p.Payload[legacy]
		if !ok || v == nil {
			continue
		}
		if s, _ := v.(string); s != "" {
			return &Classification{
				Kind:    KindLocatorLegacy,
				AssetID: assetID,
				Details: fmt.Sprintf("payload carries legacy locator key %q", legacy),
			}
		}
	}
	return nil
}

func stringFromPayload(p map[string]interface{}, key string) (string, bool) {
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

func stringFromPayloadOrEmpty(p map[string]interface{}, key string) string {
	s, _ := stringFromPayload(p, key)
	return s
}
