package app

import (
	clips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// clipsCfgAdapter wraps *config.Config to satisfy clips.ClipConfigPort.
// Each method exposes exactly the field the handler reads (Pattern 0:
// minimal — never return the whole *Config). The delegation order
// matches the canonical accessors on *config.Config:
//
//	cfg.Drive.X   for ClipsDriveFolder / ArtlistDriveFolder / StockDriveFolder
//	cfg.Storage.X for MediaPath / TempPath / DataDir / YoutubeClipsPath / AssetsPath
type clipsCfgAdapter struct {
	cfg *config.Config
}

// Compile-time assertion: clipsCfgAdapter satisfies clips.ClipConfigPort.
var _ clips.ClipConfigPort = (*clipsCfgAdapter)(nil)

func newClipsCfgAdapter(cfg *config.Config) clips.ClipConfigPort {
	if cfg == nil {
		return nil
	}
	return &clipsCfgAdapter{cfg: cfg}
}

func (a *clipsCfgAdapter) ClipsDriveFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.ClipsFolder()
}

func (a *clipsCfgAdapter) RootFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.RootFolder()
}

func (a *clipsCfgAdapter) ArtlistDriveFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.ArtlistFolder()
}

func (a *clipsCfgAdapter) StockDriveFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.StockFolder()
}

func (a *clipsCfgAdapter) MediaPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.MediaPath()
}

func (a *clipsCfgAdapter) TempPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.TempPath()
}

func (a *clipsCfgAdapter) DataDir() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.DataDir
}

func (a *clipsCfgAdapter) YoutubeClipsPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.YoutubeClipsPath()
}

func (a *clipsCfgAdapter) AssetsPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.AssetsPath()
}

func (a *clipsCfgAdapter) AssetsStoragePath() string {
	return a.AssetsPath()
}
