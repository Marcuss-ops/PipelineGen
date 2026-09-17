// Package media — clip_identity_concurrency_test.go: the live-PostgreSQL
// certificate for the YouTube clip identity (2026-09-17 unification).
//
// Two contracts are proven here, against a real PostgreSQL 18 + pgvector
// instance (DSN-gated, same harness as the rest of the package):
//
//  1. CONCURRENCY: ten workers committing the SAME clip identity in parallel
//     leave exactly ONE media_assets row, ONE current transcript, and ONE
//     outbox index event. This is the race the application-level dedup check
//     could not close ("SELECT → not found" in both workers, then two INSERTs).
//
//  2. DATABASE DEFENSE: the same video window under a DIFFERENT asset id is
//     rejected by the database itself
//     (ux_media_assets_youtube_clip_identity), so no future writer can mint a
//     second asset for a window that already has one — regardless of which
//     code path it came from.
//
// Both are asserted through the canonical public surface
// (PostgresMediaCommitter.CommitClipTextAndIndexEvent), never by hand-writing
// the rows the test then reads back.
package media_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

const (
	identityVideoID = "vLRjqTIiMjc"
	identityStart   = 39
	identityEnd     = 90
	identityAssetID = "yt_vLRjqTIiMjc_39_90_v1"
)

// clipIdentityCommand builds the canonical atomic command for the Dolly
// Parton interview window. Every field that the identity depends on is
// explicit so the test fails loudly if a component stops being carried.
func clipIdentityCommand() localized.CommitLocalizedClipCommand {
	text := "dolly parton got kicked out of a hotel on her first trip to new york"
	lang := "en"
	clip := youtubetypes.ClipAsset{
		ID:        identityAssetID,
		VideoID:   identityVideoID,
		LocalPath: "/tmp/yt_vLRjqTIiMjc_39_90_v1.mp4",
		// Content fingerprint: deliberately NOT part of the identity.
		LegacyFileMD5: strings.Repeat("ab", 32),
		SearchText:    text + " dolly parton interview conan",
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: identityStart,
			EndSec:   identityEnd,
			Duration: identityEnd - identityStart,
		},
		PolicyVersion: detail.DefaultYouTubeClipPolicyVersion,
		Metadata: youtubetypes.CanonicalClipMetadata{
			ClipID:          identityAssetID,
			AssetID:         identityAssetID,
			Title:           "Kicked out of a hotel in NYC",
			Summary:         "Dolly Parton tells Conan about her first trip to New York.",
			Description:     "Dolly Parton interview with Conan O'Brien.",
			SourceURL:       "http://www.youtube.com/watch?v=" + identityVideoID,
			SourceProvider:  "youtube",
			VideoID:         identityVideoID,
			ClipStartSec:    identityStart,
			ClipEndSec:      identityEnd,
			ClipDurationSec: identityEnd - identityStart,
			PolicyVersion:   detail.DefaultYouTubeClipPolicyVersion,
			Category:        "Interview",
			CleanTranscript: text,
		},
	}
	hash := detail.TextHash(text, lang, detail.TextTrackTranscript)
	return localized.CommitLocalizedClipCommand{
		Clip: clip,
		TextTracks: []detail.TextTrack{{
			AssetID:            identityAssetID,
			LanguageCode:       lang,
			TextKind:           detail.TextTrackTranscript,
			TextContent:        text,
			SourceType:         detail.TextSourceWhisper,
			SourceLanguageCode: lang,
			IsOriginal:         true,
			Provider:           "faster-whisper",
			ModelName:          "tiny",
			ModelVersion:       "v1",
			TextHash:           hash,
			SourceVersion:      detail.SourceVersion(hash, lang, lang, "faster-whisper", "tiny", "v1", ""),
			Status:             detail.TextTrackReady,
			IsCurrent:          true,
		}},
		IndexEvent: youtubeports.IndexEventPayload{
			AggregateID: identityAssetID,
			CreatedAt:   time.Now().UTC(),
		},
	}
}

func newClipIdentityCommitter(t *testing.T) *pgmedia.PostgresMediaCommitter {
	t.Helper()
	db := newMediaTestDB(t)
	ledger, err := pgmedia.NewRegistry(db)
	require.NoError(t, err)
	return pgmedia.NewPostgresMediaCommitter(db, pgmedia.NewOutboxRepository(db), ledger, nil)
}

// TestClipIdentity_ConcurrentCommitsConvergeOnOneAsset is contract (1).
func TestClipIdentity_ConcurrentCommitsConvergeOnOneAsset(t *testing.T) {
	committer := newClipIdentityCommitter(t)
	ctx := context.Background()

	const workers = 10
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all workers at once
			if err := committer.CommitClipTextAndIndexEvent(ctx, clipIdentityCommand()); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	// A duplicate-key / unique-violation error here would mean two writers
	// disagreed about the identity — i.e. the unification regressed.
	for _, err := range errs {
		require.NoError(t, err, "concurrent commit of the SAME identity must not fail: an identity error means two writers minted different ids for one clip")
	}

	db := committer.DB()

	var assets int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE id = $1`, identityAssetID).Scan(&assets))
	require.Equal(t, 1, assets, "10 parallel commits of one identity must leave exactly 1 media_assets row")

	// The identity components must be ON THE ROW, not only inside the id: the
	// database index below compares them.
	var provider, videoID, policy string
	var startMs, endMs int64
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT source_provider, source_video_id, policy_version, start_ms, end_ms
		FROM media_assets WHERE id = $1
	`, identityAssetID).Scan(&provider, &videoID, &policy, &startMs, &endMs))
	require.Equal(t, "youtube", provider)
	require.Equal(t, identityVideoID, videoID)
	require.Equal(t, detail.DefaultYouTubeClipPolicyVersion, policy,
		"the policy component of the identity must be persisted on the row (the DB identity index compares it)")
	require.Equal(t, int64(identityStart*1000), startMs)
	require.Equal(t, int64(identityEnd*1000), endMs)

	var currentTracks int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM asset_text_tracks
		WHERE asset_id = $1 AND text_kind = 'transcript' AND is_current = 1
	`, identityAssetID).Scan(&currentTracks))
	require.Equal(t, 1, currentTracks, "10 parallel commits must leave exactly 1 CURRENT transcript")

	var events int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox_events
		WHERE aggregate_id = $1 AND event_type = 'asset.index.requested'
	`, identityAssetID).Scan(&events))
	require.Equal(t, 1, events, "10 parallel commits must leave exactly 1 logical index event (the outbox event_key dedups replays)")
}

// TestClipIdentity_DatabaseRejectsSecondAssetForSameWindow is contract (2):
// the DB-level defense, exercised by a writer that mints a DIFFERENT id for a
// window that already exists — exactly what the retired
// `yt_<videoID>_<md5-prefix>` format did.
func TestClipIdentity_DatabaseRejectsSecondAssetForSameWindow(t *testing.T) {
	committer := newClipIdentityCommitter(t)
	ctx := context.Background()
	db := committer.DB()

	require.NoError(t, committer.CommitClipTextAndIndexEvent(ctx, clipIdentityCommand()))

	// Same video + window + policy, but the legacy content-derived id.
	_, err := db.ExecContext(ctx, `
		INSERT INTO media_assets
			(id, source, name, media_type, source_provider, source_video_id,
			 start_ms, end_ms, policy_version, lifecycle_state, index_state, created_at, updated_at)
		VALUES ('yt_vLRjqTIiMjc_a1b2c3d4', 'youtube', 'duplicate', 'video', 'youtube', $1,
				$2, $3, $4, 'ACTIVE', 'DISCOVERED', 'now', 'now')
	`, identityVideoID, identityStart*1000, identityEnd*1000, detail.DefaultYouTubeClipPolicyVersion)
	require.Error(t, err, "a second asset id for the SAME source window must be rejected by the database, not just by application dedup")
	require.Contains(t, err.Error(), "ux_media_assets_youtube_clip_identity",
		"the rejection must come from the canonical clip-identity index (got: %v)", err)

	// The legitimate re-cut case must still be allowed: same window under a
	// DIFFERENT policy is a genuinely different asset, because the policy
	// version is part of the identity.
	_, err = db.ExecContext(ctx, `
		INSERT INTO media_assets
			(id, source, name, media_type, source_provider, source_video_id,
			 start_ms, end_ms, policy_version, lifecycle_state, index_state, created_at, updated_at)
		VALUES ('yt_vLRjqTIiMjc_39_90_v2', 'youtube', 'policy v2 re-cut', 'video', 'youtube', $1,
				$2, $3, 'v2', 'ACTIVE', 'DISCOVERED', 'now', 'now')
	`, identityVideoID, identityStart*1000, identityEnd*1000)
	require.NoError(t, err, "a re-cut under a NEW policy keeps the same window but is a distinct asset; the index must not collapse it")

	// Non-YouTube rows (stock/planner/local: empty source_video_id, 0/0 window)
	// must be unaffected — this is why the index is PARTIAL.
	for i, id := range []string{"planner:deadbeef:0", "planner:deadbeef:1", "local_import_001"} {
		_, err = db.ExecContext(ctx, `
			INSERT INTO media_assets
				(id, source, name, media_type, source_provider, source_video_id,
				 start_ms, end_ms, policy_version, lifecycle_state, index_state, created_at, updated_at)
			VALUES ($1, 'planner', 'planner clip', 'video', '', '', 0, 0, 'v1', 'ACTIVE', 'DISCOVERED', 'now', 'now')
		`, id)
		require.NoError(t, err, "row %d (%s): rows without a YouTube window must not be constrained by the partial index", i, id)
	}
}
