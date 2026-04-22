package scriptdocs

import (
	"context"
	"strings"

	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/stockjit"
)

func (s *ScriptDocService) resolveJITClip(ctx context.Context, topic, phrase string, startTime, endTime int) *stockjit.Result {
	if s == nil || s.jitResolver == nil {
		return nil
	}
	res, err := s.jitResolver.Resolve(ctx, stockjit.Request{
		Topic:     topic,
		Phrase:    phrase,
		Keywords:  s.extractClipKeywords([]string{phrase}, nil, nil),
		StartTime: startTime,
		EndTime:   endTime,
		Duration:  maxInt(0, endTime-startTime),
		MediaType: "stock",
		AllowJIT:  true,
	})
	if err != nil || res == nil {
		return nil
	}
	return res
}

func (s *ScriptDocService) jitResultToAssociation(res *stockjit.Result) *ClipAssociation {
	if res == nil {
		return nil
	}
	assoc := &ClipAssociation{
		Phrase:         res.Phrase,
		Confidence:     res.Confidence,
		MatchedKeyword: res.Keyword,
		Resolution: newAssetResolution(
			"jit-stock",
			"stockdb",
			"artlist",
			"youtube",
		).withOutcome(strings.TrimSpace(res.SourceKind), strings.TrimSpace(res.ApprovalReason), res.Cached),
	}
	if assoc.Resolution != nil {
		assoc.Resolution.RequestKey = strings.TrimSpace(res.RequestID)
	}
	if strings.TrimSpace(res.SourceKind) == "artlist" {
		assoc.Type = "ARTLIST"
		assoc.Clip = &ArtlistClip{
			Name:     res.Filename,
			Term:     res.Keyword,
			URL:      res.DriveURL,
			Folder:   res.FolderPath,
			FolderID: res.FolderID,
		}
		return assoc
	}
	assoc.Type = "STOCK_DB"
	assoc.ClipDB = &stockdb.StockClipEntry{
		ClipID:   res.DriveID,
		FolderID: res.FolderID,
		Filename: res.Filename,
		Source:   "jit_stock",
		Tags:     resTags(res),
		Duration: maxInt(0, res.EndTime-res.StartTime),
		Status:   "uploaded",
	}
	return assoc
}

func resTags(res *stockjit.Result) []string {
	if res == nil {
		return nil
	}
	tags := []string{strings.ToLower(strings.TrimSpace(res.Keyword))}
	if t := strings.ToLower(strings.TrimSpace(res.Topic)); t != "" {
		tags = append(tags, t)
	}
	if p := strings.ToLower(strings.TrimSpace(res.Phrase)); p != "" {
		tags = append(tags, p)
	}
	return tags
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
