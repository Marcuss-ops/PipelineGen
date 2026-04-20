package clipsearch

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

func (s *Service) processYouTubeMomentsFromDownloaded(ctx context.Context, keyword, rawPath string, baseMeta *YouTubeClipMetadata) ([]SearchResult, int, error) {
	moments := s.pickYouTubeMoments(ctx, keyword, baseMeta)
	if len(moments) == 0 {
		m := SelectedMoment{StartSec: 0, EndSec: 25, Reason: "fallback", Source: "fallback"}
		moments = []SelectedMoment{m}
	}

	results := make([]SearchResult, 0, len(moments))
	newUploads := 0
	seenDrive := make(map[string]bool)

	for _, moment := range moments {
		meta := cloneYouTubeMetaWithMoment(baseMeta, moment)
		if existing := s.finder.FindDownloadedYouTubeByMeta(meta); existing != nil {
			if !seenDrive[existing.DriveID] {
				seenDrive[existing.DriveID] = true
				results = append(results, *existing)
			}
			continue
		}

		normalizedPath, normErr := s.processor.NormalizeClipSegmentWithAudio(ctx, rawPath, moment.StartSec, moment.EndSec-moment.StartSec)
		if normErr != nil {
			logger.Warn("Failed to normalize yt-dlp moment",
				zap.String("keyword", keyword),
				zap.Float64("start_sec", moment.StartSec),
				zap.Float64("end_sec", moment.EndSec),
				zap.Error(normErr),
			)
			continue
		}
		func() {
			defer os.Remove(normalizedPath)
			visualHash, hashErr := s.processor.ComputeVisualHash(ctx, normalizedPath)
			if hashErr == nil {
				if existing := s.finder.FindDownloadedByVisualHash(visualHash); existing != nil {
					if !seenDrive[existing.DriveID] {
						seenDrive[existing.DriveID] = true
						results = append(results, *existing)
					}
					return
				}
			}

			driveResult, upErr := s.uploader.UploadToDrive(ctx, normalizedPath, keyword)
			if upErr != nil {
				logger.Warn("Failed to upload yt-dlp moment",
					zap.String("keyword", keyword),
					zap.Error(upErr),
				)
				return
			}
			res := searchResultFromDrive(keyword, driveResult)
			enrichYouTubeSearchResult(&res, keyword, meta)
			s.uploadClipSidecarText(ctx, keyword, driveResult, buildYouTubeClipSidecarText(keyword, meta))
			res.TextDriveID = driveResult.TextFileID
			res.TextDriveURL = driveResult.TextURL
			s.persister.PersistClipMetadata(keyword, driveResult, normalizedPath, nil, strings.TrimSpace(visualHash), meta)

			if !seenDrive[res.DriveID] {
				seenDrive[res.DriveID] = true
				results = append(results, res)
				newUploads++
			}
		}()
	}
	if len(results) == 0 {
		return nil, 0, fmt.Errorf("no youtube moments processed for keyword %s", keyword)
	}
	return results, newUploads, nil
}

func cloneYouTubeMetaWithMoment(meta *YouTubeClipMetadata, moment SelectedMoment) *YouTubeClipMetadata {
	if meta == nil {
		return &YouTubeClipMetadata{SelectedMoment: &moment}
	}
	c := *meta
	c.SelectedMoment = &moment
	return &c
}

func (s *Service) pickYouTubeMoments(ctx context.Context, keyword string, meta *YouTubeClipMetadata) []SelectedMoment {
	maxMoments := getenvInt("VELOX_YOUTUBE_MAX_MOMENTS_PER_VIDEO", 6)
	if maxMoments < 1 {
		maxMoments = 1
	}
	minGap := float64(getenvInt("VELOX_YOUTUBE_MOMENT_MIN_GAP_SEC", 10))
	minDur := getenvInt("VELOX_YOUTUBE_MOMENT_MIN_SEC", 20)
	maxDur := getenvInt("VELOX_YOUTUBE_MOMENT_MAX_SEC", 55)
	if minDur < 6 {
		minDur = 6
	}
	if maxDur < minDur {
		maxDur = minDur
	}

	out := make([]SelectedMoment, 0, maxMoments)
	if meta != nil && len(meta.TranscriptSegments) > 0 {
		if gm := s.pickMomentsWithGemma(ctx, keyword, meta, minDur, maxDur, maxMoments); len(gm) > 0 {
			out = append(out, gm...)
		} else {
			out = append(out, pickMomentsHeuristicFromSegments(keyword, meta.TranscriptSegments, minDur, maxDur, maxMoments)...)
		}
	}
	if len(out) == 0 {
		out = append(out, s.pickYouTubeMoment(ctx, keyword, meta))
	}
	out = normalizeAndDedupeMoments(out, minDur, maxDur, minGap, maxMoments, meta)
	return out
}

func normalizeAndDedupeMoments(in []SelectedMoment, minDur, maxDur int, minGap float64, maxMoments int, meta *YouTubeClipMetadata) []SelectedMoment {
	out := make([]SelectedMoment, 0, len(in))
	for _, m := range in {
		if m.EndSec <= m.StartSec {
			m.EndSec = m.StartSec + float64(minDur)
		}
		if m.EndSec-m.StartSec < float64(minDur) {
			m.EndSec = m.StartSec + float64(minDur)
		}
		if m.EndSec-m.StartSec > float64(maxDur) {
			m.EndSec = m.StartSec + float64(maxDur)
		}
		if m.StartSec < 0 {
			m.StartSec = 0
		}
		if meta != nil && meta.DurationSec > 0 && m.EndSec > meta.DurationSec {
			m.EndSec = meta.DurationSec
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartSec < out[j].StartSec })
	dedup := make([]SelectedMoment, 0, len(out))
	for _, m := range out {
		if len(dedup) == 0 {
			dedup = append(dedup, m)
			continue
		}
		last := dedup[len(dedup)-1]
		if m.StartSec-last.StartSec < minGap {
			continue
		}
		dedup = append(dedup, m)
	}
	if len(dedup) > maxMoments {
		dedup = dedup[:maxMoments]
	}
	return dedup
}

func pickMomentsHeuristicFromSegments(keyword string, segments []TranscriptSegment, minDur, maxDur, maxMoments int) []SelectedMoment {
	out := make([]SelectedMoment, 0, maxMoments)
	if len(segments) == 0 {
		return out
	}
	usedStarts := make(map[int]bool)
	for len(out) < maxMoments {
		m, ok := pickMomentHeuristicFromSegments(keyword, segments, minDur, maxDur)
		if !ok {
			break
		}
		key := int(m.StartSec)
		if usedStarts[key] {
			break
		}
		usedStarts[key] = true
		out = append(out, m)
		// Shift window by removing early segments to surface a different moment.
		cut := m.EndSec
		rest := make([]TranscriptSegment, 0, len(segments))
		for _, s := range segments {
			if s.StartSec >= cut {
				rest = append(rest, s)
			}
		}
		if len(rest) == len(segments) {
			break
		}
		segments = rest
	}
	return out
}
