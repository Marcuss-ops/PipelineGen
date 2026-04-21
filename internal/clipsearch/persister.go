package clipsearch

import (
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/stockdb"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type ClipPersister struct {
	stockDB   *stockdb.StockDB
	artlistDB *artlistdb.ArtlistDB
}

func NewClipPersister(stockDB *stockdb.StockDB, artlistDB *artlistdb.ArtlistDB) *ClipPersister {
	return &ClipPersister{
		stockDB:   stockDB,
		artlistDB: artlistDB,
	}
}

func (p *ClipPersister) SaveToStockDB(keyword string, driveResult *DriveUploadResult) error {
	if p.stockDB == nil {
		return fmt.Errorf("StockDB not available")
	}

	folderID := driveResult.FolderID
	if folderID == "" {
		folderID = "Stock/Artlist/" + keyword
	}

	clip := stockdb.StockClipEntry{
		ClipID:   driveResult.DriveID,
		FolderID: folderID,
		Filename: driveResult.Filename,
		Source:   "dynamic",
		Tags:     []string{keyword},
		Duration: 0,
		Status:   "uploaded",
		ErrorLog: "",
	}

	return p.stockDB.UpsertClip(clip)
}

func (p *ClipPersister) SaveJobStatus(keyword, jobID, status, errorLog string) error {
	if p.stockDB == nil {
		return fmt.Errorf("StockDB not available")
	}
	id := strings.TrimSpace(jobID)
	if id == "" {
		return nil
	}
	entry := stockdb.StockClipEntry{
		ClipID:   id,
		FolderID: "",
		Filename: strings.TrimSpace(keyword),
		Source:   "dynamic_job",
		Tags:     []string{keyword, "job"},
		Duration: 0,
		Status:   strings.TrimSpace(status),
		ErrorLog: strings.TrimSpace(errorLog),
	}
	return p.stockDB.UpsertClip(entry)
}

func (p *ClipPersister) SaveToArtlistDB(keyword string, driveResult *DriveUploadResult, downloadPath, visualHash string, ytMeta *YouTubeClipMetadata) error {
	if p.artlistDB == nil {
		return fmt.Errorf("ArtlistDB not available")
	}

	clip := buildDynamicArtlistClip(keyword, driveResult, downloadPath, visualHash, ytMeta)

	if err := p.artlistDB.AddSearchResults(keyword, []artlistdb.ArtlistClip{clip}); err != nil {
		return err
	}
	if strings.TrimSpace(driveResult.FolderID) != "" {
		_ = p.artlistDB.SetDriveFolder(keyword, driveResult.FolderID)
	}
	if err := p.artlistDB.MarkClipDownloaded(clip.ID, keyword, driveResult.DriveID, driveResult.DriveURL, downloadPath); err != nil {
		return err
	}
	return p.artlistDB.Save()
}

func (p *ClipPersister) SaveToArtlistDBFromSource(keyword string, driveResult *DriveUploadResult, downloadPath string, source clip.IndexedClip, visualHash string) error {
	if p.artlistDB == nil {
		return fmt.Errorf("ArtlistDB not available")
	}

	clipRec := buildSourceArtlistClip(keyword, driveResult, downloadPath, source, visualHash)

	if err := p.artlistDB.AddSearchResults(keyword, []artlistdb.ArtlistClip{clipRec}); err != nil {
		return err
	}
	if strings.TrimSpace(driveResult.FolderID) != "" {
		_ = p.artlistDB.SetDriveFolder(keyword, driveResult.FolderID)
	}
	if err := p.artlistDB.MarkClipDownloaded(clipRec.ID, keyword, driveResult.DriveID, driveResult.DriveURL, downloadPath); err != nil {
		return err
	}
	return p.artlistDB.Save()
}

func (p *ClipPersister) PersistClipMetadata(keyword string, driveResult *DriveUploadResult, sourcePath string, artlistClip *clip.IndexedClip, visualHash string, ytMeta *YouTubeClipMetadata) {
	if p.stockDB != nil {
		if err := p.SaveToStockDB(keyword, driveResult); err != nil {
			logger.Warn("Failed to save clip to StockDB",
				zap.String("keyword", keyword),
				zap.Error(err),
			)
		}
	}

	if p.artlistDB == nil {
		return
	}
	var err error
	if artlistClip != nil {
		err = p.SaveToArtlistDBFromSource(keyword, driveResult, sourcePath, *artlistClip, visualHash)
	} else {
		err = p.SaveToArtlistDB(keyword, driveResult, sourcePath, visualHash, ytMeta)
	}
	if err != nil {
		logger.Warn("Failed to save clip to ArtlistDB",
			zap.String("keyword", keyword),
			zap.Error(err),
		)
	}
}

func buildDynamicArtlistClip(keyword string, driveResult *DriveUploadResult, downloadPath, visualHash string, ytMeta *YouTubeClipMetadata) artlistdb.ArtlistClip {
	now := time.Now().Format(time.RFC3339)
	tags := []string{keyword, "dynamic", "auto-registered"}
	originalURL := ""
	url := ""
	if ytMeta != nil {
		if strings.TrimSpace(ytMeta.VideoID) != "" {
			tags = append(tags, "yt_video_id:"+strings.TrimSpace(strings.ToLower(ytMeta.VideoID)))
		}
		if hash := buildYouTubeInterviewHash(ytMeta); hash != "" {
			tags = append(tags, "yt_hash:"+hash)
		}
		if strings.TrimSpace(ytMeta.VideoURL) != "" {
			originalURL = strings.TrimSpace(ytMeta.VideoURL)
			url = originalURL
		}
	}
	return artlistdb.ArtlistClip{
		ID:             "dynamic_" + driveResult.DriveID,
		VideoID:        driveResult.DriveID,
		Title:          driveResult.Filename,
		Name:           driveResult.Filename,
		Term:           keyword,
		Folder:         folderPathForDriveResult(keyword, driveResult),
		FolderID:       driveResult.FolderID,
		OriginalURL:    originalURL,
		URL:            url,
		DriveFileID:    driveResult.DriveID,
		DriveURL:       driveResult.DriveURL,
		DownloadPath:   downloadPath,
		Downloaded:     true,
		DownloadedAt:   now,
		AddedAt:        now,
		Category:       "Dynamic Search",
		Tags:           tags,
		VisualHash:     strings.TrimSpace(visualHash),
		LocalPathDrive: folderPathForDriveResult(keyword, driveResult) + "/" + driveResult.Filename,
	}
}

func buildSourceArtlistClip(keyword string, driveResult *DriveUploadResult, downloadPath string, source clip.IndexedClip, visualHash string) artlistdb.ArtlistClip {
	clipID := source.ID
	if clipID == "" {
		clipID = "artlist_dynamic_" + driveResult.DriveID
	}
	name := source.Name
	if name == "" {
		name = driveResult.Filename
	}
	url := resolveArtlistSourceURL(source)

	tags := make([]string, 0, len(source.Tags)+2)
	tags = append(tags, source.Tags...)
	tags = append(tags, keyword, "artlist")
	now := time.Now().Format(time.RFC3339)

	return artlistdb.ArtlistClip{
		ID:             clipID,
		VideoID:        source.ID,
		Title:          name,
		Name:           driveResult.Filename,
		Term:           keyword,
		Folder:         folderPathForDriveResult(keyword, driveResult),
		FolderID:       driveResult.FolderID,
		OriginalURL:    url,
		URL:            url,
		DriveFileID:    driveResult.DriveID,
		DriveURL:       driveResult.DriveURL,
		DownloadPath:   downloadPath,
		Downloaded:     true,
		DownloadedAt:   now,
		AddedAt:        now,
		Duration:       7,
		Width:          1920,
		Height:         1080,
		Category:       "Dynamic Artlist",
		Tags:           tags,
		VisualHash:     strings.TrimSpace(visualHash),
		LocalPathDrive: folderPathForDriveResult(keyword, driveResult) + "/" + driveResult.Filename,
	}
}

func folderPathForDriveResult(keyword string, driveResult *DriveUploadResult) string {
	if driveResult != nil && strings.TrimSpace(driveResult.FolderPath) != "" {
		return driveResult.FolderPath
	}
	return "Stock/Artlist/" + keyword
}
