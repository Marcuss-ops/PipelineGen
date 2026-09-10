// Package usecase — extraction_fanout.go: bounded-concurrency goroutine
// dispatch across ProcessYouTubeSegmentUseCase.Execute.
//
// PR-GODOBJ-1 (July 2026): the legacy inline per-seg loop was REMOVED
// (godlike/07 no-fake-availability: ProcessSeg is REQUIRED at
// composition time and the fallback path is physically gone). The
// canonical 9-step per-segment pipeline (process_segment.go) is the
// ONLY processor invoked from this fan-out.
//
// Honest-limitation (godlike/07): this file exceeds the AGENTS.md
// Check 44 target (40 LoC) because the per-goroutine panic-recovery +
// bounded-semaphore pattern is inherently verbose. The boilerplate
// is faithful to the EXISTING extractFanOut pattern (PR-C YouTube
// Cutover Commit C) and matches monitor.safeCheckChannel's panic-
// isolation precedent (per-goroutine `recover()` so a panic inside
// ProcessYouTubeSegmentUseCase.Execute does NOT crash the broker).
package usecase

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// extractFanOut fans out ProcessYouTubeSegmentUseCase.Execute across the
// inbound segments with bounded concurrency (maxConcurrentVideos). The
// canonical 9-step per-segment pipeline is invoked per goroutine; per-
// goroutine panic recovery keeps a single-segment panic from killing
// the broker; results are collected into the canonical ExtractResponse.
func (s *ExtractionService) extractFanOut(
	ctx context.Context,
	req *youtubetypes.ExtractRequest,
	segments []youtubetypes.Segment,
	videoID, outDir, driveFolderID, driveFolderPath, subtitleFolderID string,
) (*youtubetypes.ExtractResponse, error) {
	// Speed audit P0.1 (Sept 2026): "download once, cut N with ffmpeg -c copy".
	// When the operator enables VELOX_YOUTUBE_DOWNLOAD_ONCE (and the stager is wired),
	// stage the FULL source once here; each segment then cuts locally via PreDownloadedPath.
	// The receipt is scoped to THIS call stack (concurrency contract — the shared
	// ExtractionService must not hold per-request staging state): the deferred
	// release fires when this Extract() call completes, never for a sibling request.
	preparedSource, sourceStager := s.stageFullSourceOnce(ctx, req, videoID, segments)
	preDownloadedPath := ""
	// sourceFacts is probed ONCE on the staged full source (Sept 2026
	// single-probe contract): every segment reads the SAME immutable facts
	// instead of re-probing N times. nil means no staging or probe failure
	// → every segment degrades to CutModeNormalize (fail-closed).
	var sourceFacts *mediaexec.MediaFacts
	if preparedSource != nil && preparedSource.LocalPath != "" && sourceStager != nil {
		preDownloadedPath = preparedSource.LocalPath
		defer func() {
			if err := sourceStager.Release(ctx, preparedSource.CleanupToken); err != nil && s.log != nil {
				s.log.Debug("release staged youtube source after fanout", zap.Error(err))
			}
		}()
		if s.processSeg != nil {
			facts, probeErr := s.processSeg.ProbeSourceFacts(ctx, preDownloadedPath)
			if probeErr != nil {
				if s.log != nil {
					s.log.Debug("full-source probe failed; segments will normalize (fail-closed, no unproven stream-copy)",
						zap.String("video_id", videoID), zap.Error(probeErr))
				}
			} else if facts != nil {
				sourceFacts = facts
				if s.log != nil {
					s.log.Info("youtube full-source probed once",
						zap.String("video_id", videoID),
					zap.String("video_codec", facts.VideoCodec),
					zap.Int("width", facts.Width), zap.Int("height", facts.Height),
					zap.Int("fps_num", facts.FPSNum), zap.Int("fps_den", facts.FPSDen),
					zap.String("audio_codec", facts.AudioCodec))
				}
			}
		}
	}

	resp := buildInitialResponse(req, segments, videoID, driveFolderID, driveFolderPath)
	keepAudio := resolveKeepAudio(req)
	sem := make(chan struct{}, s.maxConcurrentVideos)
	results := make([]youtubetypes.ProcessSegmentResult, len(segments))
	var wg sync.WaitGroup
	for i, seg := range segments {
		i, seg := i, seg
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicErr := fmt.Errorf("segment %d panic: %v", i, r)
					results[i] = failedFanOutResult(youtubetypes.ProcessSegmentResult{}, seg, i, driveFolderID, driveFolderPath, panicErr)
					s.log.Error("panic in segment goroutine (extractFanOut recovered)",
						zap.Int("segment_index", i),
						zap.String("video_id", videoID),
						zap.Error(panicErr),
						zap.Any("recover", r))
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			cmd := buildSegmentCommand(req, seg, i, videoID, outDir, driveFolderID, driveFolderPath, keepAudio)
			cmd.PreDownloadedPath = preDownloadedPath
			cmd.SourceFacts = sourceFacts
			cmd.SubtitleFolderID = subtitleFolderID
			res, execErr := s.processSeg.Execute(ctx, cmd)
			if execErr != nil {
				res = failedFanOutResult(res, seg, i, driveFolderID, driveFolderPath, execErr)
			}
			results[i] = res
		}()
	}
	wg.Wait()
	for _, res := range results {
		resp.Items = append(resp.Items, res.Item)
	}
	stats := aggregateFanOutStats(resp.Items)
	resp.Stats = &stats
	resp.OK = classifyExtractionRun(resp.Stats)
	if !resp.OK && resp.Error == "" {
		resp.Error = "one or more segments failed"
	}
	return resp, nil
}

// buildSegmentCommand constructs the ProcessSegmentCommand envelope from
// the inbound ExtractRequest + one segment + the resolved destination +
// keepAudio flag. The 13-field struct literal mirrors the prior god-
// service inline assignment exactly (PR-GODOBJ-1 must NOT change wire
// behaviour; only split).
func buildSegmentCommand(
	req *youtubetypes.ExtractRequest,
	seg youtubetypes.Segment,
	index int,
	videoID, outDir, driveFolderID, driveFolderPath string,
	keepAudio bool,
) youtubetypes.ProcessSegmentCommand {
	return youtubetypes.ProcessSegmentCommand{
		VideoID:                        videoID,
		Segment:                        seg,
		Index:                          index,
		PolicyVersion:                  ProcessSegmentPolicyVersion,
		OutDir:                         outDir,
		DriveFolderID:                  driveFolderID,
		DriveFolderPath:                driveFolderPath,
		VideoURL:                       req.URL,
		ForceKeyframes:                 req.ForceKeyframes,
		KeepAudio:                      &keepAudio,
		Strategy:                       req.Strategy,
		Destination:                    req.Destination,
		SubtitleFolderID:               "", // resolved by the caller (extractFanOut) after pre-resolution
		SubtitleFolderPath:             subtitleFolderPath(req),
		RequireAllLanguagesBeforeVideo: req.RequireAllLanguagesBeforeVideo,
		RequireTranscriptReady:         req.RequireTranscriptReady,
	}
}

func subtitleFolderPath(req *youtubetypes.ExtractRequest) string {
	return ""
}

func failedFanOutResult(
	res youtubetypes.ProcessSegmentResult,
	seg youtubetypes.Segment,
	index int,
	driveFolderID, driveFolderPath string,
	err error,
) youtubetypes.ProcessSegmentResult {
	res.Status = "failed"
	res.Item.Status = "failed"
	if res.Item.Name == "" {
		res.Item.Name = cleanSegmentName(seg.Name, index)
	}
	if res.Item.Start == "" {
		res.Item.Start = strings.TrimSpace(seg.Start)
	}
	if res.Item.End == "" {
		res.Item.End = strings.TrimSpace(seg.End)
	}
	res.Item.DriveFolderID = driveFolderID
	res.Item.DriveFolderPath = driveFolderPath
	if res.Item.Error == "" && err != nil {
		res.Item.Error = err.Error()
	}
	if res.Error == nil {
		res.Error = err
	}
	return res
}
