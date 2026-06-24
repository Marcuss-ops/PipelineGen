package config

import "strings"

// DriveConfig holds Google Drive configuration.
// MediaRootFolder is the single root for ALL media on Drive.
type DriveConfig struct {
	// MediaRootFolder is the single Drive root folder for all PipelineGen media.
	// Example: "1ABCdef..." points to "PipelineGen Media" at Drive root.
	MediaRootFolder string `yaml:"media_root_folder" env:"VELOX_DRIVE_MEDIA_ROOT" default:""`

	// Stock footage root folder
	StockRootFolder string `yaml:"stock_root_folder" env:"VELOX_DRIVE_STOCK_ROOT" default:""`

	// Clips (YouTube/Artlist) root folder
	ClipsRootFolder string `yaml:"clips_root_folder" env:"VELOX_DRIVE_CLIPS_ROOT" default:""`
	// Voiceover root folder
	VoiceoverRootFolder string `yaml:"voiceover_root_folder" env:"VELOX_DRIVE_VOICEOVER_ROOT" default:""`
	// Artlist assets root folder
	ArtlistRootFolder string `yaml:"artlist_root_folder" env:"VELOX_DRIVE_ARTLIST_ROOT" default:""`
	// Books (summarized/rewritten) root folder
	BooksRootFolder string `yaml:"books_root_folder" env:"VELOX_DRIVE_BOOKS_ROOT" default:""`
	// Scripts/docs generation root folder
	ScriptsRootFolder string `yaml:"scripts_root_folder" env:"VELOX_DRIVE_SCRIPTS_ROOT" default:""`
	// ScriptsGenerateFolder is the subfolder for docs generated via /api/script-docs/generate (sync)
	ScriptsGenerateFolder string `yaml:"scripts_generate_folder" env:"VELOX_DRIVE_SCRIPTS_GENERATE" default:""`
	// Video AI generated assets root folder
	VideoAIRootFolder string `yaml:"video_ai_root_folder" env:"VELOX_DRIVE_VIDEO_AI_ROOT" default:""`
	// Images root folder
	ImagesRootFolder string `yaml:"images_root_folder" env:"VELOX_DRIVE_IMAGES_ROOT" default:""`
	// Copertine/thumbnails root folder
	CopertineRootFolder string `yaml:"copertine_root_folder" env:"VELOX_DRIVE_COPERTINE_ROOT" default:""`
	// Sound effects root folder
	SoundEffectsRootFolder string `yaml:"sound_effects_root_folder" env:"VELOX_DRIVE_SOUND_EFFECTS_ROOT" default:""`
	// Avatar AI generated content root folder
	AvatarAIRootFolder string `yaml:"avatar_ai_root_folder" env:"VELOX_DRIVE_AVATAR_AI_ROOT" default:""`
	// SharedWithEmail is the Google account email to automatically share generated docs with.
	SharedWithEmail string `yaml:"shared_with_email" env:"VELOX_DRIVE_SHARED_WITH_EMAIL" default:""`
}

// RootFolder returns the MediaRootFolder.
func (d DriveConfig) RootFolder() string {
	return d.MediaRootFolder
}

// ResolveFolder returns the effective folder ID for a given specific root.
// Priority: MediaRootFolder (unified root) > specific root folder > "".
func (d DriveConfig) ResolveFolder(specificRoot string) string {
	if mediaRoot := strings.TrimSpace(d.MediaRootFolder); mediaRoot != "" {
		return mediaRoot
	}
	return strings.TrimSpace(specificRoot)
}

func (d DriveConfig) resolveSubfolder(parentFolder, specificFolder string) string {
	if specific := strings.TrimSpace(specificFolder); specific != "" {
		return specific
	}
	return d.ResolveFolder(parentFolder)
}

// Convenience resolvers — each returns MediaRootFolder if set, else its own root.
func (d DriveConfig) StockFolder() string     { return d.ResolveFolder(d.StockRootFolder) }
func (d DriveConfig) ClipsFolder() string     { return d.ResolveFolder(d.ClipsRootFolder) }
func (d DriveConfig) VoiceoverFolder() string { return d.ResolveFolder(d.VoiceoverRootFolder) }
func (d DriveConfig) ArtlistFolder() string   { return d.ResolveFolder(d.ArtlistRootFolder) }
func (d DriveConfig) BooksFolder() string     { return d.ResolveFolder(d.BooksRootFolder) }
func (d DriveConfig) ScriptsFolder() string   { return d.ResolveFolder(d.ScriptsRootFolder) }
func (d DriveConfig) ScriptsGenFolder() string {
	return d.resolveSubfolder(d.ScriptsRootFolder, d.ScriptsGenerateFolder)
}
func (d DriveConfig) ImagesFolder() string       { return d.ResolveFolder(d.ImagesRootFolder) }
func (d DriveConfig) VideoAIFolder() string      { return d.ResolveFolder(d.VideoAIRootFolder) }
func (d DriveConfig) CopertineFolder() string    { return d.ResolveFolder(d.CopertineRootFolder) }
func (d DriveConfig) SoundEffectsFolder() string { return d.ResolveFolder(d.SoundEffectsRootFolder) }
