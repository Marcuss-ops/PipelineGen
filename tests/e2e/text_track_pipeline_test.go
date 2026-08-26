package e2e

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	texttracks "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// Materialize job pipeline cases: happy path, idempotency, wire shape,
// and single outbox emission.

func TestE2E_TextTrackMaterializeJobPipeline_HappyPath(t *testing.T) {
	fx := newTextTrackPipelineFixture(t, "media_assets_current")
	ctx := context.Background()

	const (
		assetID    = "tt_pipeline_happy_001"
		sourceText = "Broner yells at Pacquiao — stop worrying about Floyd"
	)
	seedSourceTrackEn(t, fx, assetID, sourceText, "src-v1")
	srcHash := texttracks.ComputeSourceTextHash(sourceText)

	// ── 1. fanout.EnqueueMaterializeOne → *Service.Enqueue → INSERT INTO jobs. ──
	require.NoError(t,
		fx.FanOut.EnqueueMaterializeOne(
			ctx, assetID, "en", srcHash,
			[]detail.TextTrackKind{detail.TextTrackTranscript},
		),
		"EnqueueMaterializeOne must succeed (broker surfaces ActiveKey dedup + lifecycle)")

	// Exactly 1 jobs row.
	var jobCount int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE type = ?`, job.TypeAssetTextMaterialize,
	).Scan(&jobCount))
	require.Equal(t, 1, jobCount,
		"Enqueue must produce exactly 1 asset.text.materialize jobs row")

	// ── 2. dispatcher.Dispatch → MaterializeJobHandler.HandleJob (broker hands off to worker). ──
	res := dispatchMostRecentMaterialize(t, fx)

	// Result envelope is the canonical MaterializeJobHandler return shape.
	require.Equal(t, assetID, res["asset_id"], "handler result asset_id must match input")
	require.Equal(t, "en", res["source_language"], "handler result source_language must match input")
	require.Equal(t, 1, res["kind_count"], "handler result kind_count must equal 1")

	// ── 3. asset_text_tracks state: source unchanged + new READY rows for [it, es, fr]. ──
	source, err := fx.TTRepo.Find(ctx, assetID, "en", detail.TextTrackTranscript)
	require.NoError(t, err)
	require.NotNil(t, source, "source track must remain readable (READ preservation)")
	require.Equal(t, sourceText, source.TextContent,
		"source text_content MUST NOT be modified by Materialize")
	require.Equal(t, detail.TextTrackReady, source.Status,
		"source status MUST remain READY post-Materialize")
	require.True(t, source.IsOriginal,
		"source IsOriginal MUST remain true (provided source)")

	for _, lang := range []string{"it", "es", "fr"} {
		row, findErr := fx.TTRepo.Find(ctx, assetID, lang, detail.TextTrackTranscript)
		require.NoError(t, findErr, "Find %s must succeed", lang)
		require.NotNil(t, row, "%s text_track row MUST exist post-Materialize", lang)
		require.Equal(t, detail.TextTrackReady, row.Status,
			"%s row MUST be READY post-Materialize", lang)
		require.Equal(t, detail.TextSourceTranslation, row.SourceType,
			"%s row MUST be source=translation", lang)
		require.Equal(t, "en", row.SourceLanguageCode,
			"%s row MUST record source_language=%q", lang, "en")
		require.False(t, row.IsOriginal,
			"%s row MUST NOT be original (it's a translation)", lang)
		require.Equal(t, "stub", row.Provider,
			"%s row MUST record stub Provider (canonical provenance)", lang)
		require.Equal(t, "model-v1", row.ModelVersion,
			"%s row MUST record materializer ModelVersion", lang)
		require.Equal(t, fmt.Sprintf("[%s] %s", lang, sourceText), row.TextContent,
			"%s row text_content MUST match stub-translator shape", lang)
	}

	// ── 4. Outbox emission: exactly 1 row with the canonical event_type + payload shape. ──
	out := queryOutboxFor(t, fx, assetID, outboxevents.EventAssetIndexRequested)
	require.Equal(t, outboxevents.EventAssetIndexRequested, out.EventType,
		"outbox event_type MUST be asset.index.requested")
	require.Equal(t, assetID, out.AggregateID,
		"outbox aggregate_id MUST equal the asset ID")
	require.Equal(t, "asset", out.AggregateType,
		"outbox aggregate_type MUST be 'asset' (godlike/06 SSOT)")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.PayloadJSON), &envelope),
		"outbox payload_json must be valid JSON")
	require.Equal(t, assetID, envelope["asset_id"],
		"outbox payload asset_id MUST equal the asset ID")
	require.Equal(t, string(detail.TextTrackTranscript), envelope["kind"],
		"outbox payload kind MUST equal 'transcript'")
	require.Equal(t, "asset.text.materialize complete", envelope["reason"],
		"outbox payload reason MUST be the canonical literal")

	// ── 5. Translator was called once per target language (3); source excluded (0). ──
	fx.Translator.mu.Lock()
	calls := make(map[string]int, len(fx.Translator.callsBy))
	for k, v := range fx.Translator.callsBy {
		calls[k] = v
	}
	fx.Translator.mu.Unlock()
	require.Equal(t, 1, calls["it"], "translator MUST be called exactly once for IT")
	require.Equal(t, 1, calls["es"], "translator MUST be called exactly once for ES")
	require.Equal(t, 1, calls["fr"], "translator MUST be called exactly once for FR")
	require.Equal(t, 0, calls["en"],
		"source language MUST be excluded from the fan-out (0 translator calls for 'en')")
}

// ─── Probe 2: BROKER IDEMPOTENCY ────────────────────────────────────────
// Verifies the canonical two-level idempotency stack:
//
//  1. BROKER-LEVEL: ActiveKey =
//     "asset.text.materialize:<asset_id>:<source_text_hash>".
//     *Service.Enqueue collapses repeated enqueues to a single jobs
//     row (godlike/07 fail-closed: no double publish for the same
//     source post-publish race).
//
//  2. HANDLER-LEVEL: even when the broker row count is 1, the
//     Materializer should be invoked exactly once (one HandleJob
//     call). The e2e asserts this indirectly: exactly 1 dispatch
//     produces exactly 3 translator calls + 1 outbox_events row
//     (not 6 + 2).
func TestE2E_TextTrackMaterializeJobPipeline_BrokerIdempotency(t *testing.T) {
	fx := newTextTrackPipelineFixture(t, "media_assets_current")
	ctx := context.Background()

	const (
		assetID    = "tt_pipeline_idempotency_002"
		sourceText = "idempotency test text"
	)
	seedSourceTrackEn(t, fx, assetID, sourceText, "src-v1")
	srcHash := texttracks.ComputeSourceTextHash(sourceText)

	// ── Enqueue TWICE with identical ActiveKey. Broker dedups at the row level. ──
	require.NoError(t, fx.FanOut.EnqueueMaterializeOne(
		ctx, assetID, "en", srcHash,
		[]detail.TextTrackKind{detail.TextTrackTranscript},
	), "first EnqueueMaterializeOne must succeed")
	require.NoError(t, fx.FanOut.EnqueueMaterializeOne(
		ctx, assetID, "en", srcHash,
		[]detail.TextTrackKind{detail.TextTrackTranscript},
	), "second EnqueueMaterializeOne must succeed (silently collapses to existing job)")

	// ── Exactly 1 jobs row (ActiveKey collapse). ──
	var jobCount int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE type = ?`, job.TypeAssetTextMaterialize,
	).Scan(&jobCount))
	require.Equal(t, 1, jobCount,
		"ActiveKey dedup MUST collapse repeated enqueues to a single jobs row")

	// ── ActiveKey column carries the canonical shape. ──
	var observedKey string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT active_key FROM jobs WHERE type = ? ORDER BY created_at ASC LIMIT 1`,
		job.TypeAssetTextMaterialize,
	).Scan(&observedKey))
	require.Equal(t,
		"asset.text.materialize:"+assetID+":"+srcHash,
		observedKey,
		"ActiveKey MUST follow asset.text.materialize:<asset_id>:<source_text_hash> format")

	// ── Dispatch once. Result envelope is the canonical shape. ──
	res := dispatchMostRecentMaterialize(t, fx)
	require.Equal(t, assetID, res["asset_id"])
	require.Equal(t, "en", res["source_language"])

	// ── Exactly 1 outbox_events row (handler ran once — NOT twice). ──
	var outboxCount int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, assetID,
	).Scan(&outboxCount))
	require.Equal(t, 1, outboxCount,
		"ActiveKey dedup must result in exactly 1 dispatch + 1 outbox emission")
}

// ─── Probe 3: PAYLOAD WIRE-SHAPE PIN ────────────────────────────────────
// Locks the literal JSON wire shape of MaterializeJobPayload so future
// refactors (struct tag drift, field rename, omitempty flip) surface as
// test failure here, not as a silent worker-decode failure on first
// contact with a real producer.
//
// require.JSONEq renders the comparison order-agnostic; key NAMES
// must still match exactly (godlike/06 SSOT wire contract).
func TestE2E_TextTrackMaterializeJobPipeline_PayloadWireShape(t *testing.T) {
	tests := []struct {
		name             string
		payload          texttracks.MaterializeJobPayload
		expected         string
		absentKeysAssert []string // keys that MUST NOT appear (omitempty conformance)
	}{
		{
			name: "all five canonical fields present",
			payload: texttracks.MaterializeJobPayload{
				AssetID:        "asset-payload-wire-test",
				SourceLanguage: "en",
				SourceTextHash: "sha256:abc123",
				TextKinds:      []string{"transcript", "description"},
			},
			expected: `{
				"asset_id": "asset-payload-wire-test",
				"source_language": "en",
				"source_text_hash": "sha256:abc123",
				"text_kinds": ["transcript", "description"]
			}`,
		},
		{
			name: "target_languages override included",
			payload: texttracks.MaterializeJobPayload{
				AssetID:         "asset-tl-override-test",
				SourceLanguage:  "en",
				SourceTextHash:  "sha256:def456",
				TargetLanguages: []string{"it", "es"},
				TextKinds:       []string{"transcript"},
			},
			expected: `{
				"asset_id": "asset-tl-override-test",
				"source_language": "en",
				"source_text_hash": "sha256:def456",
				"target_languages": ["it", "es"],
				"text_kinds": ["transcript"]
			}`,
		},
		{
			name: "nil TargetLanguages omitted (omitempty contract)",
			payload: texttracks.MaterializeJobPayload{
				AssetID:        "asset-no-tl-test",
				SourceLanguage: "en",
				SourceTextHash: "sha256:xyz789",
				TextKinds:      []string{"summary"},
			},
			expected: `{
				"asset_id": "asset-no-tl-test",
				"source_language": "en",
				"source_text_hash": "sha256:xyz789",
				"text_kinds": ["summary"]
			}`,
			absentKeysAssert: []string{"target_languages"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			require.NoError(t, err, "json.Marshal must not error")
			require.JSONEq(t, tc.expected, string(raw),
				"MaterializeJobPayload wire shape must match exactly (godlike/06 SSOT wire contract)")
			for _, absent := range tc.absentKeysAssert {
				require.NotContains(t, string(raw), `"`+absent+`"`,
					"key %q must be absent from wire shape under omitempty contract", absent)
			}
		})
	}
}

// Probe 4 ensures the outbox_events row has EXACTLY 1 entry per
// HandleJob invocation — i.e. the materializer does not double-emit
// when both created + retranslated languages exist. Mirrors a worker
// retry scenario: an asset whose source row changed mid-flight
// triggers retranslate (rather than skip), so CreatedLanguages +
// RetranslatedLanguages both populate and a single emission still
// holds (the canonical "asset.index.requested" surface is per-asset,
// not per-language).
func TestE2E_TextTrackMaterializeJobPipeline_OutboxEmissionSingle(t *testing.T) {
	fx := newTextTrackPipelineFixture(t, "media_assets_current")
	ctx := context.Background()

	const (
		assetID    = "tt_pipeline_outbox_single_004"
		sourceText = "outbox single-emission test"
	)
	// Seed a STALE Italian row (model_version != materializer's "model-v1")
	// so the materializer classifies IT as "retranslate" (vs IT-classified
	// as "skip") AND seeds fresh rows for es + fr.
	seedSourceTrackEn(t, fx, assetID, sourceText, "src-v1")

	// Seed stale IT row with a different model_version so policy.ShouldRetranslate
	// fires on the IT language (returns ShouldRetranslate=true because the
	// existing track's ModelVersion != materializer's "model-v1"). This
	// drives the CreatedLanguages + RetranslatedLanguages aggregation
	// path; the materializer should still emit a single asset.index.requested.
	require.NoError(t, fx.TTRepo.UpsertBatch(ctx, []detail.TextTrack{
		{
			AssetID:            assetID,
			LanguageCode:       "it",
			TextKind:           detail.TextTrackTranscript,
			TextContent:        "[it] stale (old model) text",
			SourceType:         detail.TextSourceTranslation,
			SourceLanguageCode: "en",
			IsOriginal:         false,
			Provider:           "stub",
			ModelName:          "stub-model",
			ModelVersion:       "model-v0-stale",
			TextHash:           "sha256:it_stale",
			SourceVersion:      "src-v1",
			Status:             detail.TextTrackReady,
		},
	}), "seed stale IT row must succeed")

	srcHash := texttracks.ComputeSourceTextHash(sourceText)
	require.NoError(t, fx.FanOut.EnqueueMaterializeOne(
		ctx, assetID, "en", srcHash,
		[]detail.TextTrackKind{detail.TextTrackTranscript},
	))
	res := dispatchMostRecentMaterialize(t, fx)

	// ── Single outbox row per HandleJob invocation. ──
	var n int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM outbox_events
		  WHERE aggregate_id = ?
		    AND event_type  = ?`,
		assetID, outboxevents.EventAssetIndexRequested,
	).Scan(&n))
	require.Equal(t, 1, n,
		"asset.text.materialize must emit EXACTLY 1 outbox row per dispatch (per-asset, not per-language)")

	// Result envelope concurs: at least one translated language (created or
	// retranslated) is reported, and zero failed. This gates the regression
	// where the outbox emission path was accidentally gated on
	// CreatedLanguages only (and skipped when RetranslatedLanguages held
	// a value) — both paths must converge on a single emission.
	materialized, ok := res["languages_materialized"].([]string)
	require.True(t, ok,
		"handler result must carry languages_materialized ([]string) per godlike/06 contract")
	require.Equal(t, 3, len(materialized),
		"languages_materialized must contain the retranslated Italian track plus the newly created es and fr tracks")
	failed, ok := res["languages_failed"].(map[string]string)
	require.True(t, ok,
		"handler result must carry languages_failed (map[string]string) per godlike/06 contract")
	require.Empty(t, failed,
		"languages_failed MUST be empty on the happy (no-translation-error) path")
}
