// Package assets — canonical_clip_writer.go migrates the YouTube clip
// atomic writer surface onto SQLiteMediaCommitter. This file collapses
// the legacy ClipAtomicWriterAdapter (8 files) into the canonical writer
// so there is ONE concrete production writer for media_assets, text
// tracks, text segments and outbox events.
//
// The two port interfaces (youtubeports.ClipAtomicWriter and
// localized.LocalizedClipWriter) are satisfied by the SAME
// SQLiteMediaCommitter instance that implements AssetCommitter +
// AssetMutator. This eliminates the second connection pool and the
// second outbox handle that the legacy adapter carried.
package imagesregistry

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// CommitClipAndIndexEvent is the canonical YouTube clip atomic write.
// It opens a fresh transaction, delegates media_assets + outbox to
// AssetCommitter.CommitTx, upserts any text tracks from the payload,
// commits, and surfaces the BLOCKER #4 typed error on terminal outbox
// conflicts.
//
// This method replaces ClipAtomicWriterAdapter.CommitClipAndIndexEvent.
// The caller (ProcessYouTubeSegmentUseCase) still owns the decision to
// call this method; the writer owns the transaction and the SQL.
func (c *SQLiteMediaCommitter) CommitClipAndIndexEvent(
	ctx context.Context,
	clipID string,
	clipAsset youtubetypes.ClipAsset,
	event youtubeports.IndexEventPayload,
) error {
	if c == nil || c.db == nil || c.box == nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipAndIndexEvent: committer not wired")
	}
	if clipID == "" {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipAndIndexEvent: clipID is required")
	}
	if event.AggregateID == "" {
		event.AggregateID = clipID
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipAndIndexEvent: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	req, err := buildYouTubeCommitRequest(clipID, clipAsset)
	if err != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipAndIndexEvent: build commit request: %w", err)
	}
	res, err := c.CommitTx(ctx, tx, req)
	if err != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipAndIndexEvent: commit asset: %w", err)
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	if len(clipAsset.Texts) > 0 {
		tracks := localizedClipTextsToTextTracks(clipID, clipAsset.Texts)
		if len(tracks) > 0 {
			if err := upsertTextTracksInTx(ctx, tx, tracks, nowStr); err != nil {
				return fmt.Errorf("SQLiteMediaCommitter.CommitClipAndIndexEvent: upsert text tracks: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipAndIndexEvent: commit: %w", err)
	}
	committed = true

	if terr := checkOutboxTerminalAfterCommit(c.log, res.OutboxInserted, clipID, res.OutboxEventKey, res.OutboxExistingStatus); terr != nil {
		return terr
	}

	if c.log != nil {
		c.log.Debug("SQLiteMediaCommitter: clip + index event committed",
			zap.String("clip_id", clipID),
			zap.String("event_key", res.OutboxEventKey),
		)
	}
	return nil
}

// CommitClipTextAndIndexEvent is the canonical localized clip atomic
// write. It performs the super-tx: media_assets + asset_text_tracks
// (RETURNING id) + asset_text_track_segments + outbox event, all in
// one transaction.
//
// This method replaces ClipAtomicWriterAdapter.CommitClipTextAndIndexEvent.
func (c *SQLiteMediaCommitter) CommitClipTextAndIndexEvent(
	ctx context.Context,
	cmd localized.CommitLocalizedClipCommand,
) error {
	if c == nil || c.db == nil || c.box == nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: committer not wired")
	}
	if cmd.Clip.ID == "" {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: cmd.Clip.ID is required")
	}

	if verr := validateLocalizedClipPolicy(cmd); verr != nil {
		if c.log != nil {
			c.log.Warn("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: locale policy violated; rolling back (no rows written)",
				zap.String("clip_id", cmd.Clip.ID), zap.Error(verr))
		}
		return verr
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	nowStr := time.Now().UTC().Format(time.RFC3339)
	req, err := buildYouTubeCommitRequest(cmd.Clip.ID, cmd.Clip)
	if err != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: build commit request: %w", err)
	}
	res, err := c.CommitTx(ctx, tx, req)
	if err != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: commit asset: %w", err)
	}

	trackIDByKey, terr := upsertTextTracksReturningIDsInTx(ctx, tx, cmd.TextTracks, nowStr)
	if terr != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: upsert text tracks: %w", terr)
	}

	if serr := insertTextTrackSegmentsInTx(ctx, tx, cmd.TimedTracks, trackIDByKey); serr != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: insert segments: %w", serr)
	}

	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: commit: %w", cerr)
	}
	committed = true

	if terr := checkOutboxTerminalAfterCommit(c.log, res.OutboxInserted, cmd.Clip.ID, res.OutboxEventKey, res.OutboxExistingStatus); terr != nil {
		return terr
	}

	if c.log != nil {
		c.log.Debug("SQLiteMediaCommitter.CommitClipTextAndIndexEvent: clip + tracks + segments + index event committed atomically",
			zap.String("clip_id", cmd.Clip.ID),
			zap.String("event_key", res.OutboxEventKey),
			zap.Int("text_tracks", len(cmd.TextTracks)),
			zap.Int("timed_tracks", len(cmd.TimedTracks)),
			zap.Bool("outbox_inserted", res.OutboxInserted))
	}
	return nil
}

// buildYouTubeCommitRequest translates a YouTube ClipAsset into the
// canonical persistence.CommitRequest. This is the SOLE place where the
// YouTube shape is mapped to the unified asset commit shape. Moved from
// ClipAtomicWriterAdapter.buildCommitRequest.
func buildYouTubeCommitRequest(clipID string, clipAsset youtubetypes.ClipAsset) (persistence.CommitRequest, error) {
	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID:   clipID,
		Provider:  "youtube",
		MediaType: mediaregistry.MediaVideo,
	})
	if err != nil {
		return persistence.CommitRequest{}, fmt.Errorf("buildYouTubeCommitRequest: resolve taxonomy: %w", err)
	}

	policyVersion := clipAsset.PolicyVersion
	if policyVersion == "" {
		policyVersion = derivePolicyVersion(clipID)
	}
	sourceVersion := deriveSourceVersion(clipID, clipAsset.LegacyFileMD5, policyVersion)

	filename := deriveFilenameFromAsset(clipAsset)
	if filename == "" {
		filename = clipID + ".mp4"
	}
	name := deriveNameFromAsset(clipAsset)
	if name == "" {
		name = filename
	}

	folderPath := clipAsset.Drive.FolderPath
	if folderPath == "" {
		folderPath = clipAsset.Drive.FolderID
	}

	return persistence.CommitRequest{
		AssetID:        clipID,
		Source:         "youtube",
		Name:           name,
		Filename:       filename,
		MediaType:      "video",
		Category:       clipAsset.Metadata.Category,
		DurationMs:     int64(clipAsset.Metadata.ClipDurationSec * 1000),
		ContentHash:    clipAsset.LegacyFileMD5,
		SearchText:     clipAsset.SearchText,
		Description:    clipAsset.Metadata.Description,
		LifecycleState: "ACTIVE",
		LocalPath:      clipAsset.LocalPath,
		FolderID:       clipAsset.Drive.FolderID,
		FolderPath:     folderPath,
		Taxonomy:       taxonomy,
		Metadata: persistence.TypedMetadata{
			SourceVersion:   sourceVersion,
			Title:           clipAsset.Metadata.Summary,
			Description:     clipAsset.Metadata.Description,
			Summary:         clipAsset.Metadata.Summary,
			Topics:          clipAsset.Metadata.Topics,
			Speakers:        clipAsset.Metadata.Speakers,
			MentionedPeople: clipAsset.Metadata.MentionedPeople,
			Hook:            clipAsset.Metadata.Hook,
			QualityScore:    clipAsset.Metadata.QualityScore,
			SponsorSegment:  clipAsset.Metadata.SponsorSegment,
			Tags:            clipAsset.Metadata.Tags,
			Category:        clipAsset.Metadata.Category,
			SourceProvider:  clipAsset.Metadata.SourceProvider,
			SourceVideoID:   clipAsset.Metadata.VideoID,
			SourceTitle:     clipAsset.Metadata.SourceTitle,
			SourceChannel:   clipAsset.Metadata.SourceChannel,
			StartSec:        float64(clipAsset.Metadata.ClipStartSec),
			EndSec:          float64(clipAsset.Metadata.ClipEndSec),
		},
		SourceURL:      clipAsset.Metadata.SourceURL,
		SourceProvider: clipAsset.Metadata.SourceProvider,
		SourceVideoID:  clipAsset.Metadata.VideoID,
		StartMs:        int64(clipAsset.Metadata.ClipStartSec * 1000),
		EndMs:          int64(clipAsset.Metadata.ClipEndSec * 1000),
		Title:          clipAsset.Metadata.Title,
		Locations: []persistence.LocationCommit{
			{
				Kind:        "drive",
				Provider:    "drive",
				ExternalID:  clipAsset.Drive.FileID,
				WebViewLink: clipAsset.Drive.WebViewLink,
				IsPrimary:   true,
			},
		},
		EmitIndexEvent: true,
		RequestedAt:    time.Now(),
	}, nil
}

// validateLocalizedClipPolicy checks the Require* flags WITHOUT opening
// the tx. Moved from commitClipTextAndIndexEvent_validatePolicy.
func validateLocalizedClipPolicy(cmd localized.CommitLocalizedClipCommand) error {
	if !cmd.RequireTranscriptReady && !cmd.RequireAllLanguagesBeforeVideo {
		return nil
	}

	readyLangs := make(map[string]bool)
	hasTranscriptReady := false
	for _, t := range cmd.TextTracks {
		if t.TextKind != detail.TextTrackTranscript {
			continue
		}
		if t.Status != detail.TextTrackReady {
			continue
		}
		if t.SourceType != detail.TextSourceProvided &&
			t.SourceType != detail.TextSourceYouTubeSubtitle &&
			t.SourceType != detail.TextSourceWhisper {
			continue
		}
		if !t.IsOriginal {
			continue
		}
		readyLangs[t.LanguageCode] = true
		hasTranscriptReady = true
	}

	if cmd.RequireTranscriptReady && !hasTranscriptReady {
		return &localized.ErrClipLocaleNotReady{
			AssetID:     cmd.Clip.ID,
			Reason:      "no transcript-origin READY track (provided/youtube_subtitle/whisper text_kind=transcript status=READY) in command.TextTracks",
			MissingKind: detail.TextTrackTranscript,
		}
	}

	if cmd.RequireAllLanguagesBeforeVideo && len(cmd.PreferredLanguages) > 0 {
		var missing []string
		for _, lang := range cmd.PreferredLanguages {
			if !readyLangs[lang] {
				missing = append(missing, lang)
			}
		}
		if len(missing) > 0 {
			return &localized.ErrClipLocaleNotReady{
				AssetID:      cmd.Clip.ID,
				Reason:       "missing READY translations for one or more PreferredLanguages",
				MissingKind:  detail.TextTrackTranscript,
				MissingCodes: missing,
			}
		}
	}

	return nil
}

// ReplaceTranscriptCues replaces all transcript cues for an asset's
// READY transcript tracks. Moved from ClipAtomicWriterAdapter.ReplaceTranscriptCues
// (clip_atomic_writer_cue_repair.go). The method opens its own
// transaction; callers that need to embed this in a larger tx should
// use a tx-scoped variant.
func (c *SQLiteMediaCommitter) ReplaceTranscriptCues(ctx context.Context, assetID string, byLang map[string][]detail.TimedCue) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("SQLiteMediaCommitter.ReplaceTranscriptCues: committer not wired")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	rows, err := tx.QueryContext(ctx, `SELECT id,language_code FROM asset_text_tracks WHERE asset_id=? AND text_kind='transcript' AND status='READY' AND is_current=1`, assetID)
	if err != nil {
		return err
	}
	ids := map[string]int64{}
	for rows.Next() {
		var id int64
		var lang string
		if err := rows.Scan(&id, &lang); err != nil {
			rows.Close()
			return err
		}
		ids[lang] = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_text_track_segments WHERE track_id IN (SELECT id FROM asset_text_tracks WHERE asset_id=? AND text_kind='transcript' AND status='READY' AND is_current=1)`, assetID); err != nil {
		return err
	}
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO asset_text_track_segments(track_id,sequence_no,start_ms,end_ms,text) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, lang := range langs {
		id, ok := ids[lang]
		if !ok {
			return fmt.Errorf("ReplaceTranscriptCues: missing READY transcript %s", lang)
		}
		cues := append([]detail.TimedCue(nil), byLang[lang]...)
		sort.SliceStable(cues, func(i, j int) bool { return cues[i].StartMs < cues[j].StartMs })
		for i, cu := range cues {
			if cu.StartMs < 0 || cu.EndMs <= cu.StartMs {
				return fmt.Errorf("ReplaceTranscriptCues: invalid cue %s #%d", lang, i+1)
			}
			if _, err := stmt.ExecContext(ctx, id, i+1, cu.StartMs, cu.EndMs, cu.Text); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// UpdateFolderPathForClip is the FolderPathWriter port method.
// SQLiteMediaCommitter already has UpdateFolderPath(ctx, assetID, folderID,
// folderPath, updatedAt) but the port signature is narrower (no folderID
// or updatedAt). This adapter resolves source_version and delegates to
// the tx-bound folder path mutator + index request.
func (c *SQLiteMediaCommitter) UpdateFolderPathForClip(ctx context.Context, assetID, folderPath string) error {
	if c == nil || c.db == nil || c.box == nil {
		return fmt.Errorf("UpdateFolderPathForClip: committer not wired")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var sourceVersion string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(source_version,'') FROM media_assets WHERE id=?`, assetID).Scan(&sourceVersion); err != nil {
		return fmt.Errorf("UpdateFolderPathForClip: asset: %w", err)
	}
	if sourceVersion == "" {
		return fmt.Errorf("UpdateFolderPathForClip: source_version is empty for %s", assetID)
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	if err := c.UpdateFolderPathTx(ctx, tx, assetID, "", folderPath, updatedAt); err != nil {
		return err
	}
	if _, err := CommitIndexRequestTx(ctx, tx, c.box, IndexRequest{
		AssetID: assetID, Source: "youtube", MediaType: "video",
		SourceVersion: sourceVersion, RequestedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// Compile-time assertions: SQLiteMediaCommitter satisfies the YouTube
// clip writer ports and the cue writer port. This eliminates the need
// for ClipAtomicWriterAdapter for these surfaces. The FolderPathWriter
// port (3-arg UpdateFolderPath) remains on the ClipMetadataWriterAdapter
// because SQLiteMediaCommitter's UpdateFolderPath has a 5-arg signature.
var (
	_ youtubeports.ClipAtomicWriter = (*SQLiteMediaCommitter)(nil)
	_ localized.LocalizedClipWriter = (*SQLiteMediaCommitter)(nil)
	_ texttracks.TimedCueWriter     = (*SQLiteMediaCommitter)(nil)
)
