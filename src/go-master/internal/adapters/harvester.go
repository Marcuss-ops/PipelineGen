// Package adapters provides interface adapters for the harvester
package adapters

import (
	"context"
	"time"

	"velox/go-master/internal/clipdb"
	"velox/go-master/internal/clipprocessor"
	"velox/go-master/internal/downloader"
	"velox/go-master/internal/harvester"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/youtube"
)

// YouTubeSearcherAdapter adapts youtube.Client to harvester.YouTubeSearcher
type YouTubeSearcherAdapter struct {
	client youtube.Client
}

func NewYouTubeSearcherAdapter(client youtube.Client) *YouTubeSearcherAdapter {
	return &YouTubeSearcherAdapter{client: client}
}

func (a *YouTubeSearcherAdapter) Search(ctx context.Context, query string, opts *harvester.SearchOptions) ([]harvester.SearchResult, error) {
	if opts == nil {
		opts = &harvester.SearchOptions{MaxResults: 10}
	}
	ytOpts := &youtube.SearchOptions{
		MaxResults: opts.MaxResults,
		SortBy:     opts.SortBy,
		UploadDate: opts.Timeframe,
	}
	results, err := a.client.Search(ctx, query, ytOpts)
	if err != nil {
		return nil, err
	}

	var adapted []harvester.SearchResult
	for _, r := range results {
		adapted = append(adapted, harvester.SearchResult{
			VideoID:    r.ID,
			Title:      r.Title,
			URL:        r.URL,
			Views:      r.Views,
			Duration:   int(r.Duration.Seconds()),
			Channel:    r.Channel,
			UploadedAt: time.Now(),
			Thumbnail:  r.Thumbnail,
		})
	}
	return adapted, nil
}

func (a *YouTubeSearcherAdapter) SearchByChannel(ctx context.Context, channelID string, opts *harvester.SearchOptions) ([]harvester.SearchResult, error) {
	if opts == nil {
		opts = &harvester.SearchOptions{MaxResults: 10}
	}
	channelOpts := &youtube.ChannelOptions{Limit: opts.MaxResults}
	results, err := a.client.GetChannelVideos(ctx, channelID, channelOpts)
	if err != nil {
		return nil, err
	}

	var adapted []harvester.SearchResult
	for _, r := range results {
		adapted = append(adapted, harvester.SearchResult{
			VideoID:    r.ID,
			Title:      r.Title,
			URL:        r.URL,
			Views:      r.Views,
			Duration:   int(r.Duration.Seconds()),
			Channel:    r.Channel,
			UploadedAt: time.Now(),
			Thumbnail:  r.Thumbnail,
		})
	}
	return adapted, nil
}

func (a *YouTubeSearcherAdapter) GetVideoStats(ctx context.Context, videoID string) (*harvester.SearchResult, error) {
	results, err := a.client.Search(ctx, videoID, &youtube.SearchOptions{MaxResults: 1})
	if err != nil || len(results) == 0 {
		return nil, err
	}
	r := results[0]
	return &harvester.SearchResult{
		VideoID:   r.ID,
		Title:     r.Title,
		URL:       r.URL,
		Views:     r.Views,
		Duration:  int(r.Duration.Seconds()),
		Channel:   r.Channel,
		Thumbnail: r.Thumbnail,
	}, nil
}

// ClipDBToHarvesterAdapter adapts clipdb.ClipDB to harvester.ClipDatabase
type ClipDBToHarvesterAdapter struct {
	db *clipdb.ClipDB
}

func NewClipDBToHarvesterAdapter(db *clipdb.ClipDB) *ClipDBToHarvesterAdapter {
	return &ClipDBToHarvesterAdapter{db: db}
}

func (a *ClipDBToHarvesterAdapter) ClipExists(videoID string) (bool, error) {
	return a.db.ClipExists(videoID)
}

func (a *ClipDBToHarvesterAdapter) AddClip(record *harvester.ClipRecord) error {
	entry := clipdb.ClipEntry{
		ClipID:   record.VideoID,
		Filename: record.Title,
		Source:   "youtube",
		Tags:     []string{},
		Duration: record.Duration,
		DriveURL: record.DriveURL,
	}
	return a.db.AddClip(&entry)
}

func (a *ClipDBToHarvesterAdapter) GetClip(videoID string) (*harvester.ClipRecord, error) {
	entry, err := a.db.GetClip(videoID)
	if err != nil || entry == nil {
		return nil, err
	}
	return &harvester.ClipRecord{
		VideoID:      entry.ClipID,
		Title:        entry.Filename,
		URL:          entry.DriveURL,
		Duration:     entry.Duration,
		DriveFileID:  entry.FolderID,
		Downloaded:   entry.LocalPath != "",
		DownloadPath: entry.LocalPath,
		CreatedAt:    time.Now(),
	}, nil
}

func (a *ClipDBToHarvesterAdapter) UpdateClip(record *harvester.ClipRecord) error {
	entry := clipdb.ClipEntry{
		ClipID:    record.VideoID,
		Filename:  record.Title,
		Source:    "youtube",
		Tags:      []string{},
		Duration:  record.Duration,
		DriveURL:  record.DriveURL,
		LocalPath: record.DownloadPath,
	}
	return a.db.UpdateClip(&entry)
}

// DriveUploaderAdapter adapts drive.Client to harvester
type DriveUploaderAdapter struct {
	client      *drive.Client
	driveFolder string
}

func NewDriveUploaderAdapter(client *drive.Client, driveFolder string) *DriveUploaderAdapter {
	return &DriveUploaderAdapter{
		client:      client,
		driveFolder: driveFolder,
	}
}

func (a *DriveUploaderAdapter) UploadFile(ctx context.Context, localPath, filename, folderID string) (string, string, error) {
	fileID, err := a.client.UploadFile(ctx, localPath, folderID, filename)
	if err != nil {
		return "", "", err
	}
	webURL := "https://drive.google.com/uc?id=" + fileID
	return fileID, webURL, nil
}

func (a *DriveUploaderAdapter) CreateFolder(ctx context.Context, name, parentID string) (string, error) {
	return a.client.CreateFolder(ctx, name, parentID)
}

func (a *DriveUploaderAdapter) GetFolderID(ctx context.Context, name, parentID string) (string, error) {
	folder, err := a.client.GetFolderByName(ctx, name, parentID)
	if err != nil {
		return "", err
	}
	if folder == nil {
		return "", nil
	}
	return folder.ID, nil
}

// DownloaderAdapter adapts downloader.Downloader to harvester
type DownloaderAdapter struct {
	dl downloader.Downloader
}

func NewDownloaderAdapter(dl downloader.Downloader) *DownloaderAdapter {
	return &DownloaderAdapter{dl: dl}
}

func (a *DownloaderAdapter) Download(ctx context.Context, url, outputPath string) error {
	req := &downloader.DownloadRequest{
		URL:        url,
		OutputDir:  "",
		OutputFile: outputPath,
		Format:     "best",
	}
	_, err := a.dl.Download(ctx, req)
	return err
}

// ClipProcessorAdapter adapts clipprocessor to harvester
type ClipProcessorAdapter struct {
	proc *clipprocessor.Processor
}

func NewClipProcessorAdapter(proc *clipprocessor.Processor) *ClipProcessorAdapter {
	return &ClipProcessorAdapter{proc: proc}
}

func (a *ClipProcessorAdapter) Process(ctx context.Context, videoPath string) ([]string, error) {
	if a.proc == nil {
		return nil, nil
	}
	clips, err := a.proc.ProcessVideo(ctx, videoPath)
	if err != nil {
		return nil, err
	}
	if len(clips) == 0 {
		return []string{videoPath}, nil
	}
	var paths []string
	for range clips {
		paths = append(paths, videoPath)
	}
	return paths, nil
}
