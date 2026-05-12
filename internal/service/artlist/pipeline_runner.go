package artlist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	driveutil "velox/go-master/pkg/drive"
	"velox/go-master/pkg/models"
	"velox/go-master/pkg/pathutil"
)

type artlistChecksumChecker struct {
	driveClient *driveapi.Service
}

func (c *artlistChecksumChecker) GetMD5Checksum(ctx context.Context, driveLink string) (string, error) {
	fileID := driveutil.FileIDFromLink(driveLink)
	if fileID == "" {
		return "", fmt.Errorf("could not extract file ID from link: %s", driveLink)
	}
	file, err := c.driveClient.Files.Get(fileID).Fields("id,md5Checksum").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(file.Md5Checksum), nil
}

// RunTag executes the full Artlist pipeline for one search term
func (s *Service) RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
	resp := &RunTagResponse{
		OK:        true,
		Term:      strings.TrimSpace(req.Term),
		StartedAt: func() *string { t := time.Now().UTC().Format(time.RFC3339); return &t }(),
	}

	if resp.Term == "" {
		resp.OK = false
		resp.Error = "term is required"
		return resp, fmt.Errorf("term is required")
	}

	// Assume request is already normalized
	rootFolderID := req.RootFolderID
	strategy := req.Strategy
	resp.Requested = req.Limit
	resp.DryRun = req.DryRun
	resp.Strategy = req.Strategy
	resp.RootFolderID = req.RootFolderID

	if s.assetDestResolver == nil && !req.DryRun {
		s.log.Warn("drive service not configured, proceeding with local harvesting only")
	}

	tagFolderName := pathutil.SafeFolderName(resp.Term)
	s.log.Info("artlist pipeline start",
		zap.String("term", resp.Term),
		zap.Int("limit", req.Limit),
		zap.String("root_folder_id", rootFolderID),
		zap.String("strategy", strategy),
		zap.Bool("dry_run", req.DryRun),
		zap.String("tag_folder_name", tagFolderName),
	)

	// Step 0: Ensure we have clips in the DB via live search if none found
	clipsList, err := s.ensureClips(ctx, resp.Term, req.Limit, resp)
	if err != nil {
		resp.OK = false
		resp.Status = "failed"
		return resp, err
	}

	s.log.Info("clips available for processing", zap.String("term", resp.Term), zap.Int("count", len(clipsList)))

	if len(clipsList) == 0 {
		s.log.Warn("no clips found even after live search, terminating pipeline", zap.String("term", resp.Term))
		resp.Status = "completed"
		resp.OK = true
		return resp, nil
	}

	resp.Found = len(clipsList)
	resp.EstimatedSize = resp.Found
	if lastProcessedAt, err := s.lastProcessedAtForTerm(ctx, resp.Term); err == nil {
		resp.LastProcessedAt = lastProcessedAt
	}

	// Step 1: Resolve Drive destination
	tagFolderID := s.resolveDestination(ctx, rootFolderID, resp.Term, tagFolderName, resp)
	resp.TagFolderID = tagFolderID

	// Step 2: Select candidate clips up to limit
	candidateClips := s.selectCandidates(clipsList, req.Limit)

	// Step 3: Process candidates
	s.processCandidates(ctx, candidateClips, tagFolderID, tagFolderName, resp, req)

	resp.Status = "completed"
	resp.EndedAt = func() *string { t := time.Now().UTC().Format(time.RFC3339); return &t }()
	s.log.Info("artlist pipeline complete",
		zap.String("term", resp.Term),
		zap.Int("found", resp.Found),
		zap.Int("processed", resp.Processed),
		zap.Int("skipped", resp.Skipped),
		zap.Int("failed", resp.Failed),
		zap.String("tag_folder_id", resp.TagFolderID),
	)

	return resp, nil
}

// processCandidates processes the candidate clips, handling dry-run and normal processing
func (s *Service) processCandidates(ctx context.Context, candidates []*models.Clip, tagFolderID, tagFolderName string, resp *RunTagResponse, req *RunTagRequest) {
	if req.DryRun {
		s.processDryRun(ctx, candidates, resp)
		return
	}
	for _, clip := range candidates {
		s.processClip(ctx, clip, tagFolderID, tagFolderName, resp, req)
	}
}
