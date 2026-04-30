package script

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
	artlist "velox/go-master/internal/service/artlist"
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

	liveResp, runResp, err := artlistSvc.DiscoverAndQueueRun(ctx, searchTerm, 5)
	if err != nil || liveResp == nil || len(liveResp.Clips) == 0 {
		return timelineAssetDecision{}, false
	}

	reason := buildLiveSearchReason(searchTerm, runResp)
	folderLink := extractFolderLink(runResp)
	
	return timelineAssetDecision{
		Source:  string(timelineAssetSourceArtlistDynamic),
		Folder:  "live:" + searchTerm,
		Reason:  reason,
		Matches: modelClipsToScoredMatches(liveResp.Clips, reason, "artlist dynamic search", folderLink),
	}, true
}

func buildLiveSearchReason(searchTerm string, runResp *RunTagResponse) string {
	reason := fmt.Sprintf("Live search for '%s' found clips", searchTerm)
	if runResp != nil && runResp.RunID != "" {
		reason += " while queuing run " + runResp.RunID
	}
	return reason
}

func extractFolderLink(runResp *RunTagResponse) string {
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
