package script

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/media/association"
	artlist "velox/go-master/internal/sources/artlist"
)

// attemptLiveSearchDecision tenta una ricerca live per la decisione dell'asset
func attemptLiveSearchDecision(ctx context.Context, req ScriptDocsRequest, segment TimelineSegment, artlistSvc *artlist.Service) (timelineAssetDecision, bool) {
	if artlistSvc == nil {
		return timelineAssetDecision{}, false
	}

	searchTerm := segment.Subject
	if searchTerm == "" && len(segment.Keywords) > 0 {
		searchTerm = segment.Keywords[0]
	}
	if searchTerm == "" {
		searchTerm = req.Topic
	}

	liveResp, runResp, err := artlistSvc.DiscoverAndQueueRun(ctx, searchTerm, 10)
	if err != nil || liveResp == nil || len(liveResp.Clips) == 0 {
		return timelineAssetDecision{}, false
	}

	reason := buildLiveSearchReason(searchTerm, runResp)
	folderLink := extractFolderLink(runResp)
	matches := buildLiveSearchMatches(ctx, artlistSvc, liveResp, runResp, searchTerm, folderLink)
	if len(matches) == 0 {
		return timelineAssetDecision{}, false
	}

	return timelineAssetDecision{
		Source:  string(timelineAssetSourceArtlistDynamic),
		Folder:  "live:" + searchTerm,
		Reason:  reason,
		Matches: matches,
	}, true
}

func buildLiveSearchReason(searchTerm string, runResp *artlist.RunTagResponse) string {
	reason := fmt.Sprintf("Live search for '%s' found clips", searchTerm)
	if runResp != nil && runResp.RunID != "" {
		reason += " while queuing run " + runResp.RunID
	}
	return reason
}

func extractFolderLink(runResp *artlist.RunTagResponse) string {
	if runResp == nil {
		return ""
	}
	if strings.TrimSpace(runResp.TagFolderID) != "" {
		return "https://drive.google.com/drive/folders/" + strings.TrimSpace(runResp.TagFolderID)
	}
	if strings.TrimSpace(runResp.RootFolderID) != "" {
		return "https://drive.google.com/drive/folders/" + strings.TrimSpace(runResp.RootFolderID)
	}
	return ""
}

func buildLiveSearchMatches(
	ctx context.Context,
	artlistSvc *artlist.Service,
	liveResp *artlist.SearchResponse,
	runResp *artlist.RunTagResponse,
	searchTerm string,
	folderLink string,
) []association.ScoredMatch {
	processedResp := runResp
	if processedResp != nil && processedResp.RunID != "" && artlistSvc != nil {
		if completed := waitForArtlistRunCompletion(ctx, artlistSvc, processedResp.RunID, 10*time.Minute); completed != nil {
			processedResp = completed
			folderLink = extractFolderLink(processedResp)
		}
	}

	matches := make([]association.ScoredMatch, 0)

	if strings.TrimSpace(folderLink) != "" {
		matches = append(matches, association.ScoredMatch{
			Title:      searchTerm,
			Score:      100,
			Source:     "artlist_live_run",
			Link:       folderLink,
			FolderLink: folderLink,
			FolderName: searchTerm,
			Reason:     "live artlist run folder for " + searchTerm,
		})
	}

	if processedResp != nil && len(processedResp.Items) > 0 {
		for _, item := range processedResp.Items {
			if strings.TrimSpace(item.DriveLink) == "" {
				continue
			}
			matches = append(matches, association.ScoredMatch{
				Title:      item.Name,
				Path:       item.LocalPath,
				Score:      95,
				Source:     "artlist_live_discovery",
				Link:       item.DriveLink,
				FolderName: searchTerm,
				Reason:     "live search: " + searchTerm,
			})
		}
		if len(matches) > 0 {
			return matches
		}
	}

	if liveResp != nil {
		for _, clip := range liveResp.Clips {
			if strings.TrimSpace(clip.DriveLink) == "" {
				continue
			}
			matches = append(matches, association.ScoredMatch{
				Title:      clip.Name,
				Path:       clip.LocalPath,
				Score:      90,
				Source:     "artlist_live_discovery",
				Link:       clip.DriveLink,
				FolderName: searchTerm,
				Reason:     "live search: " + searchTerm,
			})
		}
	}

	return matches
}

func waitForArtlistRunCompletion(ctx context.Context, svc *artlist.Service, runID string, timeout time.Duration) *artlist.RunTagResponse {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	check := func() *artlist.RunTagResponse {
		job, err := svc.GetJobByRunID(ctx, runID)
		if err != nil || job == nil {
			return nil
		}
		if !job.Status.IsTerminal() {
			return nil
		}
		return artlist.JobToRunTagResponse(job)
	}

	if completed := check(); completed != nil {
		return completed
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deadline.C:
			return nil
		case <-ticker.C:
			if completed := check(); completed != nil {
				return completed
			}
		}
	}
}
