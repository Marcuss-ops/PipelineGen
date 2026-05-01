package script

import (
	"context"
	"sort"
	"strings"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/sliceutil"
)

type AssociationCandidatesRequest struct {
	Topic      string   `json:"topic"`
	SegmentKey string   `json:"segment_key,omitempty"`
	Timestamp  string   `json:"timestamp,omitempty"`
	Subject    string   `json:"subject"`
	Narrative  string   `json:"narrative,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
	Entities   []string `json:"entities,omitempty"`
	TopK       int      `json:"top_k,omitempty"`
}

type AssociationCandidate struct {
	Database string `json:"database"`
	Source   string `json:"source"`
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	FolderID string `json:"folder_id,omitempty"`
	Link     string `json:"link,omitempty"`
	Score    int    `json:"score"`
	Reason   string `json:"reason,omitempty"`
}

type AssociationCandidatesResponse struct {
	OK         bool                   `json:"ok"`
	Topic      string                 `json:"topic,omitempty"`
	SegmentKey string                 `json:"segment_key,omitempty"`
	Timestamp  string                 `json:"timestamp,omitempty"`
	Subject    string                 `json:"subject,omitempty"`
	TopK       int                    `json:"top_k,omitempty"`
	Candidates []AssociationCandidate `json:"candidates,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

func (r *AssociationCandidatesRequest) Normalize() {
	if r.TopK <= 0 {
		r.TopK = 10
	}
	r.Topic = strings.TrimSpace(r.Topic)
	r.SegmentKey = strings.TrimSpace(r.SegmentKey)
	r.Timestamp = strings.TrimSpace(r.Timestamp)
	r.Subject = strings.TrimSpace(r.Subject)
	r.Narrative = strings.TrimSpace(r.Narrative)
	r.Keywords = sliceutil.UniqueStrings(sliceutil.TrimStrings(r.Keywords))
	r.Entities = sliceutil.UniqueStrings(sliceutil.TrimStrings(r.Entities))
}

func trimStrings(items []string) []string {
	return sliceutil.TrimStrings(items)
}

func BuildAssociationCandidates(ctx context.Context, req AssociationCandidatesRequest, dataDir, nodeScraperDir string, stockRepo, artlistRepo, clipsRepo *clips.Repository) (*AssociationCandidatesResponse, error) {
	if direct, ok, err := findDirectStockFolderCandidate(ctx, stockRepo, dataDir, req.Topic, req.Subject); err == nil && ok && direct != nil {
		link := normalizeDriveFolderLink(direct.Link, direct.FolderID)
		candidate := AssociationCandidate{
			Database: "stock.db.sqlite",
			Source:   "stock_folder",
			Name:     direct.Name,
			Path:     direct.Path,
			FolderID: direct.FolderID,
			Link:     link,
			Score:    300,
			Reason:   "direct exact stock folder match",
		}
		return &AssociationCandidatesResponse{
			OK:         true,
			Topic:      req.Topic,
			SegmentKey: req.SegmentKey,
			Timestamp:  req.Timestamp,
			Subject:    req.Subject,
			TopK:       req.TopK,
			Candidates: []AssociationCandidate{candidate},
		}, nil
	}

	terms := collectAssociationTerms(req)

	rawCandidates := make([]AssociationCandidate, 0)

	if folders, err := buildTimelineStockFolderCandidates(ctx, stockRepo, dataDir); err == nil {
		rawCandidates = append(rawCandidates, scoreTimelineFolderCandidates("stock.db.sqlite", "stock_folder", folders, terms, req.Subject, req.Topic)...)
	}

	if folders, err := buildTimelineClipFolderCandidates(ctx, clipsRepo); err == nil {
		rawCandidates = append(rawCandidates, scoreTimelineFolderCandidates("clips.db.sqlite", "clip_folder", folders, terms, req.Subject, req.Topic)...)
	}

	if folders, err := buildTimelineArtlistFolderCandidates(ctx, artlistRepo, nodeScraperDir); err == nil {
		rawCandidates = append(rawCandidates, scoreTimelineFolderCandidates("artlist_videos.db", "artlist_folder", folders, terms, req.Subject, req.Topic)...)
	}

	deduped := dedupeAssociationCandidates(rawCandidates)
	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].Score == deduped[j].Score {
			return deduped[i].Name < deduped[j].Name
		}
		return deduped[i].Score > deduped[j].Score
	})
	if req.TopK > 0 && len(deduped) > req.TopK {
		deduped = deduped[:req.TopK]
	}

	return &AssociationCandidatesResponse{
		OK:         true,
		Topic:      req.Topic,
		SegmentKey: req.SegmentKey,
		Timestamp:  req.Timestamp,
		Subject:    req.Subject,
		TopK:       req.TopK,
		Candidates: deduped,
	}, nil
}

func applyAssociationHints(seg *TimelineSegment, resp *AssociationCandidatesResponse) {
	if seg == nil || resp == nil || len(resp.Candidates) == 0 {
		return
	}
	best := resp.Candidates[0]
	seg.PreferredStockReason = best.Reason
	seg.PreferredStockGroup = best.Source
	preferredLink := normalizeDriveFolderLink(best.Link, best.FolderID)
	seg.PreferredStockPaths = sliceutil.UniqueStrings(sliceutil.TrimStrings([]string{best.Path, preferredLink}))
}

func collectAssociationTerms(req AssociationCandidatesRequest) []string {
	terms := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(text string) {
		for _, term := range matching.Tokenize(text) {
			term = strings.TrimSpace(term)
			if term == "" || len(term) < 3 || matching.IsStopWord(term) {
				continue
			}
			key := strings.ToLower(term)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			terms = append(terms, term)
		}
	}

	add(req.Topic)
	add(req.Subject)
	add(req.Narrative)
	add(strings.Join(req.Keywords, " "))
	add(strings.Join(req.Entities, " "))

	return terms
}

func scoreTimelineFolderCandidates(database, source string, folders []timelineFolderCandidate, terms []string, focusTexts ...string) []AssociationCandidate {
	candidates := make([]AssociationCandidate, 0, len(folders))
	focusKeys := make([]string, 0, len(focusTexts))
	for _, focusText := range focusTexts {
		if key := normalizeAssociationKey(focusText); key != "" {
			focusKeys = append(focusKeys, key)
		}
	}
	for _, folder := range folders {
		name := strings.TrimSpace(folder.Name)
		path := strings.TrimSpace(folder.Path)
		link := strings.TrimSpace(folder.Link)
		if name == "" && path == "" && link == "" {
			continue
		}

		candidateText := strings.ToLower(strings.Join([]string{name, path, link}, " "))
		score := scoreText(candidateText, terms)
		if score == 0 {
			continue
		}
		if name != "" {
			slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
			for _, term := range terms {
				termSlug := strings.ToLower(strings.ReplaceAll(term, " ", "-"))
				if strings.Contains(slug, termSlug) || strings.Contains(termSlug, slug) {
					score += 15
					break
				}
			}
		}
		if source == "stock_folder" {
			folderKey := normalizeAssociationKey(name)
			pathKey := normalizeAssociationKey(path)
			focusTokenCount := 0
			for _, focusKey := range focusKeys {
				if count := len(matchTokens(focusKey)); count > 0 && (focusTokenCount == 0 || count < focusTokenCount) {
					focusTokenCount = count
				}
			}
			for _, focusKey := range focusKeys {
				if focusKey == "" {
					continue
				}
				if folderKey == focusKey || pathKey == focusKey {
					score += 60
					break
				}
				if strings.HasSuffix(pathKey, "/"+focusKey) {
					score += 35
					break
				}
			}
			if focusTokenCount > 0 {
				candidateTokenCount := len(matchTokens(name + " " + path))
				if candidateTokenCount >= focusTokenCount+4 {
					continue
				}
				if candidateTokenCount > focusTokenCount {
					score -= (candidateTokenCount - focusTokenCount) / 2
				}
			}
		}
		if score > 100 {
			score = 100
		}

		candidates = append(candidates, AssociationCandidate{
			Database: database,
			Source:   source,
			Name:     name,
			Path:     path,
			FolderID: folder.FolderID,
			Link:     link,
			Score:    score,
			Reason:   "token overlap on segment subject/keywords/entities",
		})
	}
	return candidates
}

func normalizeAssociationKey(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "_", " ")
	text = strings.ReplaceAll(text, "-", " ")
	text = strings.Join(strings.Fields(text), " ")
	return text
}

func buildTimelineClipFolderCandidates(ctx context.Context, repo *clips.Repository) ([]timelineFolderCandidate, error) {
	if repo == nil {
		return nil, nil
	}
	records, err := loadClipsFromDB(ctx, repo, "")
	if err != nil {
		return nil, err
	}
	return buildCandidatesFromRecords(records, ""), nil
}

func dedupeAssociationCandidates(candidates []AssociationCandidate) []AssociationCandidate {
	if len(candidates) == 0 {
		return nil
	}
	seen := make(map[string]AssociationCandidate, len(candidates))
	for _, candidate := range candidates {
		key := strings.ToLower(strings.TrimSpace(candidate.Database) + "|" + strings.TrimSpace(candidate.Name) + "|" + strings.TrimSpace(candidate.Path) + "|" + strings.TrimSpace(candidate.Link))
		if existing, ok := seen[key]; ok {
			if candidate.Score > existing.Score {
				seen[key] = candidate
			}
			continue
		}
		seen[key] = candidate
	}

	out := make([]AssociationCandidate, 0, len(seen))
	for _, candidate := range seen {
		out = append(out, candidate)
	}
	return out
}
