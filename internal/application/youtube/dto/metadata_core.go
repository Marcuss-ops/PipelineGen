// Package dto provides YouTube clip metadata type aliases.
// The metadata subsystem is split across three files for clarity and
// bounded diff surface:
//
//   - metadata_core.go    : ClipMetadataFile alias shim (canonical definition
//     was in youtube/types/; PR-G Phase 1b melts types/ into dto/)
//   - metadata_persist.go : pure helpers (June 2026 extraction)
//   - metadata_persist.go : field accessors and content helpers
package dto

// ClipMetadataFile is the canonical on-disk metadata file written alongside
// each clip. Serialized as JSON (metadata_<clip_id>.json) next to the clip MP4.
// Moved from youtube/types/ into youtube/dto/ during PR-G Phase 1b melt.
// Phase 1b fix: fields restored from metadata_service_write.go usage.
type ClipMetadataFile struct {
	ClipID, ClipTitle, RawTitle, CleanTitle, ShortTitle string   `json:",omitempty"`
	EmbeddingText, VideoTitle, Channel, Description     string   `json:",omitempty"`
	RawTranscript, Transcript, CleanTranscript          string   `json:",omitempty"`
	ClipSummary, Hook                                   string   `json:",omitempty"`
	Topics, Speakers, MentionedPeople, People           []string `json:",omitempty"`
	SourceTags, ClipTags, SearchKeywords                []string `json:",omitempty"`
	DuplicateGroupID, DuplicateOf                       string   `json:",omitempty"`
	IsDuplicate, IsBestVersion                          bool     `json:",omitempty"`
	DuplicateReason                                     string   `json:",omitempty"`
	DuplicateScore                                      float64  `json:",omitempty"`
	TopicClusterID, TopicClusterLabel                   string   `json:",omitempty"`
	TopicClusterSize, TopicClusterRank                  int      `json:",omitempty"`
	Language                                            string   `json:",omitempty"`
	DurationSec, StartSec, EndSec                       int      `json:",omitempty"`
	Tags, Categories                                    []string `json:",omitempty"`
	QualityScore                                        float64  `json:",omitempty"`
	SearchVisibility                                    string   `json:",omitempty"`
	YouTubeURL, ThumbnailURL                            string   `json:",omitempty"`
	UploadDate                                          string   `json:",omitempty"`
	ViewCount                                           int64    `json:",omitempty"`
	LastEnriched                                        string   `json:",omitempty"`
}
