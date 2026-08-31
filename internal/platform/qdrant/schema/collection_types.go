// collection_types.go — Collection-level types: HTTP wire shapes,
// runtime-facing configuration, and reindex/verify reports (PR3 split).
//
// PR3 mechanical split (June 2026): relocated from types.go without
// signature or behaviour changes. Two distinct kinds of "collection"
// coexist here and that naming is intentional:
//
//   - CollectionInfo is the canonical wire-decoded /collections/{n}
//     GET response. The decoder (UnmarshalJSON) lives in
//     collection_wire.go so this file carries only the public type
//   - helpers, no JSON plumbing.
//   - Config is the runtime Qdrant-client configuration (BaseURL,
//     APIKey, Timeout, Enabled, CollectionVersion, retention). It is
//     NOT a schema manifest — schema manifests live on IndexSchema
//     (schema_types.go). Config is the "where to reach Qdrant +
//     which schema version" cross-cutting runtime knob.
//
// Two actor surfaces also live here: reindex reports (ReindexResult
// + SwitchReport from the verifier) and the LocatorCleanupReport
// (QDRANT-005 cleanup scan). SchemaDiff + DimensionDiff + DistanceDiff
// are the validate-schema artefacts consumed by CollectionManager's
// CompareSchema path and the typed-error ErrSchemaIncompatible
// (which lives in errors.go — same package, so cross-file type
// reference resolves cleanly).
package schema

import (
	"fmt"
	"strings"

	qdrantdr "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/qdrantdr"
)

// ── Config ───────────────────────────────────────────────────────────

// Config holds Qdrant client configuration.
//
// QDRANT-001 closure (June 2026): the legacy fields below were
// removed. Channel names and dimensions are owned by IndexSchema
// (the Single Source Of Truth for the V3 manifest) — the Client no
// longer carries per-channel settings. List of removed fields:
//
//   - URL (legacy alias for BaseURL; use BaseURL)
//   - TimeoutMs (legacy ms variant; use Timeout in seconds)
//   - RetryMaxAttempts (no live consumer; NewClient only sets Timeout)
//   - Collection / CollectionAlias / DisableAlias (legacy flat-name
//     routing; the canonicalisable physical-name + runtime-alias pair
//     lives on IndexSchema.PhysicalName / IndexSchema.RuntimeAlias and
//     is consumed by IndexWriter / CollectionManager)
//   - TextVectorName/TranscriptVectorName/VisualVectorName/AudioVectorName/
//     SparseVectorName (channel lives on IndexSchema.DenseVectors /
//     SparseVectors)
//   - TextDimensions/TranscriptDimensions/VisualDimensions/AudioDimensions
//     (dimensions live on IndexSchema.DenseVectors.Dimensions)
//   - EmbeddingServerURL (the sidecar URL is configured on
//     cfg.ClipIndexer.ServerURL, not on the Qdrant client)
//
// Survivor fields below tell the runtime how to reach Qdrant + whether
// it is enabled; everything else is encoded in IndexSchema and the
// schema-versioning ratchet (see architecture/current.yaml).
type Config struct {
	// BaseURL is the Qdrant REST API base URL (e.g. "http://127.0.0.1:6333").
	BaseURL string `yaml:"base_url"`

	// APIKey is an optional Qdrant API key.
	APIKey string `yaml:"api_key"`

	// Timeout is the HTTP client timeout in seconds.
	Timeout int `yaml:"timeout"`

	// Enabled is whether Qdrant integration is active.
	Enabled bool `yaml:"enabled"`

	// CollectionVersion pins a schema version (e.g. "v3"). The
	// IndexSchema that maps channels + aliases to physical collection
	// names is selected by this tag.
	CollectionVersion string `yaml:"collection_version"`

	// ProjectionRetention is the number of known-good projection
	// collections to retain after an alias switch (active + N-1 rollback).
	// Plumbed from config.QdrantConfig.ProjectionRetention
	// (QDRANT_PROJECTION_RETENTION). 0 disables the automatic post-switch
	// retention sweep.
	ProjectionRetention int `yaml:"projection_retention"`

	// CollectionRetentionDays is how many days to keep old collections
	// after a reindex switch. (The OldTarget retention policy is
	// advisory today: operator runbooks track the canonical
	// `retention` schedule.)
	CollectionRetentionDays int `yaml:"collection_retention_days"`
}

// DefaultConfig returns a safe default configuration.
func DefaultConfig() *Config {
	return &Config{
		BaseURL:                 "http://127.0.0.1:6333",
		Timeout:                 10,
		CollectionRetentionDays: 7,
	}
}

// ProductionCollection is the only Qdrant collection that runtime data
// paths may read or write. Versioned/candidate/recovery collections are
// rebuild or emergency artifacts, never runtime truth.
const ProductionCollection = "media_assets"

// CanonicalRuntimeAlias is the control-plane alias associated with the
// production projection. Runtime readers must resolve it to ProductionCollection.
const CanonicalRuntimeAlias = "media_assets_current"

// IsRuntimeCollection reports whether name is the single production
// collection allowed on runtime data paths.
func IsRuntimeCollection(name string) bool {
	return strings.TrimSpace(name) == ProductionCollection
}

// ValidateRuntimeCollection rejects every collection except production.
func ValidateRuntimeCollection(name string) error {
	if !IsRuntimeCollection(name) {
		return fmt.Errorf("collection %q is forbidden on runtime paths; only %q is allowed", name, ProductionCollection)
	}
	return nil
}

// ValidateProjectionTarget validates a physical build target. It is broader
// than ValidateRuntimeCollection because rebuild tooling may prepare a
// non-runtime candidate, but it still excludes recovery, test, synthetic and
// the runtime alias from normal projection lifecycle state.
func ValidateProjectionTarget(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == CanonicalRuntimeAlias {
		return fmt.Errorf("collection %q is not a valid projection target", name)
	}
	for _, token := range []string{"recovery", "synthetic", "test"} {
		if strings.Contains(name, token) {
			return fmt.Errorf("collection %q is reserved for emergency or fixture use", name)
		}
	}
	if name == ProductionCollection || strings.HasPrefix(name, "media_assets_") || strings.HasPrefix(name, "candidate-") {
		return nil
	}
	return fmt.Errorf("collection %q is not a recognized media-assets projection target", name)
}

// ── Collection info (public wire surface) ─────────────────────────────

// CollectionInfo describes a Qdrant collection as seen by the REST API.
//
// PR1 — fix/qdrant-wire-contracts: the public surface is unchanged so
// downstream consumers (CompareSchema, CollectionManager, readiness,
// admin CLI) keep their call sites. The MarshalJSON surface is the
// internal Qdrant wire envelope: nested under result.* with the
// canonical fields documented at https://api.qdrant.tech/api-reference/collections/get-collection.
//
// Why the change: pre-PR1 the decoder expected a flat shape
// (`name`, `status`, `vectors_count`, `config`, `payload_indexes`,
// `points_count` all at top level). Qdrant actually returns:
//
//	{ "result": {
//	    "status": "green",
//	    "vectors_count": 1064,
//	    "points_count": 1064,
//	    "config": { "params": {
//	        "vectors":         {<channel>: {"size": 768, "distance": "Cosine"}},
//	        "sparse_vectors":  {<channel>: {"modifier": "bm25"}}
//	    }},
//	    "payload_schema": {<field>: {"data_type": "keyword"}}
//	}}
//
// The UnmarshalJSON below maps both nested paths into the same public
// fields so CompareSchema's `actual.VectorConfigs[name].Size` and
// `actual.PayloadIndexes[]` keep working byte-for-byte.
type CollectionInfo struct {
	Name           string                  `json:"name"`
	Status         string                  `json:"status"`
	VectorsCount   int                     `json:"vectors_count"`
	PointTotal     int                     `json:"points_count"`
	VectorConfigs  map[string]VectorConfig `json:"-"`
	PayloadIndexes []PayloadIndexInfo      `json:"-"`
	SparseConfigs  map[string]SparseConfig `json:"-"`
}

// SparseConfig mirrors Qdrant's per-sparse-vector configuration.
// Sparse vectors do not carry size/distance — they carry a modifier
// ("bm25" | "splade") and (when present) inference config for
// server-side embedding generation.
type SparseConfig struct {
	Modifier string `json:"modifier,omitempty"`
	Model    string `json:"model,omitempty"`
}

// VectorConfig mirrors Qdrant's per-vector configuration.
type VectorConfig struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"`
}

// PayloadIndexInfo describes a single payload index.
type PayloadIndexInfo struct {
	FieldName string `json:"field_name"`
	FieldType string `json:"field_type"`
}

// sortPayloadIndexes sorts the slice by FieldName for deterministic
// diff output (so CompareSchema's MissingIndexes / ExtraIndexes lists
// match the same order between two otherwise equal CollectionInfo
// values, regardless of the JSON object map iteration order). Keeps
// signature stable for call-site readability.
func sortPayloadIndexes(items []PayloadIndexInfo) {
	// Inline insertion sort — slices are small (tens of fields).
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j-1].FieldName > items[j].FieldName; j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
}

// ── Schema validation artefacts ──────────────────────────────────────

// SchemaDiff reports the differences between expected and actual schemas.
type SchemaDiff struct {
	Compatible          bool            `json:"compatible"`
	MissingVectors      []string        `json:"missing_vectors,omitempty"`
	ExtraVectors        []string        `json:"extra_vectors,omitempty"`
	DimensionMismatches []DimensionDiff `json:"dimension_mismatches,omitempty"`
	DistanceMismatches  []DistanceDiff  `json:"distance_mismatches,omitempty"`
	MissingIndexes      []string        `json:"missing_indexes,omitempty"`
	ExtraIndexes        []string        `json:"extra_indexes,omitempty"`
}

// DimensionDiff records a vector whose dimensions don't match expectations.
type DimensionDiff struct {
	Channel  string `json:"channel"`
	Expected int    `json:"expected"`
	Actual   int    `json:"actual"`
}

// DistanceDiff records a vector whose distance metric doesn't match.
type DistanceDiff struct {
	Channel  string `json:"channel"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// ── Reindex/verify/scan reports ───────────────────────────────────────

// ReindexResult holds the outcome of a reindex operation.
type ReindexResult struct {
	TotalAssets      int      `json:"total_assets"`
	IndexedAssets    int      `json:"indexed_assets"`
	FailedAssets     int      `json:"failed_assets"`
	FailedAssetIDs   []string `json:"failed_asset_ids,omitempty"`
	TargetCollection string   `json:"target_collection"`
	DryRun           bool     `json:"dry_run"`
	// SQLiteIndexableAssets is the number of rows selected by the
	// canonical SearchIndexEligibilitySQL predicate for this rebuild.
	// It is kept separate from TotalAssets so the report cannot
	// accidentally imply that ineligible catalog rows were projected.
	SQLiteIndexableAssets int `json:"sqlite_indexable_assets"`
}

// MaxErrors is the safety cap for the report.Errors slice. Beyond this
// threshold, ErrorsTruncated is set true and further diagnostics are
// dropped from the payload but still counted by their respective
// gate-level counters. The cap prevents unbounded memory growth on
// catastrophically corrupt collections while keeping the counter totals
// accurate.
const MaxErrors = 500

// MaxMissingOrphanIDs is the safety cap for MissingIDs / OrphanIDs.
// Beyond this threshold the respective Truncated flag is set.
const MaxMissingOrphanIDs = 1000

// SwitchReport is the pre-switch verification report.
//
// PR 12 (June 2026) extensions: three new fields expose the strict
// gate footprint that PR 12 enforces:
//
//   - CompleteScan: true iff every scroll page succeeded AND the
//     maxScrolls safety cap was not hit AND the trailing NextOffset
//     was empty at the end of the loop. Mirrors PR 10's
//     ScannedTotals.CompleteScan vocabulary so the JSON-shape
//     surface is consistent across the (reconciler, verifier)
//     couple. False ⇒ the report is partial; consumers must NOT
//     gate on counts in that case.
//
//   - TotalScrolled: the canonical number of points observed by
//     the verifier. Differs from ActualPoints only in degenerate
//     cases (e.g. the verifier scanned fewer points than the
//     CountPoints endpoint reported due to early-failure). The
//     strict pt.ID and per-channel gates apply on TotalScrolled;
//     if TotalScrolled != ActualPoints the OPERATOR must inspect
//     Errors — the count is suspect.
//
//   - NonCanonicalPointCount + NonCanonicalPointIDs: pt.ID must
//     equal AssetIDToQdrantPointID(payload["asset_id"]) literally.
//     A generic UUID-parseable id (the previous behaviour) is no
//     longer accepted — only the canonical boundary can locate a
//     Qdrant point via our reverse-mapping, so a generic-UUID
//     substitute silently lost the read path.
//
// Task 7 (July 2026) hardening — zero-errors gate + structured details:
//
//   - GateDetails: per-gate pass/fail breakdown with human-readable
//     descriptions. Operators see exactly which gate(s) blocked the
//     alias switch without parsing the raw Errors list.
//   - ErrorsTruncated: true when the Errors slice hit MaxErrors.
//     The counter totals (MissingCount, PayloadIssues, etc.) still
//     reflect the full scan; the Errors list is a diagnostic sample.
//   - MissingTruncated / OrphanTruncated: true when the respective
//     ID list hit MaxMissingOrphanIDs.
//   - Zero-errors gate: Ready requires len(Errors)==0 AND
//     !ErrorsTruncated — a truncated list is evidence of an
//     incomplete diagnostic surface and blocks the switch.
type SwitchReport struct {
	TargetCollection string `json:"target_collection"`
	// SQLiteIndexableAssets is the authoritative source cardinality for
	// this candidate. It is loaded from SearchIndexEligibilitySQL and
	// is the count used by the rebuild gate; writer success counts must
	// never redefine the expected projection set.
	SQLiteIndexableAssets int `json:"sqlite_indexable_assets"`
	ExpectedPoints        int `json:"expected_points"`
	ActualPoints          int `json:"actual_points"`
	// CompleteScan: see type doc. Initialised to false; flipped true
	// only when the verifier ran the full scroll loop without any
	// truncating condition (page error | cap hit | trailing NextOffset).
	CompleteScan    bool     `json:"complete_scan"`
	TotalScrolled   int      `json:"total_scrolled"`
	MissingCount    int      `json:"missing_count"`
	OrphanCount     int      `json:"orphan_count"`
	MissingIDs      []string `json:"missing_ids,omitempty"`
	OrphanIDs       []string `json:"orphan_ids,omitempty"`
	PayloadIssues   int      `json:"payload_issues"`
	VersionMismatch int      `json:"version_mismatch"`
	// VersionMismatchPerChannel (QDRANT-003, June 2026, "versioni embedding
	// per canale") breaks the global VersionMismatch counter down by
	// vector channel. Key is the channel name (e.g. "text", "visual",
	// "audio", "transcript"); value is the count of sampled points whose
	// payload["embedding_version_<channel>"] does NOT match the schema's
	// EmbeddingSpec.ModelVersion AND were not rescued by the
	// legacy-global-fallback (see verifier.go). Empty map means: every
	// point carried the expected per-channel model version (or the legacy
	// global fallback honoured the global schema version).
	//
	// PR 12: the per-channel check runs on EVERY scrolled page (no
	// 1000-point sample). The per-channel counter therefore reflects
	// the full collection, not a sample.
	VersionMismatchPerChannel map[string]int `json:"version_mismatch_per_channel,omitempty"`
	GoldenQueriesOK           bool           `json:"golden_queries_ok"`
	FiltersOK                 bool           `json:"filters_ok"`
	DeadLetterOpen            int            `json:"dead_letter_open"`
	// NonCanonicalPointCount + NonCanonicalPointIDs (PR 12): points
	// whose pt.ID != AssetIDToQdrantPointID(payload["asset_id"]). Any
	// such point is BLOCKING — the alias switch must not proceed.
	NonCanonicalPointCount int `json:"non_canonical_point_count"`
	// NonCanonicalTruncated is true when NonCanonicalPointIDs' slice
	// cap-threshold (currently 20) truncated the canonical-list
	// entries below NonCanonicalPointCount. Operators reading the
	// JSON should consult the count first; the slice is a
	// sample-of-the-first-20 for human-readable diagnostics.
	NonCanonicalTruncated bool     `json:"non_canonical_truncated,omitempty"`
	NonCanonicalPointIDs  []string `json:"non_canonical_point_ids,omitempty"`
	// DuplicateQdrantPoints (PR-HASH-SEMANTICS item 14): the number of
	// extra Qdrant points (beyond the first) sharing the same canonical
	// payload.asset_id. The projection invariant is 1 canonical asset =
	// 1 point; a non-zero count blocks the alias switch.
	DuplicateQdrantPoints int `json:"duplicate_qdrant_points"`
	// DuplicateTruncated is true when DuplicatePointIDs' cap-threshold
	// (20) truncated the diagnostic list below DuplicateQdrantPoints.
	DuplicateTruncated bool     `json:"duplicate_truncated,omitempty"`
	DuplicatePointIDs  []string `json:"duplicate_point_ids,omitempty"`
	// RollbackTarget (PR 13) carries the active alias target that
	// was in place BEFORE the verification attempt. On Ready=false
	// the operator's PR 13 path retains this collection so a future
	// --apply can re-promote it (or a manual alias switch undoes the
	// blue-green swap). Empty when the cmd path cannot resolve the
	// active alias (e.g. recovery from a missing-runtime-alias scenario).
	RollbackTarget string `json:"rollback_target,omitempty"`
	// OldCollection is the timestamped collection the blue-green
	// reindex was ABOUT TO swap away from (PR 13). Set by command
	// pre-switch; the suffix distinguishes "currently active" from
	// "previously active" rolling snapshots.
	OldCollection    string   `json:"old_collection,omitempty"`
	Ready            bool     `json:"ready"`
	Errors           []string `json:"errors,omitempty"`
	ErrorsTruncated  bool     `json:"errors_truncated,omitempty"`
	MissingTruncated bool     `json:"missing_truncated,omitempty"`
	OrphanTruncated  bool     `json:"orphan_truncated,omitempty"`
	// GateDetails is the per-gate pass/fail breakdown (Task 7).
	// Populated by the verifier metadata phase; nil when the
	// verifier did not reach the metadata phase.
	GateDetails *GateDetails `json:"gate_details,omitempty"`
}

// GateDetails is the structured per-gate pass/fail report (Task 7).
// Every gate carries a Passed bool and a human-readable Description
// so operators can diagnose which condition(s) blocked the alias
// switch without parsing the raw Errors list.
type GateDetails struct {
	PointCountParity  GateDetail `json:"point_count_parity"`
	CompleteScan      GateDetail `json:"complete_scan"`
	MissingOrphan     GateDetail `json:"missing_orphan"`
	PayloadValidation GateDetail `json:"payload_validation"`
	EmbeddingVersion  GateDetail `json:"embedding_version"`
	CanonicalPointID  GateDetail `json:"canonical_point_id"`
	DuplicatePoints   GateDetail `json:"duplicate_points"`
	DeadLetters       GateDetail `json:"dead_letters"`
	GoldenQueries     GateDetail `json:"golden_queries"`
	FilterSmoke       GateDetail `json:"filter_smoke"`
	ZeroErrors        GateDetail `json:"zero_errors"`
}

// GateDetail describes a single verification gate outcome.
type GateDetail struct {
	Passed      bool   `json:"passed"`
	Description string `json:"description"`
}

// LocatorCleanupReport is a shared pure-data alias. The canonical shape
// lives in internal/domain/qdrantdr so application maintenance and the
// infrastructure cleaner can pass the report without manual field copies.
type LocatorCleanupReport = qdrantdr.LocatorCleanupReport
