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
	videoID, outDir, driveFolderID, driveFolderPath string,
) (*youtubetypes.ExtractResponse, error) {
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
		Normalize:                      req.Normalize,
		KeepAudio:                      &keepAudio,
		Strategy:                       req.Strategy,
		Destination:                    req.Destination,
		SubtitleFolderID:               subtitleFolderID(req),
		SubtitleFolderPath:             subtitleFolderPath(req),
		SubtitlePerClipSubfolders:      subtitlePerClipSubfolders(req),
		RequireAllLanguagesBeforeVideo: req.RequireAllLanguagesBeforeVideo,
		RequireTranscriptReady:         req.RequireTranscriptReady,
	}
}

func subtitleFolderID(req *youtubetypes.ExtractRequest) string {
	if req == nil || req.SubtitleDestination == nil {
		return ""
	}
	return req.SubtitleDestination.FolderID
}

func subtitleFolderPath(req *youtubetypes.ExtractRequest) string {
	return ""
}

func subtitlePerClipSubfolders(req *youtubetypes.ExtractRequest) bool {
	return req != nil && req.SubtitleDestination != nil && req.SubtitleDestination.PerClipSubfolders
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
