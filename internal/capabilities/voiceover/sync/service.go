// Package sync owns the voiceover Drive reconciliation capability:
// walking the canonical voiceover Drive folder and upserting the
// discovered audio files into the voiceovers repository + asset tree.
//
// Canonical owner: internal/capabilities/voiceover (moved from
// internal/capabilities/assets/reconciliation/voiceover in the
// voiceover owner-unification cleanup).
package sync

import (
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
)

type Service struct {
	uploader     *driveup.Uploader
	log          *zap.Logger
	repo         *assets.VoiceoversRepository
	assetTreeSvc *assettree.Service
	rootFolderID string
}

func NewService(uploader *driveup.Uploader, repo *assets.VoiceoversRepository, assetTreeSvc *assettree.Service, rootFolderID string, log *zap.Logger) *Service {
	return &Service{
		uploader:     uploader,
		log:          log,
		repo:         repo,
		assetTreeSvc: assetTreeSvc,
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
