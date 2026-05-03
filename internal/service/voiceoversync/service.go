package voiceoversync

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/api/drive/v3"

	"velox/go-master/internal/repository/voiceovers"
)

const folderMimeType = "application/vnd.google-apps.folder"

type Service struct {
	driveClient *drive.Service
	log         *zap.Logger
	repo         *voiceovers.Repository
	rootFolderID string
}

func NewService(driveClient *drive.Service, repo *voiceovers.Repository, rootFolderID string, log *zap.Logger) *Service {
	return &Service{
		driveClient: driveClient,
		log:         log,
		repo:         repo,
		rootFolderID: rootFolderID,
	}
}

type Summary struct {
	OK        bool      `json:"ok"`
	RootID    string    `json:"root_id"`
	Synced    int       `json:"synced"`
	Failed    int       `json:"failed"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Error     string    `json:"error,omitempty"`
}

func (s *Service) Sync(ctx context.Context) (*Summary, error) {
	summary := &Summary{
		OK:        true,
		RootID:    s.rootFolderID,
		StartedAt: time.Now().UTC(),
	}

	if s.driveClient == nil {
		summary.OK = false
		summary.Error = "drive client not configured"
		return summary, fmt.Errorf("drive client not configured")
	}

	if s.repo == nil {
		summary.OK = false
		summary.Error = "voiceover repository not configured"
		return summary, fmt.Errorf("voiceover repository not configured")
	}

	if strings.TrimSpace(s.rootFolderID) == "" {
		summary.OK = false
		summary.Error = "voiceover root folder ID not configured"
		return summary, fmt.Errorf("voiceover root folder ID not configured")
	}

	synced, failed, err := s.syncFolderRecursive(ctx, s.rootFolderID, "")
	if err != nil {
		summary.OK = false
		summary.Error = err.Error()
	}

	summary.Synced = synced
	summary.Failed = failed
	summary.EndedAt = time.Now().UTC()

	return summary, err
}

func (s *Service) syncFolderRecursive(ctx context.Context, folderID, folderPath string) (int, int, error) {
	children, err := s.listChildren(ctx, folderID)
	if err != nil {
		return 0, 1, err
	}

	synced := 0
	failed := 0

	for _, child := range children {
		if child == nil {
			continue
		}

		childName := strings.TrimSpace(child.Name)
		if childName == "" {
			childName = child.Id
		}

		childPath := path.Join(folderPath, childName)

		if child.MimeType == folderMimeType {
			subSynced, subFailed, err := s.syncFolderRecursive(ctx, child.Id, childPath)
			if err != nil {
				s.log.Warn("failed to sync subfolder",
					zap.String("folder_id", child.Id),
					zap.Error(err),
				)
			}
			synced += subSynced
			failed += subFailed
		} else if s.isAudioFile(child.Name) {
			if err := s.syncFile(ctx, child, childPath); err != nil {
				s.log.Warn("failed to sync voiceover file",
					zap.String("file_id", child.Id),
					zap.String("name", child.Name),
					zap.Error(err),
				)
				failed++
			} else {
				synced++
			}
		}
	}

	return synced, failed, nil
}

func (s *Service) syncFile(ctx context.Context, file *drive.File, filePath string) error {
	link := strings.TrimSpace(file.WebViewLink)
	if link == "" {
		link = strings.TrimSpace(file.WebContentLink)
	}
	if link == "" {
		link = "https://drive.google.com/file/d/" + file.Id
	}

	// Extract language from filename or path (e.g., test_it.mp3 -> it)
	language := s.extractLanguage(file.Name)

	// Generate an ID based on file hash or drive file ID
	id := "vo_sync_" + file.Id

	// Check if already exists
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Get folder ID for the file
	folderID := ""
	// The parent of the file is the folder it's in
	if len(file.Parents) > 0 {
		folderID = file.Parents[0]
	}

	now := time.Now().UTC()
	rec := &voiceovers.Record{
		ID:           id,
		RequestID:    "sync_" + time.Now().Format("20060102"),
		TextHash:     file.Id, // Use drive file ID as hash for synced files
		TextPreview:  file.Name,
		Language:     language,
		Voice:        "", // Unknown for synced files
		Filename:     file.Name,
		LocalPath:    "",
		CleanedPath:  "",
		FolderID:     folderID,
		FolderPath:   filePath,
		DriveFileID:  file.Id,
		DriveLink:    link,
		DownloadLink: "https://drive.google.com/uc?id=" + file.Id,
		FileHash:     "",
		Status:       "processed",
		Strategy:     "sync",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if existing != nil {
		// Preserve existing fields
		if existing.LocalPath != "" {
			rec.LocalPath = existing.LocalPath
		}
		if existing.CleanedPath != "" {
			rec.CleanedPath = existing.CleanedPath
		}
		if existing.Voice != "" {
			rec.Voice = existing.Voice
		}
		if existing.FileHash != "" {
			rec.FileHash = existing.FileHash
		}
		if !existing.CreatedAt.IsZero() {
			rec.CreatedAt = existing.CreatedAt
		}
	}

	return s.repo.Upsert(ctx, rec)
}

// buildDriveQuery builds a Drive API query string.
// All Drive queries should use this helper to ensure consistent formatting.
func buildDriveQuery(folderID string, extraConditions ...string) string {
	query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	for _, cond := range extraConditions {
		query += " and " + cond
	}
	return query
}

func (s *Service) listChildren(ctx context.Context, folderID string) ([]*drive.File, error) {
	query := buildDriveQuery(folderID)
	call := s.driveClient.Files.List().
		Q(query).
		Fields("nextPageToken, files(id, name, mimeType, webViewLink, webContentLink, parents)").
		PageSize(1000).
		Context(ctx)

	var files []*drive.File
	err := call.Pages(ctx, func(fl *drive.FileList) error {
		files = append(files, fl.Files...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (s *Service) isAudioFile(filename string) bool {
	ext := strings.ToLower(path.Ext(filename))
	return ext == ".mp3" || ext == ".wav" || ext == ".m4a" || ext == ".aac"
}

// validLanguageCodes is an allowlist of valid language codes.
// This prevents naive extraction from misidentifying filenames like "my_audio_final.mp3" as language "final".
var validLanguageCodes = map[string]bool{
	"aa": true, "ab": true, "ae": true, "af": true, "ak": true, "am": true, "an": true, "ar": true, "as": true, "av": true,
	"ay": true, "az": true, "ba": true, "be": true, "bg": true, "bh": true, "bi": true, "bm": true, "bn": true,
	"bo": true, "br": true, "bs": true, "ca": true, "ce": true, "ch": true, "co": true, "cr": true, "cs": true,
	"cu": true, "cv": true, "cy": true, "da": true, "de": true, "dv": true, "dz": true, "ee": true, "el": true,
	"en": true, "eo": true, "es": true, "et": true, "eu": true, "fa": true, "ff": true, "fi": true, "fj": true,
	"fo": true, "fr": true, "fy": true, "ga": true, "gd": true, "gl": true, "gn": true, "gu": true, "gv": true,
	"ha": true, "he": true, "hi": true, "ho": true, "hr": true, "ht": true, "hu": true, "hy": true, "hz": true,
	"ia": true, "id": true, "ie": true, "ig": true, "ii": true, "ik": true, "io": true, "is": true, "it": true,
	"iu": true, "ja": true, "jv": true, "ka": true, "kg": true, "ki": true, "kj": true, "kk": true, "kl": true,
	"km": true, "kn": true, "ko": true, "kr": true, "ks": true, "ku": true, "kv": true, "kw": true, "ky": true,
	"la": true, "lb": true, "lg": true, "li": true, "ln": true, "lo": true, "lt": true, "lu": true, "lv": true,
	"mg": true, "mh": true, "mi": true, "mk": true, "ml": true, "mn": true, "mr": true, "ms": true, "mt": true,
	"my": true, "na": true, "nb": true, "nd": true, "ne": true, "ng": true, "nl": true, "nn": true, "no": true,
	"nr": true, "nv": true, "ny": true, "oc": true, "oj": true, "om": true, "or": true, "os": true, "pa": true,
	"pi": true, "pl": true, "ps": true, "pt": true, "qu": true, "rm": true, "rn": true, "ro": true, "ru": true,
	"rw": true, "sa": true, "sc": true, "sd": true, "se": true, "sg": true, "si": true, "sk": true, "sl": true,
	"sm": true, "sn": true, "so": true, "sq": true, "sr": true, "ss": true, "st": true, "su": true, "sv": true,
	"sw": true, "ta": true, "te": true, "tg": true, "th": true, "ti": true, "tk": true, "tl": true, "tn": true,
	"to": true, "tr": true, "ts": true, "tt": true, "tw": true, "ty": true, "ug": true, "uk": true, "ur": true,
	"uz": true, "ve": true, "vi": true, "vo": true, "wa": true, "wo": true, "xh": true, "yi": true, "yo": true,
	"za": true, "zh": true, "zu": true,
	// Common locale variants
	"en-US": true, "en-GB": true, "en-AU": true, "en-CA": true, "en-IN": true,
	"fr-FR": true, "fr-CA": true,
	"pt-BR": true, "pt-PT": true,
	"es-ES": true, "es-MX": true, "es-AR": true,
	"de-DE": true, "de-AT": true, "de-CH": true,
	"it-IT": true,
	"ja-JP": true, "ko-KR": true, "zh-CN": true, "zh-TW": true,
}

func (s *Service) extractLanguage(filename string) string {
	base := strings.TrimSuffix(filename, path.Ext(filename))
	parts := strings.Split(base, "_")
	if len(parts) > 1 {
		lastPart := parts[len(parts)-1]
		// Check against allowlist of valid language codes
		if validLanguageCodes[strings.ToLower(lastPart)] {
			return strings.ToLower(lastPart)
		}
	}
	return "unknown"
}
