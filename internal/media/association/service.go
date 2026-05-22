package association

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"velox/go-master/internal/pkg/sliceutil"
	"velox/go-master/internal/pkg/termutil"
	"velox/go-master/internal/pkg/textutil"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	driveutil "velox/go-master/internal/storage/drive"
)

// Service matches script segments against media assets (stock, artlist, clips, Drive)
// using an extensible engine of association sources. It supports direct folder
// matching, semantic scoring, and candidate building for the visual planner.
type Service struct {
	dataDir        string
	nodeScraperDir string
	stockRepo      *clips.Repository
	artlistRepo    *clips.Repository
	clipsRepo      *clips.Repository
	catalogRepo    *catalog.Repository
	engine         *Engine
	clipSearch     *ClipSearchAssociation
	scriptsDir     string
}

// NewService creates an association service with the default engine and sources.
func NewService(dataDir, nodeScraperDir, scriptsDir string, stockRepo, artlistRepo, clipsRepo *clips.Repository, catalogRepo *catalog.Repository) *Service {
	s := &Service{
		dataDir:        dataDir,
		nodeScraperDir: nodeScraperDir,
		scriptsDir:     scriptsDir,
		stockRepo:      stockRepo,
		artlistRepo:    artlistRepo,
		clipsRepo:      clipsRepo,
		catalogRepo:    catalogRepo,
	}

	// Create clip search association (Artlist clips only)
	s.clipSearch = NewClipSearchAssociation(artlistRepo)

	// Default engine with standard sources
	s.engine = NewEngine(
		NewDriveStockAssociation(stockRepo, artlistRepo),
		NewArtlistStockAssociation(artlistRepo),
		NewClipDriveAssociation(clipsRepo),
		s.clipSearch,
	)

	return s
}

// RegisterAssociation adds an additional association source to the engine.
func (s *Service) RegisterAssociation(a Association) {
	s.engine.sources = append(s.engine.sources, a)
}

// Associate runs all registered association sources and returns scored matches.
// Stock drive matches receive a priority boost over Artlist results.
func (s *Service) Associate(ctx context.Context, input SegmentInput) []ScoredMatch {
	matches := s.engine.AssociateAll(ctx, input)

	// Boost stock drive priority over Artlist
	for i := range matches {
		src := strings.ToLower(matches[i].Source)
		if strings.Contains(src, "stock") && !strings.Contains(src, "artlist") {
			matches[i].Score += 50 // Significant boost to prioritize local stock
		}
	}

	return matches
}

// ScoreMedia re-scores candidates using a hybrid of linear and semantic scoring.
func (s *Service) ScoreMedia(ctx context.Context, query string, queryEmb []float32, candidates []ScoredMatch) []ScoredMatch {
	return s.engine.ScoreMedia(query, queryEmb, candidates)
}

// ResolvePreferredStockMatch returns the best direct stock folder match for the primary focus entity.
func (s *Service) ResolvePreferredStockMatch(ctx context.Context, input SegmentInput) (*ScoredMatch, bool) {
	matches := s.ResolveAllPreferredStockMatches(ctx, input)
	if len(matches) > 0 {
		return &matches[0], true
	}
	return nil, false
}

// ResolveAllPreferredStockMatches returns all direct stock folder matches for every core subject/entity.
func (s *Service) ResolveAllPreferredStockMatches(ctx context.Context, input SegmentInput) []ScoredMatch {
	var results []ScoredMatch
	seenFolders := make(map[string]bool)

	// Collect candidate terms: split the subject/topic into focused phrases as well.
	terms := collectFocusTerms(input)

	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" || len(term) < 3 {
			continue
		}

		direct, ok, err := s.FindDirectStockFolderCandidate(ctx, input.Topic, term)
		if err != nil || !ok || direct == nil {
			continue
		}

		folderKey := strings.ToLower(direct.FolderID)
		if folderKey == "" {
			folderKey = strings.ToLower(direct.Path)
		}
		if seenFolders[folderKey] {
			continue
		}
		seenFolders[folderKey] = true

		link := driveutil.NormalizeDriveFolderLink(direct.Link, direct.FolderID)

		results = append(results, ScoredMatch{
			Title:      direct.Name,
			Path:       direct.Path,
			Score:      1000,
			Source:     "drive_stock",
			Link:       link,
			FolderLink: link,
			FolderName: direct.Name,
			Reason:     fmt.Sprintf("direct protagonist stock folder match for '%s'", term),
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}

		di := folderPathDepth(results[i].Path)
		dj := folderPathDepth(results[j].Path)
		if di != dj {
			return di > dj
		}

		if len(results[i].Path) != len(results[j].Path) {
			return len(results[i].Path) > len(results[j].Path)
		}

		return results[i].Title < results[j].Title
	})

	return results
}

// collectFocusTerms extracts normalized focus phrases from a segment input,
// splitting compound subjects such as "A vs B" into individual names.
func collectFocusTerms(input SegmentInput) []string {
	terms := make([]string, 0)
	add := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		terms = append(terms, text)

		// Split compound subjects such as "Mike Tyson vs Jake Paul" into focused names.
		text = strings.NewReplacer(
			" vs ", "|",
			" versus ", "|",
			" and ", "|",
			" & ", "|",
			"/", "|",
			",", "|",
			"(", "|",
			")", "|",
		).Replace(text)
		for _, part := range strings.Split(text, "|") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			terms = append(terms, part)
		}
	}

	add(input.Subject)
	add(input.Topic)
	for _, entity := range input.Entities {
		add(entity)
	}

	// Include compact name-like phrases extracted from the raw fields.
	terms = append(terms, termutil.ExtractLikelyNames(input.Subject)...)
	terms = append(terms, termutil.ExtractLikelyNames(input.Topic)...)

	// Normalize and deduplicate.
	terms = textutil.NormalizeStringSlice(terms)
	return sliceutil.UniqueStrings(terms)
}

// BuildCandidates builds a ranked list of asset candidates for a script segment.
// It first attempts an exact direct folder match; if none is found, it scores
// stock and Artlist folders against the segment terms.
func (s *Service) BuildCandidates(ctx context.Context, req CandidatesRequest) (*CandidatesResponse, error) {
	req.Normalize()

	// 1. Direct match logic
	if direct, ok, err := s.FindDirectStockFolderCandidate(ctx, req.Topic, req.Subject); err == nil && ok {
		link := direct.Link
		link = driveutil.NormalizeDriveFolderLink(link, direct.FolderID)
		candidate := Candidate{
			Database: "stock.db.sqlite",
			Source:   "stock_folder",
			Name:     direct.Name,
			Path:     direct.Path,
			FolderID: direct.FolderID,
			Link:     link,
			Score:    300,
			Reason:   "direct exact stock folder match",
		}
		return &CandidatesResponse{
			OK:         true,
			Topic:      req.Topic,
			SegmentKey: req.SegmentKey,
			Timestamp:  req.Timestamp,
			Subject:    req.Subject,
			TopK:       req.TopK,
			Candidates: []Candidate{candidate},
		}, nil
	}

	terms := collectTerms(req)
	rawCandidates := make([]Candidate, 0)

	if folders, err := s.buildStockFolderCandidates(ctx); err == nil {
		rawCandidates = append(rawCandidates, scoreFolderCandidates("stock.db.sqlite", "stock_folder", folders, terms, req.Subject, req.Topic)...)
	}

	if folders, err := s.buildArtlistFolderCandidates(ctx); err == nil {
		rawCandidates = append(rawCandidates, scoreFolderCandidates("artlist_videos.db", "artlist_folder", folders, terms, req.Subject, req.Topic)...)
	}

	deduped := s.dedupe(rawCandidates)
	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].Score == deduped[j].Score {
			return deduped[i].Name < deduped[j].Name
		}
		return deduped[i].Score > deduped[j].Score
	})
	if req.TopK > 0 && len(deduped) > req.TopK {
		deduped = deduped[:req.TopK]
	}

	return &CandidatesResponse{
		OK:         true,
		Topic:      req.Topic,
		SegmentKey: req.SegmentKey,
		Timestamp:  req.Timestamp,
		Subject:    req.Subject,
		TopK:       req.TopK,
		Candidates: deduped,
	}, nil
}

// FindDirectStockFolderCandidate searches for an exact stock folder matching
// the topic or subject. It returns the best candidate and a boolean indicating success.
func (s *Service) FindDirectStockFolderCandidate(ctx context.Context, topic, subject string) (*FolderCandidate, bool, error) {
	folders, err := s.buildStockFolderCandidates(ctx)
	if err != nil {
		return nil, false, err
	}
	best, ok := s.directFolderMatch(folders, topic, subject)
	if !ok {
		return nil, false, nil
	}
	return &best, true, nil
}

func (s *Service) directFolderMatch(folders []FolderCandidate, topic, subject string) (FolderCandidate, bool) {
	focuses := []string{}
	subject = strings.TrimSpace(subject)
	topic = strings.TrimSpace(topic)
	if subject != "" {
		focuses = append(focuses, subject)
	} else if topic != "" {
		focuses = append(focuses, topic)
	}
	bestScore := 0
	bestDepth := -1
	var best FolderCandidate

	for _, folder := range folders {
		name := normalizeKey(folder.Name)
		path := normalizeKey(folder.Path)

		link := folder.Link
		if link == "" && folder.FolderID != "" {
			link = "https://drive.google.com/drive/folders/" + folder.FolderID
		}

		if name == "" && path == "" {
			continue
		}
		folderDepth := strings.Count(path, "/")
		for _, focus := range focuses {
			focus = normalizeKey(focus)
			if focus == "" {
				continue
			}
			score := 0
			switch {
			case name == focus:
				score = 300
			case path == focus:
				score = 280
			case strings.HasSuffix(path, "/"+focus):
				score = 260
			case strings.Contains(name, focus) && len(focus) >= 3:
				score = 220
			case strings.Contains(path, focus) && len(focus) >= 3:
				score = 200
			default:
				continue
			}
			// Favor more specific folders when the textual match is otherwise comparable.
			score += folderDepth * 10
			if score > bestScore || (score == bestScore && folderDepth > bestDepth) {
				bestScore = score
				bestDepth = folderDepth
				best = folder
				best.Link = link
			}
		}
	}

	if bestScore == 0 {
		return FolderCandidate{}, false
	}
	return best, true
}

func folderPathDepth(path string) int {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.Trim(path, "/")
	if path == "" {
		return 0
	}
	return strings.Count(path, "/")
}

func (s *Service) dedupe(candidates []Candidate) []Candidate {
	if len(candidates) == 0 {
		return nil
	}
	seen := make(map[string]Candidate, len(candidates))
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

	out := make([]Candidate, 0, len(seen))
	for _, candidate := range seen {
		out = append(out, candidate)
	}
	return out
}
