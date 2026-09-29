// Package usecase — extraction_staging.go: the SOLE owner of "which seconds of
// the source do we download for a multi-segment YouTube extract?".
//
// Contract (September 2026, replaces the download-once full-source staging):
// an extraction downloads ONLY the seconds it publishes. The requested segment
// windows are merged into the fewest CONTIGUOUS blocks, every block is staged
// with ONE yt-dlp `--download-sections` call, and each segment is then cut
// locally from the block that contains it (a seek at
// `segmentStart - blockStart`, see ProcessSegmentCommand.PreDownloadedOffsetSec).
//
// Why not stage the whole source once (the previous P0.1 behaviour): it was the
// right answer to "N segments each spawn their own yt-dlp invocation" but the
// wrong answer to bytes. A 9-clip batch out of an hour-long interview pulled
// gigabytes to publish ~4 minutes, and the job's wall clock was dominated by
// that download. Merging the windows into blocks keeps the invocation count
// near 1 per request while fetching only the published seconds, so it dominates
// full-source staging on both axes.
//
// Why the block boundaries are exact source timestamps: the local cut subtracts
// the block start from an absolute segment timestamp, which is only correct if
// the staged file starts EXACTLY at the block start. yt-dlp snaps a plain
// section download to the keyframe at or before the start (up to one GOP of
// padding), which would shift every clip earlier, so every block is staged with
// ForceKeyframes=true — the same choice the stock sections_only path makes for
// the same reason (stockIngestPreparer.Prepare).
//
// Concurrency contract: ExtractionService is a SHARED service across concurrent
// Extract() requests, so the staged receipts must never live on the service
// struct (a second request could overwrite or release the first request's
// token). stageSectionBlocks therefore returns the receipts to the caller,
// which keeps them on its OWN call stack and releases them (best-effort) after
// fanout completes. The FilesystemStager is already safe for concurrent access
// (keyed locking per stage ID), so no extra locking is needed here.
//
// The staged sections live with a 24h TTL (FilesystemStager default) and are
// released best-effort after fanout, so a hot retry within the TTL hits the
// cache instead of re-fetching the same window.
package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

const (
	// sectionBlockGapToleranceSec merges two requested windows into ONE block
	// when they are separated by at most this many seconds.
	//
	// The trade is explicit: a second yt-dlp invocation costs the downloader's
	// FIXED per-invocation overhead — measured at roughly 15-20s on this host
	// (JS challenge, format negotiation, muxer spawn; see the
	// max_concurrent_stock_downloads note in config.yaml) — while the extra
	// seconds ride along inside a download that is already running (1080p
	// sections measure ~8 MiB per 40s). Merging a small gap is therefore
	// strictly cheaper than starting a second download for it. Keep this well
	// below the per-invocation overhead: past that point the extra bytes win.
	sectionBlockGapToleranceSec = 15

	// sectionStageConcurrency bounds how many blocks are staged in parallel.
	// Blocks are independent downloads, so they overlap; the process-wide
	// YouTube IP budget (downloader's youtube_gate) is what actually protects
	// the upstream, and this bound only keeps one request from opening an
	// unbounded number of ffmpeg/yt-dlp processes.
	sectionStageConcurrency = 4
)

// sectionBlock is one merged, contiguous download window plus the segments it
// covers. StartSec/EndSec are ABSOLUTE source seconds; the staged file for this
// block maps t=0 to StartSec.
type sectionBlock struct {
	StartSec       float64
	EndSec         float64
	SegmentIndexes []int
}

// stagedSection is the staged file for one block. Path is the file the segments
// cut from locally; CleanupToken is the acquisition receipt the CALLER releases
// after fanout (concurrency contract in the package doc).
type stagedSection struct {
	Block        sectionBlock
	Path         string
	Facts        *mediaexec.MediaFacts
	CleanupToken string
}

// segmentSource is the per-segment resolution of stagedSection: the file the
// segment is cut from and the absolute source second that maps to its t=0.
// The zero value means "no staged section for this segment", which every
// consumer MUST treat as "fall back to the per-segment yt-dlp path".
type segmentSource struct {
	Path      string
	OffsetSec float64
	Facts     *mediaexec.MediaFacts
}

// FullSourceStager exposes the acquisition stager that stages source windows,
// without breaking encapsulation. Returns nil when the use case is nil.
func (u *ProcessYouTubeSegmentUseCase) FullSourceStager() acquisition.SourceStager {
	if u == nil {
		return nil
	}
	return u.media.Stager
}

// ProbeSourceFacts probes a staged section ONCE and returns the immutable facts
// the CutModeResolver needs. It is the single-probe owner of the extraction
// fanout: every segment of a block reads the SAME immutable facts instead of
// re-probing the file per segment. A nil FFProbe port or any probe error
// returns (nil, err) — the caller degrades the segment to CutModeNormalize
// (fail-closed, no unproven stream-copy).
func (u *ProcessYouTubeSegmentUseCase) ProbeSourceFacts(ctx context.Context, localPath string) (*mediaexec.MediaFacts, error) {
	if u == nil || u.media.FFProbe == nil || localPath == "" {
		return nil, nil
	}
	facts, err := u.media.FFProbe.ProbeFacts(ctx, localPath)
	if err != nil {
		return nil, err
	}
	return facts, nil
}

// sectionBlocksForSegments merges the requested segment windows into the fewest
// contiguous blocks, in ascending start order.
//
// Two windows land in the same block when the later one starts at most
// sectionBlockGapToleranceSec after the block currently ends; overlapping and
// touching windows always merge. A segment whose timestamps do not parse, or
// whose window is inverted/empty, is DELIBERATELY left out of every block: it
// then gets no staged source and falls back to the per-segment yt-dlp path,
// where step1_BuildClipID reports the typed invalid-timestamp error exactly as
// before. Guessing a window for an unparseable segment would download the wrong
// seconds and silently publish them.
//
// SegmentIndexes keeps the CALLER's segment order, so a consumer can map
// `segmentSources[i]` back onto `segments[i]` without re-sorting.
func sectionBlocksForSegments(segments []youtubetypes.Segment) []sectionBlock {
	type window struct {
		start, end float64
		index      int
	}
	windows := make([]window, 0, len(segments))
	for i, seg := range segments {
		// ParseTimestamp is the SAME parser step1_BuildClipID uses, so the block
		// boundaries can never disagree with the windows the clips are cut at.
		start, startErr := textutil.ParseTimestamp(seg.Start)
		if startErr != nil {
			continue
		}
		end, endErr := textutil.ParseTimestamp(seg.End)
		if endErr != nil || end <= start || start < 0 {
			continue
		}
		windows = append(windows, window{start: float64(start), end: float64(end), index: i})
	}
	if len(windows) == 0 {
		return nil
	}
	sort.SliceStable(windows, func(a, b int) bool {
		if windows[a].start != windows[b].start {
			return windows[a].start < windows[b].start
		}
		return windows[a].end < windows[b].end
	})

	blocks := make([]sectionBlock, 0, len(windows))
	for _, w := range windows {
		last := len(blocks) - 1
		if last >= 0 && w.start <= blocks[last].EndSec+sectionBlockGapToleranceSec {
			if w.end > blocks[last].EndSec {
				blocks[last].EndSec = w.end
			}
			blocks[last].SegmentIndexes = append(blocks[last].SegmentIndexes, w.index)
			continue
		}
		blocks = append(blocks, sectionBlock{
			StartSec:       w.start,
			EndSec:         w.end,
			SegmentIndexes: []int{w.index},
		})
	}
	return blocks
}

// sectionDownloadString renders the yt-dlp `--download-sections` value for one
// block: `*HH:MM:SS-HH:MM:SS` (the `*` prefix selects an absolute time range).
//
// Whole-second precision is exact here, not a rounding: segment timestamps are
// parsed with textutil.ParseTimestamp (integer seconds) and the block
// boundaries are those same integers, so no sub-second information exists to
// lose. FormatSecondsToTimestamp is the canonical HH:MM:SS formatter (pkg
// textutil is the leaf owner; the stock sections_only path renders the same
// shape via its own millisecond formatter).
func sectionDownloadString(start, end float64) string {
	return fmt.Sprintf("*%s-%s",
		textutil.FormatSecondsToTimestamp(int(start)),
		textutil.FormatSecondsToTimestamp(int(end)),
	)
}

// stageSectionBlocks stages every merged block with ONE sectioned download
// each, in parallel (bounded by sectionStageConcurrency).
//
// Fail-soft by design (godlike/07 honest degradation, not fake availability):
// a block whose Prepare or probe fails is DROPPED from the result, and the
// segments it covered then carry no staged source and take the pre-existing
// per-segment yt-dlp path. A staging failure must never fail the extraction,
// and it must never be silently reported as "staged".
//
// Returns nil when staging is not applicable at all: nil service/request, no
// blocks, or no stager wired. Nothing is stored on the shared service — the
// receipts belong to the caller's stack (concurrency contract).
func (s *ExtractionService) stageSectionBlocks(ctx context.Context, req *youtubetypes.ExtractRequest, videoID string, blocks []sectionBlock) []stagedSection {
	if s == nil || req == nil || s.processSeg == nil || len(blocks) == 0 {
		return nil
	}
	stager := s.processSeg.FullSourceStager()
	if stager == nil {
		if s.log != nil {
			s.log.Debug("youtube section staging skipped: SourceStager not wired; segments use per-segment yt-dlp",
				zap.String("video_id", videoID))
		}
		return nil
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return nil
	}

	concurrency := sectionStageConcurrency
	if concurrency > len(blocks) {
		concurrency = len(blocks)
	}
	sem := make(chan struct{}, concurrency)
	staged := make([]*stagedSection, len(blocks))
	var wg sync.WaitGroup
	for i, block := range blocks {
		i, block := i, block
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			section := sectionDownloadString(block.StartSec, block.EndSec)
			// ForceKeyframes is REQUIRED, not a preference: the local cut
			// subtracts this block's start from an absolute segment timestamp,
			// which only holds when the staged file starts exactly there (see
			// the package doc). It is also what the stock sections_only path
			// does, and the key is derived over the same SourceRef triple, so a
			// re-run of the same window is a cache hit instead of a re-download.
			source := acquisition.SourceRef{
				URL:             url,
				DownloadSection: section,
				ForceKeyframes:  true,
				MergeFormat:     "mp4",
				PolicyVersion:   ProcessSegmentPolicyVersion,
			}
			prepareCtx, err := stager.Prepare(ctx, acquisition.PrepareRequest{
				Source:         source,
				CallerRef:      "youtube.extract.section:" + videoID,
				IdempotencyKey: acquisition.DeriveIdempotencyKey(source),
				Timeout:        12 * time.Minute,
				TTL:            24 * time.Hour,
			})
			if err != nil {
				if s.log != nil {
					s.log.Warn("youtube section staging failed; those segments fall back to per-segment yt-dlp",
						zap.String("video_id", videoID),
						zap.String("section", section),
						zap.Error(err))
				}
				return
			}
			if prepareCtx == nil || prepareCtx.LocalPath == "" {
				return
			}
			var facts *mediaexec.MediaFacts
			if probed, probeErr := s.processSeg.ProbeSourceFacts(ctx, prepareCtx.LocalPath); probeErr != nil {
				if s.log != nil {
					s.log.Debug("staged section probe failed; its segments normalize (fail-closed, no unproven stream-copy)",
						zap.String("video_id", videoID),
						zap.String("section", section),
						zap.Error(probeErr))
				}
			} else {
				facts = probed
			}
			staged[i] = &stagedSection{
				Block:        block,
				Path:         prepareCtx.LocalPath,
				Facts:        facts,
				CleanupToken: prepareCtx.CleanupToken,
			}
			if s.log != nil {
				s.log.Info("youtube section staged",
					zap.String("video_id", videoID),
					zap.String("section", section),
					zap.String("local_path", prepareCtx.LocalPath),
					zap.Int64("size_bytes", prepareCtx.SizeBytes),
					zap.Int("segments", len(block.SegmentIndexes)))
			}
		}()
	}
	wg.Wait()

	out := make([]stagedSection, 0, len(blocks))
	for _, sec := range staged {
		if sec != nil {
			out = append(out, *sec)
		}
	}
	return out
}

// segmentSourcesFromStaged projects the staged blocks onto the CALLER's segment
// slice: entry i describes the file segment i is cut from and the absolute
// source second that maps to t=0 of that file. Segments covered by no staged
// block (staging skipped or failed) keep the zero value, which every consumer
// reads as "download this one per-segment".
func segmentSourcesFromStaged(segmentCount int, staged []stagedSection) []segmentSource {
	sources := make([]segmentSource, segmentCount)
	for _, sec := range staged {
		for _, idx := range sec.Block.SegmentIndexes {
			if idx < 0 || idx >= segmentCount {
				continue
			}
			sources[idx] = segmentSource{
				Path:      sec.Path,
				OffsetSec: sec.Block.StartSec,
				Facts:     sec.Facts,
			}
		}
	}
	return sources
}

// releaseStagedSections releases every staged receipt, best-effort: the fanout
// has already produced its artifacts, so a release error is logged and never
// turned into a job failure (the 24h TTL sweeps anything left behind).
func (s *ExtractionService) releaseStagedSections(ctx context.Context, stager acquisition.SourceStager, staged []stagedSection) {
	if stager == nil {
		return
	}
	for _, sec := range staged {
		if sec.CleanupToken == "" {
			continue
		}
		if err := stager.Release(ctx, sec.CleanupToken); err != nil && s != nil && s.log != nil {
			s.log.Debug("release staged youtube section after fanout", zap.Error(err))
		}
	}
}
