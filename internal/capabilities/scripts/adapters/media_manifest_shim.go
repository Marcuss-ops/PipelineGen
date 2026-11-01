// Package adapters — media_manifest_shim.go: opt-in fast path backed by a
// pinned media manifest (schema media_manifest.v1, authored offline by
// ops/tools/media_manifest.py).
//
// Structural goal (Garlasco image-time fix): discovered assets that are
// already pinned by content SHA-256 must not re-enter the provider search or
// the VLM scorer at runtime. The manifest is the floor: pinned entries carry
// the durable verdict (accepted) and the content hash, so the fast path in
// scoreInternetImageCandidates (allCandidatesHaveReusableVerdict) has nothing
// left to score. A pinned run delivers candidates with zero provider calls —
// the stage keeps its contract of attaching candidates, never selecting.
//
// Opt-in contract: pins load ONLY when an explicit path is configured
// (VELOX_MEDIA_MANIFEST_PATH at the composition root, or WithMediaManifestPins
// in tests). No manifest, no behavior change; a broken manifest file is
// reported to the composition root as a warning that is never a run failure
// (the stage stays best-effort by policy, ProcessorBestEffort).
//
// Pins deliberately win over force_refresh: a force refresh asks for fresh
// discovery, but a pinned asset is the operator's authoritative pick (the r39
// deliverable pattern), so it is returned without touching providers. The
// pins are local, verified files, not network sources, so serving them in
// cache-only mode does not violate its no-provider contract.
package adapters

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MediaManifestProvider is the provenance label stamped on candidates injected
// through the pinned-manifest fast path.
const MediaManifestProvider = "media_manifest"

// veloxMediaManifestEnv is the opt-in env key read by LoadMediaManifestPinsFromEnv.
const veloxMediaManifestEnv = "VELOX_MEDIA_MANIFEST_PATH"

// MediaManifestCacheState is the SegmentCacheState.InternetImages label for a
// segment served entirely from pins. Deliberately distinct from HIT_EXACT so
// run observability can tell apart "manifest pin" from "L1/L2 cache hit".
const MediaManifestCacheState = "MANIFEST_PIN"

// mediaManifestEntry mirrors the per-entry shape written by
// ops/tools/media_manifest.py. Only the fields the runtime needs are decoded,
// the rest is parsed; strictness is on the CONTRACT (schema tag + 64-hex
// sha256), not on completeness.
type mediaManifestEntry struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	SceneID string `json:"scene_id"`
	Entity  string `json:"entity"`
	Query   string `json:"query"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
	Path    string `json:"path"`
	Source  struct {
		Provider    string `json:"provider"`
		SourceURL   string `json:"source_url"`
		DriveFileID string `json:"drive_file_id"`
	} `json:"source"`
	Verified struct {
		Identity  bool `json:"identity"`
		Structure bool `json:"structure"`
	} `json:"verified"`
}

type mediaManifestDoc struct {
	Schema  string               `json:"schema"`
	Scope   mediaManifestScope   `json:"scope"`
	Entries []mediaManifestEntry `json:"entries"`
}

// mediaManifestScope binds a manifest to the project that authored it. Scene
// ids are project-local (scene-01..scene-10 recur in every run), so an
// unscoped manifest would replay Garlasco's portraits in another project.
type mediaManifestScope struct {
	TitleContains []string `json:"title_contains"`
	TopicContains []string `json:"topic_contains"`
}

// MediaManifestPins holds the loaded manifest entries, indexed by scene.
// Use LoadMediaManifestPins / LoadMediaManifestPinsFromEnv to build it.
type MediaManifestPins struct {
	byScene       map[string][]mediaManifestEntry
	titleContains []string
	topicContains []string
	path          string
}

// validDigestHex checks the exact contract accepted by the offline verifier:
// 64 lowercase hex characters (information SHA-256). Hash strength is not a
// crypto property here; the length+alphabet check is a corruption guard, and
// the rule is owned by internal/kernel/digest rather than re-implemented here.
func validDigestHex(raw string) bool {
	return digest.IsCanonicalSHA256(strings.ToLower(strings.TrimSpace(raw)))
}

// LoadMediaManifestPins reads a media_manifest.v1 document from disk.
// It rejects an unknown schema and any entry whose sha256 is not a well-formed
// SHA-256 digest — the manifest is meant to gate runs, and a malformed gate
// corrupting discovery silently would be worse than failing eagerly at load
// time (the wiring logs and continues fail-open).
func LoadMediaManifestPins(path string) (*MediaManifestPins, error) {
	raw, err := os.ReadFile(path) // nolint: gosec — explicit operator-provided path
	if err != nil {
		return nil, fmt.Errorf("read media manifest %s: %w", path, err)
	}
	var doc mediaManifestDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse media manifest %s: %w", path, err)
	}
	if doc.Schema != "media_manifest.v1" {
		return nil, fmt.Errorf("media manifest %s: schema %q is not media_manifest.v1", path, doc.Schema)
	}
	pins := &MediaManifestPins{
		byScene:       map[string][]mediaManifestEntry{},
		titleContains: append([]string(nil), doc.Scope.TitleContains...),
		topicContains: append([]string(nil), doc.Scope.TopicContains...),
		path:          path,
	}
	for _, entry := range doc.Entries {
		if !validDigestHex(entry.SHA256) {
			return nil, fmt.Errorf("media manifest %s: entry %q has malformed sha256 %q",
				path, entry.ID, entry.SHA256)
		}
		key := strings.ToLower(strings.TrimSpace(entry.SceneID))
		pins.byScene[key] = append(pins.byScene[key], entry)
	}
	return pins, nil
}

// LoadMediaManifestPinsFromEnv resolves the opt-in contract: empty/unset env
// produces nil pins (no behavior change). A configured path that fails to load
// surfaces the error so the composition root can warn and continue fail-open.
func LoadMediaManifestPinsFromEnv() (*MediaManifestPins, error) {
	path := strings.TrimSpace(os.Getenv(veloxMediaManifestEnv))
	if path == "" {
		return nil, nil
	}
	return LoadMediaManifestPins(path)
}

// Enabled reports whether pins are actually loaded. Distinct from nil so a
// legitimately empty manifest (no entries) is not confused with "opt-in off".
func (p *MediaManifestPins) Enabled() bool { return p != nil }

// AppliesTo scopes the pins to the project that authored them. A manifest
// without scope keeps the legacy global behavior; a scoped manifest only
// applies when the plan title/topic carries one of its markers.
func (p *MediaManifestPins) AppliesTo(plan *scriptpkg.ResolvedGenerationPlan) bool {
	if p == nil || plan == nil {
		return false
	}
	if len(p.titleContains) == 0 && len(p.topicContains) == 0 {
		return true
	}
	return containsAnyMarker(plan.Title, p.titleContains) || containsAnyMarker(plan.Topic, p.topicContains)
}

func containsAnyMarker(haystack string, markers []string) bool {
	haystack = strings.ToLower(strings.TrimSpace(haystack))
	if haystack == "" {
		return false
	}
	for _, marker := range markers {
		marker = strings.ToLower(strings.TrimSpace(marker))
		if marker != "" && strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

// EntryCount reports the number of pinned entries across all scenes. It is
// wiring-level observability only: does the opt-in carry a non-empty inventory.
func (p *MediaManifestPins) EntryCount() int {
	if p == nil {
		return 0
	}
	n := 0
	for _, entries := range p.byScene {
		n += len(entries)
	}
	return n
}

// ForSceneID returns the raw pinned entries for a scene or segment id (the
// stage feeds the segment's SceneID first, then SegmentID; entry scene_id is
// normalized lowercase so authoring tools can mix cases freely).
func (p *MediaManifestPins) ForSceneID(sceneOrSegmentID string) []mediaManifestEntry {
	if p == nil {
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(sceneOrSegmentID))
	return p.byScene[key]
}

// pinCandidates converts manifest entries into discovery candidates that carry
// the verdicts the runtime already trusts: SemanticStatus accepted +
// SemanticScore 1.0 (REUSABLE verdict — no VLM call), ContentSHA256 (binding),
// and DriveLink/SourceURL for materialization. The candidates are stamped with
// the OWNING segment's identity (SegmentID/Position/TextHash), not the scene
// key they were pinned under: provenance binding is segment-scoped and a
// candidate bound to the scene id is dropped by normalizeVidRushCandidate.
// Positions are dense from 0 so downstream binding sees a deterministic
// ordering.
func (p *MediaManifestPins) pinCandidates(seg scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	entries := p.ForSceneID(seg.SceneID)
	if len(entries) == 0 {
		entries = p.ForSceneID(seg.SegmentID)
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(entries))
	for _, entry := range entries {
		out = append(out, segmentAssetCandidateFromPin(entry, seg, len(out)))
	}
	return out
}

func segmentAssetCandidateFromPin(entry mediaManifestEntry, seg scriptpkg.VidRushSegmentResult, position int) scriptpkg.SegmentAssetCandidate {
	sourceURL := strings.TrimSpace(entry.Source.SourceURL)
	link := sourceURL
	if link == "" {
		if id := strings.TrimSpace(entry.Source.DriveFileID); id != "" {
			if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") {
				link = id
			} else {
				// Canonical Drive direct-download form; the materializer can
				// fetch bytes straight away without a redirect hop.
				link = "https://drive.google.com/uc?export=view&id=" + id
			}
		}
	}
	// SemanticStatus accepted + SemanticScore 1.0 is the durable-verdict
	// encoding the entity catalog owns (ReusablePersonImageVerdict). A pin IS
	// an offline verdict: ops/tools/media_manifest.py verified the physical
	// bytes against this exact sha256 before the manifest was ever released
	// to the runtime.
	candidate := scriptpkg.SegmentAssetCandidate{
		SegmentID: strings.TrimSpace(seg.SegmentID),
		Position:  seg.Position,
		TextHash:  strings.TrimSpace(seg.TextHash),
		AssetID:   fmt.Sprintf("manifest_%s_%s", entry.ID, entry.SHA256[:12]), Provider: MediaManifestProvider,
		Entity:                 strings.TrimSpace(entry.Entity),
		Query:                  entry.Query,
		SemanticStatus:         entitycatalog.CandidateSemanticAccepted,
		SemanticScore:          1.0,
		QualityReason:          "pinned by media_manifest.v1: offline sha256 verify",
		SourceURL:              sourceURL,
		DriveLink:              link,
		ContentSHA256:          strings.ToLower(entry.SHA256),
		AcquisitionStatus:      scriptpkg.VidRushStatusAcquired,
		VerificationStatus:     scriptpkg.VidRushStatusVerified,
		PersistenceStatus:      scriptpkg.VidRushStatusPersisted,
		SelectionReason:        "media_manifest pin",
		LifecycleFailureStage:  "",
		LifecycleFailureReason: "",
	}
	candidate.ClearLifecycleFailure()
	return candidate
}

// WithMediaManifestPins attaches loaded pins. This is the opt-in switch used
// by the composition root; without it the stage is byte-for-byte unchanged.
func (proc *MediaResolverImageStage) WithMediaManifestPins(pins *MediaManifestPins) *MediaResolverImageStage {
	if proc != nil {
		proc.manifestPins = pins
	}
	return proc
}

// WithMediaManifestPins attaches loaded pins to the provider fan-out. The
// fan-out is the discovery owner on plans that do not schedule the
// internet_images postprocessor, so the fast path must exist on both surfaces.
func (f *VidRushProviderFanout) WithMediaManifestPins(pins *MediaManifestPins) *VidRushProviderFanout {
	if f != nil {
		f.manifestPins = pins
	}
	return f
}

// pinnedCandidates returns pre-verified candidates for a pinned segment.
func (f *VidRushProviderFanout) pinnedCandidates(plan *scriptpkg.ResolvedGenerationPlan, seg scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	if f == nil || f.manifestPins == nil || !f.manifestPins.Enabled() || !f.manifestPins.AppliesTo(plan) {
		return nil
	}
	return f.manifestPins.pinCandidates(seg)
}

// pinnedInternetImageState returns pre-verified candidates for a segment when
// the manifest pins it — the entire provider/VLM/search surface is skipped.
func (proc *MediaResolverImageStage) pinnedInternetImageState(plan *scriptpkg.ResolvedGenerationPlan, seg scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	if proc == nil || proc.manifestPins == nil || !proc.manifestPins.Enabled() || !proc.manifestPins.AppliesTo(plan) {
		return nil
	}
	return proc.manifestPins.pinCandidates(seg)
}
