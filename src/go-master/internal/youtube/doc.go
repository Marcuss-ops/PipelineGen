// Package youtube provides a unified interface for interacting with YouTube.
//
// It supports multiple backend implementations, primarily focused on the yt-dlp subprocess backend.
//
// Key features:
// 1. Metadata Retrieval: Fetch detailed information about videos, including titles, durations, and thumbnails.
// 2. Video/Audio Downloads: Support for high-quality video downloads and audio-only extraction (e.g., MP3).
// 3. Search & Discovery: Search for videos, list channel content, and fetch trending videos with advanced filtering.
// 4. Subtitles & Transcripts: Automated extraction and parsing of video subtitles and transcripts into plain text.
// 5. Resilience: Built-in support for retries, exponential backoff, and various yt-dlp extractor arguments to bypass rate limits.
//
// The module is designed to be used via the Client interface, allowing for backend swaps
// and easier testing through mocking.
package youtube
