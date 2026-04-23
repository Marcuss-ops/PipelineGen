package clipsearch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/stockdb"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

var nonWordRe = regexp.MustCompile(`[^a-z0-9\s]+`)

type ClipFinder struct {
	stockDB   *stockdb.StockDB
	artlistDB *artlistdb.ArtlistDB
}

func NewClipFinder(stockDB *stockdb.StockDB, artlistDB *artlistdb.ArtlistDB) *ClipFinder {
	return &ClipFinder{
		stockDB:   stockDB,
		artlistDB: artlistDB,
	}
}

func (f *ClipFinder) FindClipInDB(keyword string) (*SearchResult, error) {
	if hit := f.findInStockDB(keyword); hit != nil {
		return hit, nil
	}

	if hit := f.findInArtlistDB(keyword); hit != nil {
		return hit, nil
	}

	return nil, fmt.Errorf("clip not found for keyword: %s", keyword)
}

func (f *ClipFinder) findInStockDB(keyword string) *SearchResult {
	if f.stockDB == nil {
		return nil
	}
	allClips, err := f.stockDB.GetAllClips()
	if err != nil {
		return nil
	}
	keywordLower := " " + strings.ToLower(keyword) + " "
	for _, c := range allClips {
		tags := " " + strings.ToLower(strings.Join(c.Tags, " ")) + " "
		filename := " " + strings.ToLower(c.Filename) + " "
		if strings.Contains(tags, keywordLower) || strings.Contains(filename, keywordLower) {
			return stockEntryToSearchResult(keyword, c)
		}
	}
	return nil
}

func (f *ClipFinder) findInArtlistDB(keyword string) *SearchResult {
	if f.artlistDB == nil {
		return nil
	}
	hits, err := f.artlistDB.FindDownloadedClipsWithSimilarTags([]string{keyword}, 1)
	if err != nil || len(hits) == 0 {
		return nil
	}
	return artlistEntryToSearchResult(keyword, hits[0])
}

func (f *ClipFinder) FindDownloadedArtlistBySource(keyword string, source clip.IndexedClip) *SearchResult {
	if f.artlistDB == nil {
		return nil
	}
	clips, ok := f.artlistDB.GetDownloadedClipsForTerm(keyword)
	if !ok || len(clips) == 0 {
		return nil
	}

	sourceURL := strings.TrimSpace(resolveArtlistSourceURL(source))
	sourceID := strings.TrimSpace(source.ID)
	for _, c := range clips {
		if c.DriveFileID == "" || c.DriveURL == "" {
			continue
		}
		if sourceID != "" && strings.TrimSpace(c.ID) == sourceID {
			return artlistEntryToSearchResult(keyword, c)
		}
		if sourceURL != "" && (strings.TrimSpace(c.OriginalURL) == sourceURL || strings.TrimSpace(c.URL) == sourceURL) {
			return artlistEntryToSearchResult(keyword, c)
		}
	}
	return nil
}

func (f *ClipFinder) FindDownloadedArtlistByVisualAndTitle(keyword, visualHash, title string) *SearchResult {
	if f.artlistDB == nil {
		return nil
	}
	if strings.TrimSpace(visualHash) == "" || strings.TrimSpace(title) == "" {
		return nil
	}
	clips, ok := f.artlistDB.GetDownloadedClipsForTerm(keyword)
	if !ok || len(clips) == 0 {
		return nil
	}
	for _, c := range clips {
		if c.DriveFileID == "" || c.DriveURL == "" {
			continue
		}
		if strings.TrimSpace(c.VisualHash) == "" || strings.TrimSpace(c.Name) == "" {
			continue
		}
		if c.VisualHash != visualHash {
			continue
		}
		if titleSimilarity(c.Name, title) >= 0.72 {
			return artlistEntryToSearchResult(keyword, c)
		}
	}
	return nil
}

// FindDownloadedByVisualHash searches any downloaded clip (across all terms) by visual hash.
// This prevents duplicate uploads when the same clip is discovered through different keywords.
func (f *ClipFinder) FindDownloadedByVisualHash(visualHash string) *SearchResult {
	if f.artlistDB == nil {
		return nil
	}
	hash := strings.TrimSpace(visualHash)
	if hash == "" {
		return nil
	}
	terms := f.artlistDB.GetAllTerms()
	for _, term := range terms {
		clips, ok := f.artlistDB.GetDownloadedClipsForTerm(term)
		if !ok || len(clips) == 0 {
			continue
		}
		for _, c := range clips {
			if strings.TrimSpace(c.VisualHash) == "" {
				continue
			}
			if c.VisualHash == hash {
				return artlistEntryToSearchResult(term, c)
			}
		}
	}
	return nil
}

func (f *ClipFinder) FindDownloadedYouTubeByMeta(meta *YouTubeClipMetadata) *SearchResult {
	if f.artlistDB == nil || meta == nil {
		return nil
	}
	videoID := strings.TrimSpace(strings.ToLower(meta.VideoID))
	hash := buildYouTubeInterviewHash(meta)
	if videoID == "" && hash == "" {
		return nil
	}
	videoTag := "yt_video_id:" + videoID
	hashTag := "yt_hash:" + hash

	terms := f.artlistDB.GetAllTerms()
	for _, term := range terms {
		clips, ok := f.artlistDB.GetDownloadedClipsForTerm(term)
		if !ok || len(clips) == 0 {
			continue
		}
		for _, c := range clips {
			if c.DriveFileID == "" || c.DriveURL == "" {
				continue
			}
			if hash != "" && containsTag(c.Tags, hashTag) {
				return artlistEntryToSearchResult(term, c)
			}
			if videoID != "" && containsTag(c.Tags, videoTag) {
				return artlistEntryToSearchResult(term, c)
			}
			urlBlob := strings.ToLower(strings.TrimSpace(c.OriginalURL + " " + c.URL + " " + c.Name + " " + c.Title))
			if videoID != "" && strings.Contains(urlBlob, videoID) {
				return artlistEntryToSearchResult(term, c)
			}
		}
	}
	return nil
}

func titleSimilarity(a, b string) float64 {
	ta := tokenizeTitle(a)
	tb := tokenizeTitle(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	setA := make(map[string]bool, len(ta))
	setB := make(map[string]bool, len(tb))
	for _, t := range ta {
		setA[t] = true
	}
	for _, t := range tb {
		setB[t] = true
	}
	inter := 0
	for t := range setA {
		if setB[t] {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union <= 0 {
		return 0
	}
	jaccard := float64(inter) / float64(union)
	lenPenalty := 1.0 - math.Min(0.25, math.Abs(float64(len(ta)-len(tb)))/20.0)
	return jaccard * lenPenalty
}

func tokenizeTitle(s string) []string {
	n := strings.ToLower(strings.TrimSpace(s))
	n = nonWordRe.ReplaceAllString(n, " ")
	parts := strings.Fields(n)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) >= 3 {
			out = append(out, p)
		}
	}
	return out
}

func stockEntryToSearchResult(keyword string, clip stockdb.StockClipEntry) *SearchResult {
	return &SearchResult{
		Keyword:  keyword,
		ClipID:   clip.ClipID,
		Filename: clip.Filename,
		Source:   "stock",
		DriveURL: fmt.Sprintf("https://drive.google.com/file/d/%s/view", clip.ClipID),
		DriveID:  clip.ClipID,
		Folder:   clip.FolderID,
		FolderID: clip.FolderID,
	}
}

func artlistEntryToSearchResult(keyword string, clip artlistdb.ArtlistClip) *SearchResult {
	return &SearchResult{
		Keyword:  keyword,
		ClipID:   clip.DriveFileID,
		Filename: clip.Name,
		Source:   "artlist",
		DriveURL: clip.DriveURL,
		DriveID:  clip.DriveFileID,
		Folder:   clip.Folder,
		FolderID: clip.FolderID,
	}
}

func (s *Service) SearchClips(ctx context.Context, keywords []string) ([]SearchResult, error) {
	return s.SearchClipsWithOptions(ctx, keywords, SearchOptions{})
}

func (s *Service) SearchClipsWithOptions(ctx context.Context, keywords []string, opts SearchOptions) ([]SearchResult, error) {
	normalizedKeywords := normalizeKeywords(keywords)
	results, newUploads := s.processKeywords(ctx, normalizedKeywords, opts)

	if newUploads > 0 {
		s.runPostCycleSync(ctx, newUploads)
	}

	return results, nil
}

func (s *Service) processKeywords(ctx context.Context, keywords []string, opts SearchOptions) ([]SearchResult, int) {
	if opts.MaxClipsPerKeyword > 1 {
		return s.processKeywordsMulti(ctx, keywords, opts)
	}

	newUploads := int32(0)
	g, gCtx := errgroup.WithContext(ctx)

	var mu sync.Mutex
	finalResults := make([]SearchResult, 0, len(keywords))

	for _, kw := range keywords {
		kw := kw // capture
		g.Go(func() error {
			// Acquire semaphore
			select {
			case s.workerSemaphore <- struct{}{}:
				defer func() { <-s.workerSemaphore }()
			case <-gCtx.Done():
				return gCtx.Err()
			}

			jobID := s.ensureKeywordJobCheckpoint(kw)
			res, uploaded, found := s.processKeyword(gCtx, kw, opts, jobID)
			if found {
				mu.Lock()
				finalResults = append(finalResults, res)
				if uploaded {
					newUploads++
				}
				mu.Unlock()
			}
			return nil
		})
	}

	_ = g.Wait()
	return finalResults, int(newUploads)
}

func (s *Service) processKeyword(ctx context.Context, kw string, opts SearchOptions, jobID string) (SearchResult, bool, bool) {
	s.markCheckpoint(jobID, ClipJobStatusSearched, "", nil)

	if !opts.ForceFresh {
		existing, err := s.finder.FindClipInDB(kw)
		if err == nil && existing != nil {
			logger.Info("Found clip in DB cache",
				zap.String("keyword", kw),
				zap.String("clip_id", existing.ClipID),
			)
			s.markCheckpoint(jobID, ClipJobStatusDone, "", existing)
			return *existing, false, true
		}
	}

	if shouldPreferYouTubeKeyword(kw) {
		if result, uploaded, found := s.processYTDLPKeyword(ctx, kw, jobID); found {
			return result, uploaded, true
		}
		if result, uploaded, found := s.processArtlistKeyword(ctx, kw, jobID); found {
			return result, uploaded, true
		}
	} else {
		if result, uploaded, found := s.processArtlistKeyword(ctx, kw, jobID); found {
			return result, uploaded, true
		}
		if result, uploaded, found := s.processYTDLPKeyword(ctx, kw, jobID); found {
			return result, uploaded, true
		}
	}
	return SearchResult{}, false, false
}

func (s *Service) processArtlistKeyword(ctx context.Context, kw string, jobID string) (SearchResult, bool, bool) {
	artlistPath, artlistClip, err := s.downloadFromArtlist(ctx, kw)
	if err != nil {
		logger.Warn("Artlist direct download failed, falling back to yt-dlp search",
			zap.String("keyword", kw),
			zap.Error(err),
		)
		return SearchResult{}, false, false
	}
	s.markCheckpoint(jobID, ClipJobStatusDownloaded, "", nil)
	defer os.Remove(artlistPath)

	// Deduplicate by source clip identity + keyword: skip new upload when already present.
	if existing := s.finder.FindDownloadedArtlistBySource(kw, artlistClip); existing != nil {
		logger.Info("Skipping upload for already downloaded Artlist source clip",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.String("drive_id", existing.DriveID),
		)
		return *existing, false, true
	}

	normalizedPath, normErr := s.processor.NormalizeClipToSevenSeconds1080p(ctx, artlistPath, artlistClip.Duration)
	if normErr != nil {
		logger.Warn("Artlist clip normalization failed, falling back to yt-dlp search",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.Error(normErr),
		)
		return SearchResult{}, false, false
	}
	s.markCheckpoint(jobID, ClipJobStatusProcessed, "", nil)
	defer os.Remove(normalizedPath)

	visualHash, hashErr := s.processor.ComputeVisualHash(ctx, normalizedPath)
	if hashErr != nil {
		logger.Warn("Failed to compute clip visual hash",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.Error(hashErr),
		)
	}

	if existing := s.finder.FindDownloadedArtlistByVisualAndTitle(kw, visualHash, artlistClip.Name); existing != nil {
		logger.Info("Skipping upload for visually duplicated Artlist clip",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.String("drive_id", existing.DriveID),
		)
		s.markCheckpoint(jobID, ClipJobStatusDone, "", existing)
		return *existing, false, true
	}

	driveResult, upErr := s.uploader.UploadToDrive(ctx, normalizedPath, kw)
	if upErr != nil {
		logger.Warn("Artlist clip upload failed, falling back to yt-dlp search",
			zap.String("keyword", kw),
			zap.String("clip_id", artlistClip.ID),
			zap.Error(upErr),
		)
		return SearchResult{}, false, false
	}
	res := searchResultFromDrive(kw, driveResult)
	s.uploadClipSidecarText(ctx, kw, driveResult, buildArtlistClipSidecarText(kw, artlistClip))
	res.TextDriveID = driveResult.TextFileID
	res.TextDriveURL = driveResult.TextURL
	s.markCheckpoint(jobID, ClipJobStatusUploaded, "", &res)

	s.persister.PersistClipMetadata(kw, driveResult, normalizedPath, &artlistClip, visualHash, nil)

	logger.Info("Dynamic Artlist clip processed and registered",
		zap.String("keyword", kw),
		zap.String("clip_id", artlistClip.ID),
		zap.String("drive_url", driveResult.DriveURL),
	)
	return res, true, true
}

func (s *Service) processYTDLPKeyword(ctx context.Context, kw string, jobID string) (SearchResult, bool, bool) {
	downloadedPath, ytMeta, err := s.downloadClip(ctx, kw)
	if err != nil {
		if errors.Is(err, ErrYouTubeAlreadyDownloaded) {
			if existing := s.finder.FindDownloadedYouTubeByMeta(ytMeta); existing != nil {
				logger.Info("Skipping download for already downloaded YouTube interview hash",
					zap.String("keyword", kw),
					zap.String("video_id", strings.TrimSpace(ytMeta.VideoID)),
					zap.String("drive_id", existing.DriveID),
				)
				s.markCheckpoint(jobID, ClipJobStatusDone, "", existing)
				return *existing, false, true
			}
		}
		logger.Warn("Failed to download clip for keyword",
			zap.String("keyword", kw),
			zap.Error(err),
		)
		return SearchResult{}, false, false
	}
	s.markCheckpoint(jobID, ClipJobStatusDownloaded, "", nil)
	defer os.Remove(downloadedPath)

	s.markCheckpoint(jobID, ClipJobStatusProcessed, "", nil)
	results, uploads, procErr := s.processYouTubeMomentsFromDownloaded(ctx, kw, downloadedPath, ytMeta)
	if procErr != nil || len(results) == 0 {
		logger.Warn("Failed to process yt-dlp moments",
			zap.String("keyword", kw),
			zap.Error(procErr),
		)
		return SearchResult{}, false, false
	}
	s.markCheckpoint(jobID, ClipJobStatusUploaded, "", &results[0])
	return results[0], uploads > 0, true
}
