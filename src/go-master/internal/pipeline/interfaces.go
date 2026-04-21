package pipeline

import (
	"context"
	"time"
)

// VideoInfo rappresenta i metadati di un video
type VideoInfo struct {
	ID          string
	Title       string
	Channel     string
	Description string
	Duration    int
	UploadDate  time.Time
}

// Highlight rappresenta un segmento interessante trovato dall'IA
type Highlight struct {
	StartSec int    `json:"start_sec"`
	Duration int    `json:"duration"`
	Reason   string `json:"reason"`
}

// Fetcher definisce lo stage di recupero metadati e trascrizioni
type Fetcher interface {
	FetchMetadata(ctx context.Context, videoID string) (*VideoInfo, error)
	FetchTranscript(ctx context.Context, videoID string) (string, error)
}

// Analyzer definisce lo stage di analisi AI per trovare i momenti salienti
type Analyzer interface {
	Analyze(ctx context.Context, info *VideoInfo, transcript string) ([]Highlight, error)
}

// Downloader definisce lo stage di download preciso dei segmenti
type Downloader interface {
	DownloadClip(ctx context.Context, videoID string, start, duration int) (string, error)
}

// Uploader definisce lo stage di caricamento sullo storage (Drive, S3, etc)
type Uploader interface {
	Upload(ctx context.Context, localPath, folderPath string) (string, error)
}
