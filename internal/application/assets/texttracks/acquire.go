// Package texttracks — acquire.go: AcquireService is the canonical
// application-layer service that acquires a source-language text
// track for a media asset when the DB has no READY row.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026).
//
// The BackfillService is the orchestrator (it reads the DB, decides
// whether to acquire, fans out translations, emits the outbox event).
// The AcquireService is the SOLE canonical owner of the
// "find a source transcript from outside the DB" decision — the
// 5-priority chain:
//
//  2. Local VTT/SRT file on disk (derived from the clip's local_path).
//     3+4. YouTube subtitles via SubtitleFetcherPort.FetchSegmentSubtitles
//     (the adapter probes manual + auto in one call).
//  5. Whisper fallback via WhisperTranscriberPort.TranscribeAudioWithDetection
//     (Fase 1.b typed method: returns DetectedLanguage + Confidence).
//
// Priority 1 (DB) is owned by the BackfillService (it queries
// asset_text_tracks BEFORE calling AcquireService). This file is
// ONLY for priorities 2-5.
//
// godlike/06 SSOT: this is the SOLE canonical place where the
// 5-priority chain is assembled for the BACKFILL path. The
// YouTube per-segment path has its own resolver
// (internal/application/youtube/usecase/text_track_resolver.go)
// that is intentionally separate — the two paths have different
// contexts (per-segment streaming vs. operator-driven batch).
//
// godlike/07 fail-closed: the AcquireService returns
// (nil, ErrNoSourceAcquired) when all priorities fail. The
// BackfillService surfaces this as a typed per-clip error;
// operators can run a separate `text-tracks acquire` subcommand
// (future Fase) to manually fill the gap.
package texttracks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ErrNoSourceAcquired is the canonical typed error when the
// AcquireService exhausts all 5 priorities without finding a
// source transcript. The BackfillService surfaces this as a
// per-clip fail-closed error (the run continues with the next
// asset).
var ErrNoSourceAcquired = errors.New("texttracks: no source transcript acquired (all 5 priorities exhausted)")

// SubtitlesPort is the narrow interface the AcquireService uses
// for YouTube subtitle acquisition (priorities 3+4). The concrete
// adapter is *ytinfra.SubtitleFetcherAdapter (wired at composition).
// Defined here (not imported from youtube/ports) to keep the
// texttracks package hermetic — the youtube package is heavier
// and the backfill path doesn't need its full surface.
type SubtitlesPort interface {
	FetchSegmentSubtitles(ctx context.Context, videoID string, startSec, endSec int) (*asset.ResolvedTextBundle, error)
}

// WhisperPort is the narrow interface the AcquireService uses
// for Whisper fallback (priority 5). The concrete adapter is the
// canonical WhisperTranscriberPort from youtube/ports
// (TranscribeAudioWithDetection returns the typed
// asset.TranscriptResult with Text, DetectedLanguage, Confidence).
type WhisperPort interface {
	TranscribeAudioWithDetection(ctx context.Context, localPath string) (asset.TranscriptResult, error)
}

// DrivePort is the narrow Drive read surface used only when the registered
// local artifact is unavailable or unreadable.
type DrivePort interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
}

// AcquireCommand bundles the inputs the 5-priority chain needs.
// All fields are mandatory except Language (used for VTT/SRT
// file-name disambiguation; empty means "any language").
type AcquireCommand struct {
	AssetID     string
	VideoID     string // YouTube video ID (for priorities 3+4)
	LocalPath   string // local clip file path (for priority 2 + 5)
	StartSec    int    // segment start (for priorities 3+4)
	EndSec      int    // segment end (for priorities 3+4)
	DriveFileID string // canonical Drive fallback source
	Language    string // BCP-47 source language hint (priority 2 disambiguation)
}

// AcquireResult is the canonical return value. PlainText is the
// source-language transcript; Cues are populated when the source
// carried per-segment timing (VTT/SRT or YouTube subs).
type AcquireResult struct {
	AssetID      string                `json:"asset_id"`
	PlainText    string                `json:"plain_text"`
	Cues         []asset.TimedCue      `json:"cues,omitempty"`
	LanguageCode string                `json:"language_code"`
	SourceType   asset.TextTrackSource `json:"source_type"`
	SourcePath   string                `json:"source_path,omitempty"` // for priority 2 (local file path)
	Confidence   *float64              `json:"confidence,omitempty"`  // for priority 5 (Whisper)
	Priority     int                   `json:"priority"`              // 2..5 — which level won
	DurationMs   int64                 `json:"duration_ms"`
}

// AcquireService is the canonical application-layer service for
// source-text acquisition. Self-contained: no dependencies on the
// youtube package. The composition root wires the concrete
// SubtitleFetcherAdapter + WhisperTranscriberAdapter.
type AcquireService struct {
	subtitles SubtitlesPort
	whisper   WhisperPort
	drive     DrivePort
	log       *zap.Logger
}

// WithDrive attaches the canonical Drive reader used as a fallback for
// registered clips whose local copy is missing or corrupt.
func (s *AcquireService) WithDrive(drive DrivePort) *AcquireService {
	if s != nil {
		s.drive = drive
	}
	return s
}

// NewAcquireService constructs the canonical service. The
// subtitles and whisper ports are OPTIONAL (nil → that priority
// is silently skipped). A nil log is a TYPED error (the operator
// runbook needs the logger for forensics).
func NewAcquireService(subtitles SubtitlesPort, whisper WhisperPort, log *zap.Logger) (*AcquireService, error) {
	if log == nil {
		return nil, fmt.Errorf("texttracks.NewAcquireService: log is nil")
	}
	return &AcquireService{
		subtitles: subtitles,
		whisper:   whisper,
		log:       log,
	}, nil
}

// Acquire runs the 5-priority chain (priorities 2-5; priority 1
// is the DB lookup owned by BackfillService). Returns
// (result, nil) on success; (nil, ErrNoSourceAcquired) when all
// priorities fail; (nil, err) only on a typed infrastructure
// error (Whisper hardware failure, malformed VTT, etc.).
//
// The chain is fail-soft: a single priority failure logs + falls
// through. The terminal error is reserved for the "all priorities
// failed" case AND for typed infrastructure errors that the
// caller should see in the per-clip report.
func (s *AcquireService) Acquire(ctx context.Context, cmd AcquireCommand) (*AcquireResult, error) {
	start := time.Now()
	if cmd.AssetID == "" {
		return nil, fmt.Errorf("texttracks.AcquireService.Acquire: asset_id is required")
	}
	if cmd.LocalPath == "" && cmd.VideoID == "" && cmd.DriveFileID == "" {
		return nil, fmt.Errorf("texttracks.AcquireService.Acquire: at least one of local_path or video_id is required")
	}

	// Priority 2: local VTT/SRT on disk. Probes a deterministic
	// set of file-name variants derived from the clip's
	// local_path. The first file that exists AND is parseable
	// wins. Malformed files are LOGGED + SKIPPED (the chain
	// falls through to YouTube subs).
	if cmd.LocalPath != "" {
		result, err := s.acquireFromLocalFile(ctx, cmd)
		if err == nil && result != nil {
			s.log.Info("acquire: local VTT/SRT (priority 2)",
				zap.String("asset_id", cmd.AssetID),
				zap.String("source_path", result.SourcePath),
				zap.String("language", result.LanguageCode),
				zap.Int("cues", len(result.Cues)))
			return result, nil
		}
		if err != nil && !errors.Is(err, errLocalFileNotFound) {
			s.log.Warn("acquire: local file parse error; falling through to YouTube subs",
				zap.String("asset_id", cmd.AssetID),
				zap.String("local_path", cmd.LocalPath),
				zap.Error(err))
		}
	}

	// Priority 2.5: recover the canonical clip from Drive before attempting
	// remote subtitles or a potentially corrupt local source. The temporary
	// file is removed after Whisper finishes; the DB's Drive identity remains
	// the durable history and no duplicate asset is created.
	if cmd.DriveFileID != "" && s.drive != nil && s.whisper != nil {
		result, err := s.acquireFromDrive(ctx, cmd)
		if err == nil && result != nil {
			s.log.Info("acquire: Drive fallback (priority 2.5)",
				zap.String("asset_id", cmd.AssetID),
				zap.String("drive_file_id", cmd.DriveFileID),
				zap.Int("priority", result.Priority))
			// A repaired local clip is authoritative for timed subtitles. If
			// the historical Drive copy has text but no cues, continue through
			// YouTube/Whisper so ASS is built from the current local media.
			if len(result.Cues) > 0 || cmd.LocalPath == "" {
				return result, nil
			}
			s.log.Warn("acquire: Drive transcript has no timed cues; trying current local clip",
				zap.String("asset_id", cmd.AssetID),
				zap.String("local_path", cmd.LocalPath))
		}
		if err != nil {
			s.log.Warn("acquire: Drive fallback failed; falling through", zap.String("asset_id", cmd.AssetID), zap.Error(err))
		}
	}

	// Priority 3+4: YouTube subtitles (manual + auto in one
	// adapter call). The SubtitlesPort.FetchSegmentSubtitles
	// adapter probes manual first, then auto, and returns the
	// first non-empty bundle. If subtitles is nil OR the call
	// fails OR the returned bundle is empty, fall through.
	if s.subtitles != nil && cmd.VideoID != "" {
		bundle, err := s.subtitles.FetchSegmentSubtitles(ctx, cmd.VideoID, cmd.StartSec, cmd.EndSec)
		if err != nil {
			s.log.Warn("acquire: YouTube subtitles failed; falling through to Whisper",
				zap.String("asset_id", cmd.AssetID),
				zap.String("video_id", cmd.VideoID),
				zap.Error(err))
		} else if bundle != nil && !bundle.IsEmpty() {
			s.log.Info("acquire: YouTube subtitles (priority 3+4)",
				zap.String("asset_id", cmd.AssetID),
				zap.String("video_id", cmd.VideoID),
				zap.String("language", bundle.LanguageCode),
				zap.Int("cues", len(bundle.Cues)))
			return &AcquireResult{
				AssetID:      cmd.AssetID,
				PlainText:    bundle.PlainText,
				Cues:         bundle.Cues,
				LanguageCode: bundle.LanguageCode,
				SourceType:   bundle.SourceType,
				Priority:     3,
				DurationMs:   time.Since(start).Milliseconds(),
			}, nil
		}
	}

	// Priority 5: Whisper fallback. Requires LocalPath (the
	// audio file to transcribe). If whisper is nil OR the call
	// fails OR the returned text is empty, the chain is
	// exhausted.
	if s.whisper != nil && cmd.LocalPath != "" {
		det, err := s.whisper.TranscribeAudioWithDetection(ctx, cmd.LocalPath)
		if err != nil {
			// godlike/07 diagnostics: wrap the inner error
			// with %w (not %v) so operators can distinguish
			// ErrStubTranscript (Fase 5 placeholder) from a
			// real Whisper hardware failure via
			// errors.Is(returnedErr, youtube.ErrStubTranscript).
			// ErrNoSourceAcquired remains the canonical
			// surface for the BackfillService.
			s.log.Warn("acquire: Whisper failed; chain exhausted",
				zap.String("asset_id", cmd.AssetID),
				zap.String("local_path", cmd.LocalPath),
				zap.Error(err))
			return nil, fmt.Errorf("%w: whisper: %w", ErrNoSourceAcquired, err)
		}
		if det.Text != "" {
			// Normalize the detected language. The concrete
			// Whisper adapter MUST already do this (per the
			// port contract), but we double-normalize here
			// for defence-in-depth (godlike/07 honest lock).
			lang, nErr := asset.Normalize(det.DetectedLanguage)
			if nErr != nil {
				lang = "und"
			}
			s.log.Info("acquire: Whisper (priority 5)",
				zap.String("asset_id", cmd.AssetID),
				zap.String("local_path", cmd.LocalPath),
				zap.String("language", lang),
				zap.Float64("confidence", confidenceValue(det.Confidence)))
			return &AcquireResult{
				AssetID:      cmd.AssetID,
				PlainText:    det.Text,
				Cues:         det.Cues, // Whisper now returns per-segment timing!
				LanguageCode: lang,
				SourceType:   asset.TextSourceWhisper,
				Confidence:   det.Confidence,
				Priority:     5,
				DurationMs:   time.Since(start).Milliseconds(),
			}, nil
		}
	}

	// Chain exhausted.
	s.log.Info("acquire: all 5 priorities exhausted",
		zap.String("asset_id", cmd.AssetID),
		zap.String("local_path", cmd.LocalPath),
		zap.String("video_id", cmd.VideoID))
	return nil, ErrNoSourceAcquired
}

// confidenceValue safely dereferences a *float64 confidence
// for logging. Returns 0.0 when nil.
func confidenceValue(c *float64) float64 {
	if c == nil {
		return 0
	}
	return *c
}

// errLocalFileNotFound is an internal sentinel: a local VTT/SRT
// file was not found at any of the probed paths. This is NOT a
// hard error — the chain falls through to YouTube subs.
var errLocalFileNotFound = errors.New("texttracks: no local VTT/SRT file found")

// acquireFromLocalFile implements priority 2: probe a
// deterministic set of file-name variants derived from the
// clip's local_path. The first existing + parseable file wins.
//
// Probed paths (in strict priority order):
//
//  1. <base>.vtt
//  2. <base>.srt
//  3. <base>.<language>.vtt   (e.g. /tmp/clip.it.vtt)
//  4. <base>.<language>.srt
//
// where <base> = local_path with the last extension stripped.
// Files that exist but are malformed are LOGGED + SKIPPED; the
// chain does NOT abort on a parse error (the next priority is
// YouTube subs).
func (s *AcquireService) acquireFromLocalFile(ctx context.Context, cmd AcquireCommand) (*AcquireResult, error) {
	ext := filepath.Ext(cmd.LocalPath)
	base := strings.TrimSuffix(cmd.LocalPath, ext)
	if base == "" {
		return nil, errLocalFileNotFound
	}

	candidates := []string{
		base + ".vtt",
		base + ".srt",
	}
	if cmd.Language != "" {
		candidates = append(candidates,
			base+"."+cmd.Language+".vtt",
			base+"."+cmd.Language+".srt",
		)
	}

	for _, path := range candidates {
		info, statErr := os.Stat(path)
		if statErr != nil || info.IsDir() {
			continue
		}
		// File exists — try to parse it.
		text, cues, parseErr := ParseSubtitleFile(path)
		if parseErr != nil {
			s.log.Warn("acquire: local subtitle file parse error; trying next candidate",
				zap.String("asset_id", cmd.AssetID),
				zap.String("path", path),
				zap.Error(parseErr))
			continue
		}
		if text == "" {
			continue
		}
		// Priority 2 source_type: "local_file" (Fase 5 convention;
		// maps to asset.TextTrackSource at the caller). The
		// BackfillService converts this to the canonical
		// asset.TextSourceProvided on save (local files are
		// treated as user-provided provenance).
		lang := cmd.Language
		if lang == "" {
			lang = "und"
		}
		return &AcquireResult{
			AssetID:      cmd.AssetID,
			PlainText:    text,
			Cues:         cues,
			LanguageCode: lang,
			SourceType:   "local_file", // see BackfillService for the conversion
			SourcePath:   path,
			Priority:     2,
			DurationMs:   0, // set by the outer Acquire call
		}, nil
	}
	return nil, errLocalFileNotFound
}
