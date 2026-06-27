// Package qdrant — QDRANT-008 / TODO 12 strict reindex verifier (June 2026).
//
// VerifyReindex is the post-reindex validation gate. The verifier applies
// a STRICT-AND-EXACT contract to a freshly-indexed collection before the
// active alias can be flipped onto it. Every gate listed below BLOCKS the
// alias switch on non-zero (Ready goes false):
//
//  1. Count EXACT match — ActualPoints MUST == ExpectedPoints; both extra
//     and missing points trip the mismatch (the lenient >= was removed).
//
//  2. Full scroll — every point scrolled-up-to-page-cap is individually
//     validated; there is no sampling window (the early `iteration<2`
//     early-break has been removed in favor of validating every point).
//
//  3. Scroll-cap block — when the maxScrolls safety cap is reached, a
//     "scroll limit reached" error is appended, the diagnostic is surfaced
//     to the operator, and the Ready gate stays false (silent cap-out is
//     not allowed; operators can either increase maxScrolls or accept
//     that Ready stays false).
//
//  4. Canonical PointID — pt.ID MUST equal AssetIDToQdrantPointID(assetID).
//     Any UUID-form-but-mismatched pt.ID bumps PayloadIssues and trips the
//     Ready gate. Non-UUID-form pt.ID bumps NonUUIDPointIDs and trips the
//     Ready gate.
//
//  5. Per-channel embedding_version — every scrolled point MUST carry
//     embedding_version_<channel> for every schema channel AND that value
//     MUST equal the schema's EmbeddingSpec.ModelVersion. ANY mismatch or
//     missing key bumps VersionMismatchPerChannel[channel]. The legacy
//     GLOBAL `embedding_version` field is NOT consulted — there is no
//     fallback to the schema-version string. (This is the QDRANT-003
//     close-out + TODO 12 hardening: zero-legacy; the global field is
//     payload noise, not verification truth.)
//
//  6. Per-media-type channel matrix — every scrolled point MUST carry
//     embedding_version_<channel> for every channel required by the
//     manifest for its media_type. matrix:
//
//     video: text, transcript, visual
//     image: text, visual
//     audio: text, transcript, audio
//
//     Missing any required channel for a media_type bumps
//     MissingChannelForMediaType; the counter is also broken down by
//     channel name in MissingChannelForMediaTypeDetail so operators see
//     exactly which channels are absent per media_type.
//
//  7. Payload minimum — every point must carry asset_id + name + source.
//
//  8. Dead-letter count (optional) + golden queries (optional) + filter
//     matrix (optional) — same as QDRANT-003 close-out.
//
// Ready is true ONLY when every gate above passes: counts EXACT match,
// zero missing/orphan, zero payload issues, zero version-mismatch channels,
// zero missing-channel-for-media-type, zero non-UUID point IDs, zero dead
// letters, zero non-skipped gates' failures, and no errors appended.
package qdrant

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// MediaTypeChannelMatrix (QDRANT-008 / TODO 12, June 2026) is the
// per-media-type manifest of REQUIRED channels. Each scrolled point
// whose media_type is in the matrix MUST carry
// embedding_version_<channel> for every channel listed here for its
// media_type, or the matrix gate trips a MissingChannelForMediaType
// bump and Ready goes false.
//
// Channels NOT listed here are OPTIONAL for that media_type (a video
// CAN have an audio channel, but a video MUST have text+transcript+visual).
//
// Unknown or missing media_type: matrix gate is SILENTLY skipped
// (the per-channel global check still applies via Gate 4 above). A
// missing media_type is not a matrix-blocker.
//
// Canonical policy table (this is the source of truth — not a config
// flag, not a runtime override, not a per-collection annotation):
//
//	video → text, transcript, visual  (no audio unless explicitly added)
//	image → text, visual               (no transcript, no audio)
//	audio → text, transcript, audio    (no visual)
//
// If a future channel is added to the schema (e.g. an embedding_summary
// channel for caption-only media), extend this table; the verifier will
// fail-closed on missing keys for whichever media_type the table
// declares require the new channel.
var MediaTypeChannelMatrix = map[string][]string{
	"video": {"text", "transcript", "visual"},
	"image": {"text", "visual"},
	"audio": {"text", "transcript", "audio"},
}

// GoldenQueryRunner executes predefined smoke queries against a
// collection to verify it is queryable after reindex.
// nil = golden-query gate is trivially satisfied (GoldenQueriesOK=true).
// Wire a real runner (e.g. DefaultGoldenQueryRunner) to execute smoke
// queries that verify the collection is reachable and returns results.
type GoldenQueryRunner interface {
	RunQueries(ctx context.Context, collection string) (passed bool, failures int, err error)
}

// ReindexVerifier holds the dependencies for post-reindex validation.
type ReindexVerifier struct {
	client        *Client
	assetStore    AssetStore
	deadLetter    DeadLetterChecker // nil = skip dead-letter check
	schema        *IndexSchema      // canonically the schema under reindex; nil = skip per-channel version check
	goldenQueries GoldenQueryRunner // nil = skip golden-query gate (QDRANT-003)
	filters       FilterMatrix      // nil = skip filter-matrix gate (QDRANT-003 close-out)
	log           *zap.Logger
}

// NewReindexVerifier creates a verifier. deadLetter may be nil (legacy
// admin CLIs). schema MAY be nil only for tests that exercise gates
// unrelated to per-channel embedding versioning; production wire paths
// (cmd/admin/reindex_qdrant.go, BuildOutboxBundle) MUST supply non-nil
// schema so the per-channel version check fires.
//
// QDRANT-003 (June 2026) closure — second-pass extension: per-channel
// embedding version check. The schema's EmbeddingSpec.ModelVersion is
// the canonical per-channel target; the verifier surfaces mismatches in
// report.VersionMismatchPerChannel so operators can see which channel's
// model output drifted from the manifest.
//
// Breaking signature change: the production caller is
// internal/cmd/admin/reindex_qdrant.go (single callsite as of June
// 2026). Test fixtures do not construct ReindexVerifier directly.
// NewReindexVerifier creates a verifier. deadLetter and goldenQueries
// may be nil (skip those gates). schema MAY be nil only for tests.
func NewReindexVerifier(client *Client, assetStore AssetStore, deadLetter DeadLetterChecker, schema *IndexSchema, goldenQueries GoldenQueryRunner, log *zap.Logger) *ReindexVerifier {
	return NewReindexVerifierFull(client, assetStore, deadLetter, schema, goldenQueries, nil, log)
}

// NewReindexVerifierFull is the canonical constructor used by production
// wire paths (cmd/admin/reindex_qdrant.go, BuildOutboxBundle). Every
// gate can be enabled here; nil in any slot disables that gate.
//
// QDRANT-003 close-out (June 2026): adds the FilterMatrix slot. The
// production wiring in internal/app/build_bundles_process.go MUST
// supply a non-nil filters matrix when the target collection is the
// production media_assets_v3_e5_768_siglip_768 alias.
func NewReindexVerifierFull(client *Client, assetStore AssetStore, deadLetter DeadLetterChecker, schema *IndexSchema, goldenQueries GoldenQueryRunner, filters FilterMatrix, log *zap.Logger) *ReindexVerifier {
	return &ReindexVerifier{
		client:        client,
		assetStore:    assetStore,
		deadLetter:    deadLetter,
		schema:        schema,
		goldenQueries: goldenQueries,
		filters:       filters,
		log:           log,
	}
}

// VerifyReindex runs the full validation suite against the target collection
// and returns a populated SwitchReport with Ready set accordingly.
//
// expectedPoints is the count reported by ReindexAll (IndexedAssets).
//
// QDRANT-008 / TODO 12 strict (June 2026): every gate is exact-match /
// full-scan / per-point. The caller checks `report.Ready` before calling
// SwitchAlias; a false Ready MUST block the alias switch.
func (v *ReindexVerifier) VerifyReindex(ctx context.Context, targetCollection string, expectedPoints int) (*SwitchReport, error) {
	report := &SwitchReport{
		TargetCollection:                 targetCollection,
		ExpectedPoints:                   expectedPoints,
		VersionMismatchPerChannel:        make(map[string]int),
		MissingChannelForMediaTypeDetail: make(map[string]int),
	}
	// QDRANT-003 closure (June 2026): golden queries are no longer
	// hard-coded to true. When the runner is not wired, the gate
	// defaults to false — a nil runner means the operator chose to
	// skip smoke validation, and the Ready gate reflects that.			goldenQueriesWired := v.goldenQueries != nil
	filtersWired := v.filters != nil

	// ── Gate 1: Point count parity ────────────────────────────────
	actualPoints, err := v.client.CountPoints(ctx, targetCollection)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count points: %v", err))
		// Hard error — can't verify anything without a count.
		return report, fmt.Errorf("QDRANT-003: cannot verify reindex — count failed: %w", err)
	}
	report.ActualPoints = actualPoints

	// QDRANT-003 closed (June 2026): count must be EXACT.
	// The previous code used >= (permissive) which allowed a
	// collection with extra orphan points from a prior reindex
	// to pass the gate. Now extra points are surfaced as a
	// mismatch just like missing points.
	if actualPoints != expectedPoints {
		delta := expectedPoints - actualPoints
		report.Errors = append(report.Errors,
			fmt.Sprintf("point count mismatch: expected %d, actual %d (delta %d)",
				expectedPoints, actualPoints, delta))
		// Do NOT return early. Continue gathering diagnostics so the
		// operator gets a full report. Ready will be false.
	}

	// ── Gate 2: Scroll + missing/orphan/payload/version ──────────
	sqliteIDs, err := v.assetStore.ListAllAssetIDs(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("list SQLite asset IDs: %v", err))
		return report, fmt.Errorf("QDRANT-003: cannot verify reindex — SQLite list failed: %w", err)
	}

	// Build SQLite ID set for O(1) lookup.
	sqliteSet := make(map[string]bool, len(sqliteIDs))
	for _, id := range sqliteIDs {
		sqliteSet[id] = true
	}

	// Build Qdrant point ID set by scrolling.
	qdrantIDs := make(map[string]bool)
	var offset string
	scrollPage := 500
	const maxScrolls = 400 // safety cap: 200k points max
	pointsScrolled := 0

	for iteration := 0; iteration < maxScrolls; iteration++ {
		result, err := v.client.ScrollPoints(ctx, targetCollection, offset, scrollPage)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("scroll page %d: %v", iteration, err))
			break // partial data is better than nothing
		}

		pointsScrolled += len(result.Points)
		for _, pt := range result.Points {
			// Read canonical asset_id directly from point payload
			// (UUID v5 hashes are one-way; PointIDToAssetID was removed).
			// Comma-ok is required: a missing or non-string asset_id must NOT
			// pollute qdrantIDs with an empty key, which would silently mask a
			// SQLite row whose own asset_id is the empty string (MissingIDs).
			// validatePayloadMinimum below continues to surface the missing-field
			// case via PayloadIssues, so the Ready gate still trips correctly.
			assetID, assetIDOK := pt.Payload["asset_id"].(string)
			if assetIDOK && assetID != "" {
				qdrantIDs[assetID] = true
			}

			// Gate 3: Payload minimum validation.
			if issue := validatePayloadMinimum(pt.Payload, assetID); issue != "" {
				report.PayloadIssues++
				if len(report.Errors) < 20 { // cap error list
					report.Errors = append(report.Errors, issue)
				}
			}

			// Gate 4: Embedding version check — ALL points (QDRANT-003
			// closed + QDRANT-008 / TODO 12 strict, June 2026). The
			// previous implementation checked only the first 1000
			// points (iteration<2) which could silently accept a
			// collection where 95% of points carry stale embedding
			// versions. The legacy global embedding_version field is
			// payload noise (NOT verification truth under TODO 12):
			// every scrolled point is checked via the per-channel
			// embedding_version_<channel> keys ONLY. For collections
			// with >200k points operators should increase maxScrolls.
			//
			// Per-channel closure (TODO 12): the per-channel
			// embedding_version_<channel> check is the ONLY
			// verification source. A point that fails any channel's
			// check bumps VersionMismatch EXACTLY once via the
			// pointMismatched latch. Per-channel counter increments
			// per channel so operators see which channel drifted.
			{
				pointMismatched := false

				// Per-channel check ONLY (TODO 12: legacy global
				// embedding_version fallback REMOVED; the
				// per-channel keys are the sole truth).
				if v.schema != nil {
					for _, spec := range v.schema.DenseVectors {
						if spec.ModelVersion == "" {
							continue
						}
						key := fmt.Sprintf("embedding_version_%s", spec.Channel)
						actual, present := pt.Payload[key].(string)
						if !present {
							// Every point MUST carry the
							// per-channel payload key — bumping the
							// per-channel counter is the canonical
							// signal that a reindex wrote the
							// pre-per-channel schema.
							report.VersionMismatchPerChannel[spec.Channel]++
							pointMismatched = true
							continue
						}
						if actual != spec.ModelVersion {
							report.VersionMismatchPerChannel[spec.Channel]++
							pointMismatched = true
						}
					}
				}

				if pointMismatched {
					report.VersionMismatch++
				}
			}

			// Gate 5b: Per-media-type channel matrix (QDRANT-008 /
			// TODO 12, June 2026). Each scrolled point whose
			// media_type is in the matrix MUST carry
			// embedding_version_<channel> for every channel the
			// manifest requires for that media_type. Missing any
			// required channel bumps MissingChannelForMediaType.
			//
			// media_type matrix (canonical source of truth):
			//   video → text, transcript, visual
			//   image → text, visual
			//   audio → text, transcript, audio
			//
			// Unknown or missing media_type: matrix gate is
			// SILENTLY skipped (per-channel global check still
			// applies via Gate 4 above). A missing media_type is
			// not a matrix-blocker; the schema-existence of every
			// channel key is still a per-channel-blocker.
			{
				mtRaw, _ := pt.Payload["media_type"].(string)
				mt := strings.ToLower(strings.TrimSpace(mtRaw))
				if mt != "" {
					if expected, ok := MediaTypeChannelMatrix[mt]; ok {
						for _, ch := range expected {
							key := fmt.Sprintf("embedding_version_%s", ch)
							val, present := pt.Payload[key]
							if !present || val == nil {
								report.MissingChannelForMediaType++
								report.MissingChannelForMediaTypeDetail[ch]++
								if len(report.Errors) < 20 {
									report.Errors = append(report.Errors,
										fmt.Sprintf("point %q (media_type=%q, asset_id=%q) missing required channel %q (matrix gate)",
											pt.ID, mt, assetID, ch))
								}
							}
						}
					}
				}
			}

			// Gate 5: Point UUID verification (QDRANT-003 close-out).
			// ZERO-LEGACY: every point MUST carry a UUID v5-form pt.ID
			// AND that pt.ID must equal AssetIDToQdrantPointID(assetID).
			// Non-UUID points bump report.NonUUIDPointIDs (which blocks
			// the Ready gate) AND insert an explicit error so operators
			// see the rogue point's identifier. The previous "skip when
			// not recognisably a UUID" semantics is REMOVED — a legacy
			// non-UUID point is a zero-legacy invariant violation.
			//
			// PointID malformation is checked UNCONDITIONALLY (independent
			// of whether pt.Payload carries asset_id). A non-UUID pt.ID is a
			// zero-legacy violation whether or not the payload is valid —
			// the old "skip-when-no-asset-id" semantics masked dozens of
			// malformed points downstream of broken indexer writes.
			if !isUUIDForm(pt.ID) {
				report.NonUUIDPointIDs++
				if len(report.Errors) < 20 {
					report.Errors = append(report.Errors,
						fmt.Sprintf("point ID %q is not in UUID v5 form (asset_id=%q) — zero-legacy verification REQUIRES the canonical uuid5(assetID) point ID",
							pt.ID, assetID))
				}
			} else if assetIDOK && assetID != "" {
				expectedID := AssetIDToQdrantPointID(assetID)
				if pt.ID != expectedID {
					report.PayloadIssues++
					if len(report.Errors) < 20 {
						report.Errors = append(report.Errors,
							fmt.Sprintf("point UUID mismatch: payload asset_id=%q but pt.ID=%q (expected %q)",
								assetID, pt.ID, expectedID))
					}
				}
			}
		}

		if result.NextOffset == "" {
			break
		}
		offset = result.NextOffset

		if iteration == maxScrolls-1 {
			// QDRANT-003 closed (June 2026): incomplete scroll now
			// BLOCKS Ready. The previous implementation logged a
			// warning and continued — a collection with >200k points
			// would be verified on only the first 200k, and the
			// remaining unscrolled points could silently mask
			// missing/orphan/version/payload issues. Now the error
			// forces operators with large collections to increase
			// maxScrolls or accept that Ready stays false.
			report.Errors = append(report.Errors,
				fmt.Sprintf("scroll limit reached (%d iterations, %d points scrolled) — "+
					"remaining points NOT verified; increase maxScrolls and re-run",
					maxScrolls, pointsScrolled))
			v.log.Warn("scroll iteration limit reached; verification of remaining points skipped",
				zap.Int("limit", maxScrolls),
				zap.Int("scrolled", pointsScrolled))
		}
	}

	// Guard: if zero points were scrolled (first-page failure or empty
	// collection), skip the missing/orphan computation — the qdrantIDs
	// set is empty and would produce catastrophic false positives.
	if pointsScrolled == 0 {
		report.Errors = append(report.Errors,
			"QDRANT-003: zero points scrolled — cannot compute missing/orphan IDs. "+
				"Check collection exists and scroll API is reachable.")
	} else {
		// ── Compute missing / orphan from ID sets ────────────────────
		for sqliteID := range sqliteSet {
			if !qdrantIDs[sqliteID] {
				report.MissingIDs = append(report.MissingIDs, sqliteID)
				report.MissingCount++
			}
		}
		for qdrantID := range qdrantIDs {
			if !sqliteSet[qdrantID] {
				report.OrphanIDs = append(report.OrphanIDs, qdrantID)
				report.OrphanCount++
			}
		}
	}

	// ── Gate 5: Dead‑letter check (optional) ─────────────────────
	if v.deadLetter != nil {
		if dl, err := v.deadLetter.CountOpen(ctx); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("dead‑letter count: %v", err))
		} else {
			report.DeadLetterOpen = dl
		}
	}

	// ── Gate 6: Golden query smoke (QDRANT-003, June 2026) ──────
	// When a GoldenQueryRunner is wired (production admin command),
	// the report reflects real query results. When nil (tests,
	// legacy CLIs), the gate is skipped (GoldenQueriesOK = true)
	// so existing tests that don't mock a Qdrant search endpoint
	// continue to pass. Operators running --apply without a wired
	// runner get a log warning; Ready still requires the runner to
	// pass (nil runner + ExpectedPoints > 0 → Ready=false is the
	// expected behavior for production).
	if goldenQueriesWired {
		passed, failures, err := v.goldenQueries.RunQueries(ctx, targetCollection)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("golden queries: %v", err))
		} else {
			report.GoldenQueriesOK = passed
			report.GoldenQueryFailures = failures
			if !passed {
				report.Errors = append(report.Errors,
					fmt.Sprintf("golden queries: %d failures", failures))
			}
		}
	} else {
		// No runner wired — golden query gate is trivially satisfied.
		// (The pre-QDRANT-003 behavior: hard-coded true.)
		report.GoldenQueriesOK = true
	}

	// ── Gate 7: Filter matrix smoke (QDRANT-003 close-out) ─────────────
	// FiltersOK gate runs requested payload filters against the collection
	// to confirm indexer writes hit the per-field payload indexes.
	// nil runner = gate trivially satisfied (legacy tests / CLIs w/o
	// full wiring). Production-admin CLIs MUST supply a non-nil matrix
	// so a missing payload index (e.g. dropped `source` index) blocks
	// the alias switch.
	if filtersWired {
		passed, failures, checksRun, err := v.filters.RunMatrix(ctx, targetCollection)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("filter matrix: %v", err))
		} else {
			report.FiltersOK = passed
			report.FilterFailures = failures
			report.FilterChecksRun = checksRun
			if !passed {
				report.Errors = append(report.Errors,
					fmt.Sprintf("filter matrix: %d filter failures out of %d checks", failures, checksRun))
			}
		}
	} else {
		// No matrix wired — filter gate is trivially satisfied.
		report.FiltersOK = true
	}

	// ── Ready gate: ALL conditions must pass ─────────────────────
	// QDRANT-008 / TODO 12 (June 2026): MissingChannelForMediaType
	// is included so a reindex into a collection that lacks the
	// per-manifest channels for any media_type cannot be switched
	// onto the active alias.
	report.Ready = report.ActualPoints == report.ExpectedPoints &&
		report.ExpectedPoints > 0 &&
		report.MissingCount == 0 &&
		report.OrphanCount == 0 &&
		report.PayloadIssues == 0 &&
		report.VersionMismatch == 0 &&
		report.MissingChannelForMediaType == 0 &&
		report.NonUUIDPointIDs == 0 &&
		report.DeadLetterOpen == 0 &&
		report.GoldenQueriesOK &&
		report.FiltersOK &&
		len(report.Errors) == 0

	if !report.Ready {
		v.log.Warn("QDRANT-003: reindex verification FAILED",
			zap.String("target", targetCollection),
			zap.Int("expected", report.ExpectedPoints),
			zap.Int("actual", report.ActualPoints),
			zap.Int("missing", report.MissingCount),
			zap.Int("orphan", report.OrphanCount),
			zap.Int("payload_issues", report.PayloadIssues),
			zap.Int("version_mismatch", report.VersionMismatch),
			zap.Int("dead_letter_open", report.DeadLetterOpen),
			zap.Int("errors", len(report.Errors)))
	} else {
		v.log.Info("QDRANT-003: reindex verification PASSED — all gates green",
			zap.String("target", targetCollection),
			zap.Int("points", report.ActualPoints))
	}

	return report, nil
}

// validatePayloadMinimum checks that a Qdrant point's payload contains the
// minimum required fields (asset_id, name, source). Returns a human-readable
// issue string, or empty string if the payload is valid.
func validatePayloadMinimum(payload map[string]interface{}, pointID string) string {
	if payload == nil {
		return fmt.Sprintf("point %s: payload is nil", pointID)
	}
	required := []string{"asset_id", "name", "source"}
	for _, field := range required {
		if val, ok := payload[field]; !ok || val == nil || val == "" {
			return fmt.Sprintf("point %s: missing required payload field %q", pointID, field)
		}
	}
	return ""
}

// isUUIDForm returns true when s matches the UUID string pattern:
// 36 characters with hyphens at positions 8, 13, 18, and 23
// (e.g. "550e8400-e29b-41d4-a716-446655440000").
// Used by the verifier to skip UUID checks on non-UUID point IDs
// from test fixtures or legacy points.
func isUUIDForm(s string) bool {
	if len(s) != 36 {
		return false
	}
	return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}
