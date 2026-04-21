// Package downloader fornisce un'interfaccia unificata per scaricare video da diverse piattaforme
package downloader

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Platform rappresenta una piattaforma video
type Platform string

const (
	PlatformYouTube Platform = "youtube"
	PlatformTikTok  Platform = "tiktok"
	PlatformVimeo   Platform = "vimeo"
)

// VideoInfo informazioni comuni sul video
type VideoInfo struct {
	ID          string        `json:"id"`
	Platform    Platform      `json:"platform"`
	URL         string        `json:"url"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Duration    time.Duration `json:"duration"`
	Thumbnail   string        `json:"thumbnail"`
	Author      string        `json:"author"`
	Views       int64         `json:"views"`
	CreatedAt   time.Time     `json:"created_at"`
	Tags        []string      `json:"tags"`
}

// DownloadRequest richiesta di download comune
type DownloadRequest struct {
	URL         string   `json:"url"`
	Platform    Platform `json:"platform"`
	OutputDir   string   `json:"output_dir"`
	OutputFile  string   `json:"output_file"`
	MaxHeight   int      `json:"max_height"`
	Format      string   `json:"format"`
	CookiesFile string   `json:"cookies_file"`
	Proxy       string   `json:"proxy"`
	UserAgent   string   `json:"user_agent"` // Importante per TikTok
	Retries     int      `json:"retries"`
}

// DownloadResult risultato del download
type DownloadResult struct {
	VideoID   string        `json:"video_id"`
	Platform  Platform      `json:"platform"`
	Title     string        `json:"title"`
	FilePath  string        `json:"file_path"`
	FileSize  int64         `json:"file_size"`
	Duration  time.Duration `json:"duration"`
	Thumbnail string        `json:"thumbnail"`
	Author    string        `json:"author"`
}

// SearchResult risultato di ricerca comune
type SearchResult struct {
	ID         string        `json:"id"`
	Platform   Platform      `json:"platform"`
	URL        string        `json:"url"`
	Title      string        `json:"title"`
	Author     string        `json:"author"`
	Duration   time.Duration `json:"duration"`
	Views      int64         `json:"views"`
	Thumbnail  string        `json:"thumbnail"`
	UploadDate string        `json:"upload_date"`
}

// Downloader interfaccia comune per tutte le piattaforme
type Downloader interface {
	// GetInfo ottiene informazioni sul video
	GetInfo(ctx context.Context, url string) (*VideoInfo, error)
	
	// Download scarica un video
	Download(ctx context.Context, req *DownloadRequest) (*DownloadResult, error)
	
	// Search cerca video sulla piattaforma
	Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error)
	
	// GetTranscript estrae il transcript (se disponibile)
	GetTranscript(ctx context.Context, url string, lang string) (string, error)
	
	// Platform ritorna la piattaforma
	Platform() Platform
	
	// IsAvailable verifica se il downloader è disponibile
	IsAvailable(ctx context.Context) error
}

// PlatformDetector rileva la piattaforma da un URL
type PlatformDetector struct{}

// DetectPlatform rileva la piattaforma da un URL
func (d *PlatformDetector) DetectPlatform(url string) Platform {
	urlLower := strings.ToLower(url)
	
	if isYouTubeURL(urlLower) {
		return PlatformYouTube
	}
	if isTikTokURL(urlLower) {
		return PlatformTikTok
	}
	if isVimeoURL(urlLower) {
		return PlatformVimeo
	}
	
	return ""
}

// ExtractVideoID estrae l'ID del video da un URL
func (d *PlatformDetector) ExtractVideoID(url string) string {
	platform := d.DetectPlatform(url)
	
	switch platform {
	case PlatformYouTube:
		return extractYouTubeID(url)
	case PlatformTikTok:
		return extractTikTokID(url)
	case PlatformVimeo:
		return extractVimeoID(url)
	}
	
	return ""
}

// isValidURL verifica se un URL è valido
func isValidURL(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// isYouTubeURL verifica se è un URL YouTube
func isYouTubeURL(url string) bool {
	return strings.Contains(url, "youtube.com") || 
		   strings.Contains(url, "youtu.be") ||
		   strings.Contains(url, "youtube.com/shorts")
}

// isTikTokURL verifica se è un URL TikTok
func isTikTokURL(url string) bool {
	return strings.Contains(url, "tiktok.com") ||
		   strings.Contains(url, "vm.tiktok.com")
}

// isVimeoURL verifica se è un URL Vimeo
func isVimeoURL(url string) bool {
	return strings.Contains(url, "vimeo.com")
}

// extractYouTubeID estrae ID da URL YouTube
func extractYouTubeID(url string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:v=|/v/|/embed/|youtu\.be/)([a-zA-Z0-9_-]{11})`),
		regexp.MustCompile(`/shorts/([a-zA-Z0-9_-]{11})`),
	}
	
	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(url)
		if len(matches) > 1 {
			return matches[1]
		}
	}
	
	return ""
}

// extractTikTokID estrae ID da URL TikTok
func extractTikTokID(url string) string {
	// TikTok URLs: https://www.tiktok.com/@user/video/ID
	pattern := regexp.MustCompile(`/video/(\d+)`)
	matches := pattern.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	
	return ""
}

// extractVimeoID estrae ID da URL Vimeo
func extractVimeoID(url string) string {
	pattern := regexp.MustCompile(`vimeo\.com/(\d+)`)
	matches := pattern.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	
	return ""
}

// NormalizeURL normalizza un URL
func NormalizeURL(url string) string {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "https://" + url
	}
	return url
}

// BuildCacheKey crea una chiave cache per un video
func BuildCacheKey(platform Platform, videoID string) string {
	return fmt.Sprintf("%s:%s", platform, videoID)
}
