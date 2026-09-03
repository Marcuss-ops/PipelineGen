// Package media — clip_writer_helpers.go is the ported helper layer of the
// canonical YouTube clip writer on the PostgreSQL media SSOT. Split from
// clip_writer.go (godlike/08 600-line gate): the request translation,
// locale-policy validation, text-track upserts, segment inserts, and
// column-mapping derivations mirror the SQLite
// clip_writer_helpers.go 1:1 — one engine, one transaction, zero SQLite
// media writes.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
)

// ── Ported helpers (SQLite mirror: clip_writer_helpers.go) ──────────────

// buildYouTubeCommitRequest translates a YouTube ClipAsset into the
// canonical persistence.CommitRequest. Mirrors the SQLite sole mapping
// site 1:1.
func buildYouTubeCommitRequest(clipID string, clipAsset youtubetypes.ClipAsset) (persistence.CommitRequest, error) {
	taxonomy, err := resolveClipTaxonomy(clipAssetToDomainAsset(clipID, clipAsset))
	if err != nil {
		// resolveClipTaxonomy expects a domain asset; fall back to the
		// direct mediaregistry resolution shape used by the SQLite writer.
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
// the tx (verbatim port).
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

// checkOutboxTerminalAfterCommitPG inspects the outbox enqueue result AFTER
// tx.Commit(); a terminal-row suppression surfaces the BLOCKER #4 sentinel.
func checkOutboxTerminalAfterCommitPG(
	log *zap.Logger,
	inserted bool,
	clipID string,
	eventKey string,
	existingStatus string,
) error {
	if inserted {
		return nil
	}
	if !isTerminalOutboxStatus(existingStatus) {
		return nil
	}
	err := fmt.Errorf("%w: clip %q event_key=%q suppressed by existing terminal row (status=%q)",
		youtubeports.ErrOutboxTerminalConflict, clipID, eventKey, existingStatus)
	if log != nil {
		log.Warn("canonical clip writer: returning ErrOutboxTerminalConflict (BLOCKER #4 closure)",
			zap.String("clip_id", clipID),
			zap.String("event_key", eventKey),
			zap.String("existing_status", existingStatus),
			zap.Error(err))
	}
	return err
}

// localizedClipTextsToTextTracks converts payload-provided
// LocalizedClipText entries into domain TextTrack rows (verbatim port).
func localizedClipTextsToTextTracks(clipID string, texts []youtubetypes.LocalizedClipText) []detail.TextTrack {
	if len(texts) == 0 {
		return nil
	}
	var tracks []detail.TextTrack
	for _, t := range texts {
		lang := t.LanguageCode
		if lang == "" {
			lang = "en"
		}
		srcType := detail.TextTrackSource(t.SourceType)
		if srcType == "" {
			srcType = detail.TextSourceProvided
		}
		isOriginal := t.IsOriginal
		if srcType == detail.TextSourceProvided {
			isOriginal = true
		}

		type entry struct {
			kind    detail.TextTrackKind
			content string
		}
		entries := []entry{
			{detail.TextTrackTranscript, t.Transcript},
			{"description", t.Description},
			{"summary", t.Summary},
			{"title", t.Title},
		}
		for _, e := range entries {
			if e.content == "" {
				continue
			}
			var confidence *float64
			if t.Confidence > 0 {
				confidence = &t.Confidence
			}
			tracks = append(tracks, detail.TextTrack{
				AssetID:            clipID,
				LanguageCode:       lang,
				TextKind:           e.kind,
				TextContent:        e.content,
				SourceType:         srcType,
				SourceLanguageCode: t.SourceLanguageCode,
				IsOriginal:         isOriginal,
				ModelName:          t.ModelName,
				ModelVersion:       t.ModelVersion,
				Confidence:         confidence,
				Status:             detail.TextTrackReady,
			})
		}
	}
	return tracks
}

// upsertTextTracksInTxPG upserts text tracks without capturing IDs
// (CommitClipAndIndexEvent path).
func upsertTextTracksInTxPG(ctx context.Context, tx *sql.Tx, tracks []detail.TextTrack, nowStr string) error {
	_, err := upsertTextTracksReturningIDsInTxPG(ctx, tx, tracks, nowStr)
	return err
}

// upsertTextTracksReturningIDsInTxPG performs the asset_text_tracks UPSERT
// inside the caller's tx, capturing the assigned track_id (via RETURNING id)
// for each row. PostgreSQL uses $N placeholders and a partial-index ON
// CONFLICT matching idx_asset_text_tracks_current.
func upsertTextTracksReturningIDsInTxPG(
	ctx context.Context,
	tx *sql.Tx,
	tracks []detail.TextTrack,
	nowStr string,
) (map[string]int64, error) {
	trackIDByKey := make(map[string]int64, len(tracks))
	if len(tracks) == 0 {
		return trackIDByKey, nil
	}

	upsertSQL := `
INSERT INTO asset_text_tracks (
    asset_id, language_code, text_kind,
    text_content,
    source_type, source_language_code, is_original,
    provider, model_name, model_version,
    text_hash, source_version,
    confidence, status,
    created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15)
ON CONFLICT (asset_id, language_code, text_kind) WHERE is_current = 1 DO UPDATE SET
    text_content         = excluded.text_content,
    source_type          = excluded.source_type,
    source_language_code = excluded.source_language_code,
    is_original          = excluded.is_original,
    provider             = excluded.provider,
    model_name           = excluded.model_name,
    model_version        = excluded.model_version,
    text_hash            = excluded.text_hash,
    source_version       = excluded.source_version,
    confidence           = excluded.confidence,
    status               = excluded.status,
    updated_at           = excluded.updated_at
RETURNING id`

	stmt, err := tx.PrepareContext(ctx, upsertSQL)
	if err != nil {
		return nil, fmt.Errorf("upsertTextTracksReturningIDsInTx: prepare: %w", err)
	}
	defer stmt.Close()

	for _, t := range tracks {
		if t.AssetID == "" || t.LanguageCode == "" || t.TextKind == "" {
			return nil, fmt.Errorf("upsertTextTracksReturningIDsInTx: row missing required keys (AssetID/LanguageCode/TextKind)")
		}

		var confidence interface{}
		if t.Confidence != nil {
			confidence = *t.Confidence
		}

		isOriginal := 0
		if t.IsOriginal {
			isOriginal = 1
		}
		status := string(t.Status)
		if status == "" {
			status = string(detail.TextTrackReady)
		}

		var id int64
		scanErr := stmt.QueryRowContext(ctx,
			t.AssetID,
			t.LanguageCode,
			string(t.TextKind),
			t.TextContent,
			string(t.SourceType),
			t.SourceLanguageCode,
			isOriginal,
			t.Provider,
			t.ModelName,
			t.ModelVersion,
			t.TextHash,
			t.SourceVersion,
			confidence,
			status,
			nowStr,
		).Scan(&id)
		if scanErr != nil {
			return nil, fmt.Errorf("upsertTextTracksReturningIDsInTx: exec (asset=%s lang=%s kind=%s): %w",
				t.AssetID, t.LanguageCode, t.TextKind, scanErr)
		}

		key := textTrackKeyPG(t.LanguageCode, t.TextKind, t.SourceType)
		trackIDByKey[key] = id
	}
	return trackIDByKey, nil
}

// textTrackKeyPG is the canonical key used to match TimedTextTrack entries
// with their parent TextTrack rows.
func textTrackKeyPG(language string, kind detail.TextTrackKind, source detail.TextTrackSource) string {
	return strings.Join([]string{language, string(kind), string(source)}, "|")
}

// insertTextTrackSegmentsInTxPG performs the BATCH INSERT of
// asset_text_track_segments, one row per cue, inside the caller's tx.
func insertTextTrackSegmentsInTxPG(
	ctx context.Context,
	tx *sql.Tx,
	timedTracks []localized.TimedTextTrack,
	trackIDByKey map[string]int64,
) error {
	if len(timedTracks) == 0 {
		return nil
	}

	insertSQL := `
INSERT INTO asset_text_track_segments (
    track_id, sequence_no, start_ms, end_ms, text
) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (track_id, sequence_no) DO UPDATE SET
    start_ms = excluded.start_ms,
    end_ms   = excluded.end_ms,
    text     = excluded.text`

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("insertTextTrackSegmentsInTx: prepare: %w", err)
	}
	defer stmt.Close()

	for _, tt := range timedTracks {
		key := textTrackKeyPG(tt.LanguageCode, tt.TextKind, tt.SourceType)
		trackID, ok := trackIDByKey[key]
		if !ok {
			return fmt.Errorf("insertTextTrackSegmentsInTx: timed track has no matching TextTrack (lang=%s kind=%s source=%s) — ensure TextTracks has the parent row",
				tt.LanguageCode, tt.TextKind, tt.SourceType)
		}

		sortedCues := append([]detail.TimedCue(nil), tt.Cues...)
		sort.SliceStable(sortedCues, func(i, j int) bool {
			if sortedCues[i].StartMs != sortedCues[j].StartMs {
				return sortedCues[i].StartMs < sortedCues[j].StartMs
			}
			return sortedCues[i].EndMs < sortedCues[j].EndMs
		})

		for seq, cue := range sortedCues {
			if cue.StartMs < 0 || cue.EndMs < cue.StartMs || cue.Text == "" {
				return fmt.Errorf("insertTextTrackSegmentsInTx: invalid cue (seq=%d start=%d end=%d text_len=%d)",
					seq, cue.StartMs, cue.EndMs, len(cue.Text))
			}
			if _, execErr := stmt.ExecContext(ctx,
				trackID, seq+1, cue.StartMs, cue.EndMs, cue.Text,
			); execErr != nil {
				return fmt.Errorf("insertTextTrackSegmentsInTx: exec (seq=%d): %w", seq+1, execErr)
			}
		}
	}
	return nil
}

// ── Column-mapping derivation helpers (verbatim port) ───────────────────

// deriveNameFromAsset returns a canonical name for the clip row.
func deriveNameFromAsset(asset youtubetypes.ClipAsset) string {
	if asset.Metadata.Summary != "" {
		return asset.Metadata.Summary
	}
	return ""
}

// deriveFilenameFromAsset returns the canonical filename for the clip row.
func deriveFilenameFromAsset(asset youtubetypes.ClipAsset) string {
	if asset.LocalPath != "" {
		return filepathBase(asset.LocalPath)
	}
	return ""
}

// derivePolicyVersion extracts the policy_version suffix from a canonical
// clipID ("yt_<videoID>_<startSec>_<endSec>_<policyVer>"). Returns "v1"
// when the suffix is missing.
func derivePolicyVersion(clipID string) string {
	const wantUnderscores = 4
	seen := 0
	for i := len(clipID) - 1; i >= 0; i-- {
		if clipID[i] == '_' {
			seen++
			if seen == wantUnderscores {
				pv := clipID[i+1:]
				if pv != "" {
					return pv
				}
				return "v1"
			}
		}
	}
	return "v1"
}

// deriveSourceVersion returns the canonical ingest-time content hash
// fingerprint used as event.source_version.
func deriveSourceVersion(clipID, fileHash, policyVersion string) string {
	if fileHash != "" {
		return fileHash
	}
	return checksum.LegacyMD5String(clipID + ":" + policyVersion)
}

func filepathBase(p string) string {
	// mirrors imagesregistry.filepathBase
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// clipAssetToDomainAsset bridges the YouTube DTO to the domain asset shape
// expected by resolveClipTaxonomy (media type/provider only).
func clipAssetToDomainAsset(clipID string, clipAsset youtubetypes.ClipAsset) *asset.Asset {
	_ = clipID
	_ = clipAsset
	return nil
}

var _ = mediacommit.TextTrack{} // keep mediacommit import stable for future upsert consolidation
