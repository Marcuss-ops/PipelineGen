package model

// manifest.go owns the P5 overlay manifest identity model
// (docs/operations/TICKET-MANIFEST-IDENTITY.md): an overlay is not its own
// MP4 but a manifest entry — item_id → (track, in_us, out_us, content-hash) —
// inside one certified artifact per timeline. Identity is per manifest,
// retry is per entry.
//
// Two identities, on purpose:
//
//   - OverlayManifestEntry.RetryKey names the ENTRY's work unit
//     (item_id + content-hash, nothing else): the same pair must never
//     re-render nor produce a second artifact on retry. Placement
//     (track/in_us/out_us) is timeline metadata, not content, so it is
//     intentionally excluded from the retry identity.
//   - OverlayManifest.Digest names the REQUEST (plan identity + entries +
//     placement). It is the content address of the composed timeline
//     artifact and is computed WITHOUT TimelineSHA256, so it is stable both
//     before the artifact exists (cache key) and after (receipt).
//
// Serialization is deterministic: MarshalJSON emits entries in canonical
// (in_us, out_us, track, item_id) order, so the same manifest always
// produces the same bytes and the same digest.
//
// Zero I/O here. This package must never reference RenderArtifact, the
// platform store, or Transport; the scripts layer consumes these types
// through the generated facade.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	// SchemaVersionManifest is the wire identity of the overlay manifest
	// document: the semantic rendering-schema family this model belongs to.
	SchemaVersionManifest = "pipelinegen.overlay-manifest.v1"

	// manifestEntryRetryTag version-tags entry retry identities so the hash
	// can never collide with another digest domain in this repo.
	manifestEntryRetryTag = "overlay-manifest-entry-retry/v1"
)

// ManifestPlacement is the OPTIONAL spatial projection of an entry
// (docs/operations/TICKET-PARADIGM-ALPHA-LAYER-HUD.md §A1, contract §3.2):
// the pixel rect, on the plan's canvas, where the entry's content draws.
// Absent = full-canvas (the current path, no migration); present = the
// content projects into this rect. The four values are set together — a
// half-set rect is malformed and fails validation. Canvas bounds are a
// producer-side fail-closed check (the manifest carries no canvas size).
type ManifestPlacement struct {
	XPx int64 `json:"x_px"`
	YPx int64 `json:"y_px"`
	WPx int64 `json:"w_px"`
	HPx int64 `json:"h_px"`
}

// OverlayManifestEntry is one overlay's identity inside a single certified
// timeline artifact. The pair (ItemID, ContentHash) is the idempotent work
// identity; Track/InUS/OutUS/Placement place the entry's content on the
// timeline and canvas.
type OverlayManifestEntry struct {
	// ItemID is the stable semantic identity of the overlay item, reused
	// verbatim from the plan item ID across regenerations of the same slot.
	ItemID string `json:"item_id"`
	// Track is the compositing layer order (z-order) of the entry. 0 is the
	// lowest overlay layer; higher values draw on top.
	Track int `json:"track"`
	// InUS/OutUS place the entry on the timeline using the plan's canonical
	// integer-microsecond clock: InUS is the inclusive start, OutUS the
	// exclusive end (InUS + the item's duration_us). Validation requires
	// OutUS > InUS.
	InUS  int64 `json:"in_us"`
	OutUS int64 `json:"out_us"`
	// ContentHash is the content-addressed identity of the entry's rendered
	// content: a canonical lowercase SHA-256 (64 hex). It names WHAT is
	// drawn, never where; two entries may share content but never ItemID.
	ContentHash string `json:"content_hash"`
	// ArtifactInUS/ArtifactOutUS are the OPTIONAL packed-composition positions
	// (docs/operations/OVERLAY-MANIFEST-CONTRACT.md §3, "packed timeline"):
	// where on the CERTIFIED artifact's own frames the entry's content was
	// actually rendered. Both stay nil on the classic plan-absolute timeline;
	// on a packed timeline (§3.4 of TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE:
	// empty plan spans are NOT rendered) they are both set and differ from
	// InUS/OutUS — which keep carrying the semantic plan window verbatim.
	// Nil pointers keep the zero/absent distinction: packed offsets may be 0.
	ArtifactInUS  *int64 `json:"artifact_in_us,omitempty"`
	ArtifactOutUS *int64 `json:"artifact_out_us,omitempty"`
	// Placement is the OPTIONAL spatial projection (ManifestPlacement): the
	// pixel rect on the plan canvas where this entry draws. Nil means the
	// entry draws full-canvas (the classic path). Like Track/InUS/OutUS it
	// is placement metadata: it is excluded from RetryKey (content identity)
	// and included in OverlayManifest.Digest (composition identity — two
	// rects are two compositions, because the pixels differ).
	Placement *ManifestPlacement `json:"placement,omitempty"`
}

// Validate reports whether the entry is a well-formed manifest row.
func (e OverlayManifestEntry) Validate() error {
	if strings.TrimSpace(e.ItemID) == "" {
		return fmt.Errorf("overlay manifest entry: item_id is required")
	}
	if e.Track < 0 {
		return fmt.Errorf("overlay manifest entry %q: track must be >= 0", e.ItemID)
	}
	if e.InUS < 0 {
		return fmt.Errorf("overlay manifest entry %q: in_us must be >= 0", e.ItemID)
	}
	if e.OutUS <= e.InUS {
		return fmt.Errorf("overlay manifest entry %q: out_us must be greater than in_us", e.ItemID)
	}
	if !digest.IsCanonicalSHA256(e.ContentHash) {
		return fmt.Errorf("overlay manifest entry %q: content_hash %q is not a canonical SHA-256", e.ItemID, e.ContentHash)
	}
	if (e.ArtifactInUS == nil) != (e.ArtifactOutUS == nil) {
		return fmt.Errorf("overlay manifest entry %q: artifact_in_us/artifact_out_us must be set together", e.ItemID)
	}
	if e.ArtifactInUS != nil {
		in, out := *e.ArtifactInUS, *e.ArtifactOutUS
		if in < 0 {
			return fmt.Errorf("overlay manifest entry %q: artifact_in_us must be >= 0", e.ItemID)
		}
		if out <= in {
			return fmt.Errorf("overlay manifest entry %q: artifact_out_us must be greater than artifact_in_us", e.ItemID)
		}
		if out-in > e.OutUS-e.InUS {
			// The packed span never outlasts the entry's literal plan window:
			// packing only shrinks the canvas (union of real overlay windows),
			// it never re-times an entry's own duration.
			return fmt.Errorf("overlay manifest entry %q: packed arrow [%d,%d) exceeds the entry window", e.ItemID, in, out)
		}
	}
	if e.Placement != nil {
		if e.Placement.XPx < 0 || e.Placement.YPx < 0 {
			return fmt.Errorf("overlay manifest entry %q: placement x_px/y_px must be >= 0", e.ItemID)
		}
		if e.Placement.WPx <= 0 || e.Placement.HPx <= 0 {
			return fmt.Errorf("overlay manifest entry %q: placement w_px/h_px must be greater than 0", e.ItemID)
		}
	}
	return nil
}

// RetryKey is the idempotent work identity of the entry: same item_id and
// same content-hash always map to the same key, so a retried entry reuses
// its certified content instead of re-rendering it or storing a duplicate
// artifact. Placement is deliberately not part of the key.
func (e OverlayManifestEntry) RetryKey() string {
	return digest.SHA256String(strings.Join([]string{
		manifestEntryRetryTag,
		strings.TrimSpace(e.ItemID),
		strings.ToLower(strings.TrimSpace(e.ContentHash)),
	}, "\x00"))
}

// OverlayManifest is the P5 replacement for per-item intermediates: one
// certified artifact per timeline, indexed by manifest entries. It is also
// the wire document handed to the final worker's contract slot.
type OverlayManifest struct {
	SchemaVersion string `json:"schema_version"`
	PlanID        string `json:"plan_id"`
	VideoID       string `json:"video_id"`
	// TimelineSHA256 is the identity of the single certified timeline
	// artifact (the composed output). It is the RESULT of the render: it is
	// excluded from Digest() so the request identity is stable before the
	// artifact exists. Empty means "not yet certified".
	TimelineSHA256 string                 `json:"timeline_sha256,omitempty"`
	Entries        []OverlayManifestEntry `json:"entries"`
}

// Validate reports whether the manifest is a well-formed, internally
// consistent document. It enforces the production content-identity rule
// ("one image one overlay", the 2026-09-30 incident class): two entries may
// never share ItemID or ContentHash.
func (m *OverlayManifest) Validate() error {
	if m == nil {
		return fmt.Errorf("overlay manifest: document is required")
	}
	if m.SchemaVersion != SchemaVersionManifest {
		return fmt.Errorf("overlay manifest: schema_version must be %q", SchemaVersionManifest)
	}
	if strings.TrimSpace(m.PlanID) == "" {
		return fmt.Errorf("overlay manifest: plan_id is required")
	}
	if strings.TrimSpace(m.VideoID) == "" {
		return fmt.Errorf("overlay manifest: video_id is required")
	}
	if m.TimelineSHA256 != "" && !digest.IsCanonicalSHA256(m.TimelineSHA256) {
		return fmt.Errorf("overlay manifest: timeline_sha256 %q is not a canonical SHA-256", m.TimelineSHA256)
	}
	if len(m.Entries) == 0 {
		return fmt.Errorf("overlay manifest: entries must not be empty")
	}
	seenItem := make(map[string]struct{}, len(m.Entries))
	seenContent := make(map[string]string, len(m.Entries))
	for _, entry := range m.Entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		if _, dup := seenItem[entry.ItemID]; dup {
			return fmt.Errorf("overlay manifest: duplicate item_id %q", entry.ItemID)
		}
		seenItem[entry.ItemID] = struct{}{}
		content := strings.ToLower(entry.ContentHash)
		if other, dup := seenContent[content]; dup {
			return fmt.Errorf("overlay manifest: content_hash %q is shared by entries %q and %q (one content one overlay)", entry.ContentHash, other, entry.ItemID)
		}
		seenContent[content] = entry.ItemID
	}
	return nil
}

// canonicalEntries returns the entries in canonical display order:
// (in_us, out_us, track, item_id). Track carries z-order explicitly, so
// sorting the rows never changes rendering semantics.
func (m OverlayManifest) canonicalEntries() []OverlayManifestEntry {
	entries := make([]OverlayManifestEntry, len(m.Entries))
	copy(entries, m.Entries)
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.InUS != b.InUS {
			return a.InUS < b.InUS
		}
		if a.OutUS != b.OutUS {
			return a.OutUS < b.OutUS
		}
		if a.Track != b.Track {
			return a.Track < b.Track
		}
		return a.ItemID < b.ItemID
	})
	return entries
}

// MarshalJSON emits the manifest with entries in canonical order, making
// the wire bytes deterministic for a given document regardless of how the
// caller ordered Entries.
func (m OverlayManifest) MarshalJSON() ([]byte, error) {
	type manifestWire struct {
		SchemaVersion  string                 `json:"schema_version"`
		PlanID         string                 `json:"plan_id"`
		VideoID        string                 `json:"video_id"`
		TimelineSHA256 string                 `json:"timeline_sha256,omitempty"`
		Entries        []OverlayManifestEntry `json:"entries"`
	}
	return json.Marshal(manifestWire{
		SchemaVersion:  m.SchemaVersion,
		PlanID:         m.PlanID,
		VideoID:        m.VideoID,
		TimelineSHA256: m.TimelineSHA256,
		Entries:        m.canonicalEntries(),
	})
}

// Digest is the content address of the composed timeline artifact: the
// identity of the REQUEST (schema + plan identity + canonical entries with
// their placement), deliberately excluding TimelineSHA256 so the value is
// identical before and after certification. Two manifests with the same
// entries and placement compose the same artifact and must share one cache
// row and one stored artifact.
func (m OverlayManifest) Digest() string {
	type manifestRequest struct {
		SchemaVersion string                 `json:"schema_version"`
		PlanID        string                 `json:"plan_id"`
		VideoID       string                 `json:"video_id"`
		Entries       []OverlayManifestEntry `json:"entries"`
	}
	raw, err := json.Marshal(manifestRequest{
		SchemaVersion: m.SchemaVersion,
		PlanID:        m.PlanID,
		VideoID:       m.VideoID,
		Entries:       m.canonicalEntries(),
	})
	if err != nil {
		// Unreachable for plain string/int fields; fail closed to an empty
		// identity that no cache row can ever match.
		return ""
	}
	return digest.SHA256Bytes(raw)
}
