# Changelog

## [Unreleased]

### Added
- **`pkg/pathutil`**: Centralized slug/sanitize functions (`Slug`, `SafeFilename`, `SafeFolderName`, `SafeJoin`)
- **`pkg/hashutil`**: MD5/SHA256 hash utilities (`MD5File`, `SHA256File`, `SHA256Reader`, `SHA256Bytes`)
- **`pkg/executil`**: Command execution wrapper with context support and injection prevention
- **`pkg/media/ffmpeg`**: Video processing package (`Normalize`, `CutSegment`, `Probe`)
- **`pkg/media/downloader`**: yt-dlp downloader with section support and channel listing
- **YouTube clip endpoint**: `POST /api/youtube-clips/extract` with Drive upload and clips DB integration

### Changed
- **`internal/upload/drive`**: Refactored to proper `Uploader` struct with `.Media(f)` upload support
- **`internal/service/artlist`**: Migrated to use modular packages (`downloader`, `ffmpeg`, `hashutil`, `pathutil`, `drive`)
- **`internal/service/youtubeclip`**: Wired with `clips.Repository` and `drive.Service` dependencies

### Removed
- Duplicated slug/sanitize logic from `artlist/run_helpers.go`, `images/service.go`, `voiceover/service.go`
- Inline MD5 calculation from `artlist/pipeline.go`
- Direct `exec.Command` calls replaced with `executil.Run`

### Fixed
- Drive upload in `artlist/drive_uploader.go` (was missing `.Media(f)` call)
- Import conflicts between `google.golang.org/api/drive/v3` and `internal/upload/drive`
- YouTube clip service now properly saves to clips database and uploads to Drive

### Security
- All external command executions now use `exec.CommandContext` (no shell) via `executil` package
- URL validation via `pkg/security.ValidateDownloadURL` enforced for all downloads
- Timestamp validation via `pkg/security.SanitizeTimestamp` for all video segments
